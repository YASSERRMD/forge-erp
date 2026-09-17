#!/usr/bin/env python3
"""Dolibarr llx_* -> ForgeERP ferp_* migration pipeline (offline, idempotent).

Reads Dolibarr table dumps as CSV (one file per table, e.g. llx_facture.csv)
plus optional link/EAV/category files, applies the structural transformations
from docs/DIFFERENCES.md items 2-8, and writes one deterministic JSON file per
ferp_* table. Re-running on the same input yields byte-identical output
(idempotent: rows are keyed by natural key, outputs sorted, files rewritten).

Usage:
    python3 dolibarr_import.py --demo --dry-run        # offline self-check, no I/O
    python3 dolibarr_import.py --demo --output out/     # write demo ferp_*.json
    python3 dolibarr_import.py --input dumps/ --output out/ [--dry-run]

Input layout (--input DIR): llx_<table>.csv files with a header row including
rowid. Recognized: llx_propal, llx_commande, llx_facture,
llx_commande_fournisseur, llx_facture_fourn (document headers);
llx_element_element (lineage); llx_*_extrafields (EAV: object rowid carried in
column `fk_object`); llx_categorie + llx_categorie_<scope> (categories).
Any other llx_*.csv is counted and passed through with the llx_->ferp_ rename
(item 1) so no source table is silently dropped.
"""

import argparse
import csv
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from transformers import (  # noqa: E402
    ferp_table,
    header_target,
    lineage_for_target,
    map_category_link,
    map_header,
    pivot_extrafields,
    qty_had_fraction,
    qty_to_base_units,
)

DOC_TABLES = ("llx_propal", "llx_commande", "llx_facture",
              "llx_commande_fournisseur", "llx_facture_fourn")


def demo_rows():
    """Synthetic llx_* rows so --demo runs with zero input files."""
    docs = {
        "llx_propal": [{"rowid": "1", "ref": "PR-1", "entity": "1",
                        "total_ttc": "120.00", "fk_statut": "2"}],
        "llx_commande": [{"rowid": "5", "ref": "CO-5", "entity": "1",
                          "total_ttc": "240.50", "fk_statut": "1"}],
        "llx_facture": [{"rowid": "11", "facnumber": "FA-11", "entity": "1",
                         "total_ttc": "119.60", "fk_statut": "1"},
                        {"rowid": "12", "facnumber": "FA-12", "entity": "2",
                         "total_ttc": "-15.755", "fk_statut": "0"}],
        "llx_facture_fourn": [{"rowid": "3", "ref": "SF-3", "entity": "1",
                               "total_ttc": "500.00", "fk_statut": "1"}],
    }
    links = [
        {"rowid": 7, "srctype": "propal", "srcid": 1,
         "targettype": "facture", "targetid": 11},
        {"rowid": 2, "srctype": "commande", "srcid": 5,
         "targettype": "facture", "targetid": 11},
    ]
    eav = [
        {"object_type": "facture", "object_id": 11,
         "field": "delivery_note", "value": "leave at door"},
        {"object_type": "facture", "object_id": 12,
         "field": "vat_number", "value": "FR123456789"},
    ]
    categories = [(1, "product", 42, 7)]
    return docs, links, eav, categories


def read_csv(path):
    with open(path, newline="", encoding="utf-8-sig") as f:
        return list(csv.DictReader(f))


def load_input(input_dir):
    """Load llx_*.csv dumps. Returns (docs, links, eav, categories, passthrough)."""
    docs, links, eav, categories, passthrough = {}, [], [], [], {}
    if not input_dir:
        return docs, links, eav, categories, passthrough
    for name in sorted(os.listdir(input_dir)):
        if not name.endswith(".csv"):
            continue
        table = name[:-4]
        rows = read_csv(os.path.join(input_dir, name))
        if table in DOC_TABLES:
            docs[table] = rows
        elif table == "llx_element_element":
            for r in rows:
                links.append({"rowid": int(r.get("rowid", 0)),
                              "srctype": r.get("srctype", ""),
                              "srcid": int(r.get("fk_source", 0)),
                              "targettype": r.get("targettype", ""),
                              "targetid": int(r.get("fk_target", 0))})
        elif table.endswith("_extrafields"):
            obj = table[len("llx_"):-len("_extrafields")]
            for r in rows:
                oid = r.get("fk_object", r.get("rowid"))
                for field, value in r.items():
                    if field in ("rowid", "fk_object", "tms", "entity"):
                        continue
                    eav.append({"object_type": obj, "object_id": int(oid),
                                "field": field, "value": value or None})
        elif table.startswith("llx_categorie_") and table != "llx_categorie":
            scope = table[len("llx_categorie_"):]
            for r in rows:
                categories.append((int(r.get("entity", 1)), scope,
                                   int(r.get("fk_object", 0)),
                                   int(r.get("fk_categorie", 0))))
        else:
            passthrough[table] = rows
    return docs, links, eav, categories, passthrough


def canon(row):
    return json.dumps(row, sort_keys=True, ensure_ascii=True, default=str)


def run(docs, links, eav, categories, passthrough, sample_n=5):
    """Transform everything. Returns (outputs, report).

    outputs: {ferp_table: [rows]} with deterministic order and natural-key
    dedupe (idempotency). report: reconciliation dict with per-table
    in/out counts, row errors, fractional-qty flags, lineage fan-in flags,
    and a hash-sample check.
    """
    outputs, errors = {}, []
    in_counts, frac_qty, fanin = {}, 0, 0

    custom = pivot_extrafields(eav)

    for llx_table, rows in docs.items():
        in_counts[llx_table] = len(rows)
        for r in rows:
            try:
                oid = int(r.get("rowid", 0))
                obj = llx_table[len("llx_"):]
                # quantity sanity probe (item 5) when a qty column exists
                for qcol in ("qty", "qty_total", "nbproduct"):
                    if qcol in r and r[qcol] not in (None, ""):
                        qty_to_base_units(r[qcol])
                        if qty_had_fraction(r[qcol]):
                            frac_qty += 1
                        break
                lin = lineage_for_target(links, obj, oid)
                if lin[2]:
                    fanin += 1
                header = map_header(
                    llx_table, r,
                    lineage=lin,
                    custom_fields=custom.get((obj, oid), {}),
                    rate=float(r["multicurrency_tx"]) if r.get("multicurrency_tx") else None,
                )
                outputs.setdefault(header["_table"], []).append(header)
            except Exception as exc:  # per-row errors never abort the run
                errors.append({"table": llx_table, "rowid": r.get("rowid"), "error": str(exc)})

    for entity, scope, oid, cid in categories:
        outputs.setdefault("ferp_category_links", []).append(
            map_category_link(entity, scope, oid, cid))
    in_counts["categorie_links"] = len(categories)
    in_counts["extrafields"] = len(eav)
    in_counts["element_element"] = len(links)

    for llx_table, rows in passthrough.items():
        name = ferp_table(llx_table) or ("ferp_" + llx_table)
        in_counts[llx_table] = len(rows)
        outputs.setdefault(name, []).extend(dict(r) for r in rows)

    # Idempotency: natural-key dedupe + deterministic order.
    for table, rows in outputs.items():
        seen, uniq = set(), []
        for r in rows:
            key = canon({k: v for k, v in r.items() if k != "custom_fields"})
            if key not in seen:
                seen.add(key)
                uniq.append(r)
        uniq.sort(key=canon)
        outputs[table] = uniq

    out_counts = {t: len(rows) for t, rows in outputs.items()}

    # Hash-sample check: sha256 over the first N canonical rows in vs out.
    sample_src, sample_out = [], []
    for llx_table in sorted(docs):
        sample_src.extend(canon(r) for r in docs[llx_table][:sample_n])
    for table in sorted(outputs):
        sample_out.extend(canon(r) for r in outputs[table][:sample_n])
    digest = lambda rows: hashlib.sha256("\n".join(sorted(rows)).encode()).hexdigest()

    report = {
        "in_counts": in_counts,
        "out_counts": out_counts,
        "row_errors": errors,
        "fractional_qty_rows": frac_qty,
        "lineage_fanin_targets": fanin,
        "hash_sample": {"n_in": len(sample_src), "n_out": len(sample_out),
                        "in_sha256": digest(sample_src),
                        "out_sha256": digest(sample_out)},
    }
    return outputs, report


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Migrate Dolibarr llx_* CSV dumps to ForgeERP ferp_* JSON "
                    "(DIFFERENCES items 2-8). Offline. Idempotent.")
    ap.add_argument("--input", default=None,
                    help="directory of llx_*.csv dumps (omit with --demo)")
    ap.add_argument("--output", default=None,
                    help="directory for ferp_*.json output (required unless --dry-run)")
    ap.add_argument("--demo", action="store_true",
                    help="use a built-in synthetic llx dataset (no input files)")
    ap.add_argument("--dry-run", action="store_true",
                    help="transform and report without writing any files")
    ap.add_argument("--sample", type=int, default=5,
                    help="rows per table for the hash-sample check (default 5)")
    args = ap.parse_args(argv)

    if not args.demo and not args.input:
        ap.error("need --input DIR or --demo")
    if not args.dry_run and not args.output:
        ap.error("need --output DIR or --dry-run")

    if args.demo:
        docs, links, eav, categories = demo_rows()
        passthrough = {}
    else:
        docs, links, eav, categories, passthrough = load_input(args.input)

    outputs, report = run(docs, links, eav, categories, passthrough, args.sample)

    written = []
    if not args.dry_run:
        os.makedirs(args.output, exist_ok=True)
        for table in sorted(outputs):
            path = os.path.join(args.output, table + ".json")
            with open(path, "w", encoding="utf-8") as f:
                json.dump(outputs[table], f, indent=1, sort_keys=True,
                          ensure_ascii=True, default=str)
                f.write("\n")
            written.append(path)

    print(json.dumps({"report": report, "written": written,
                      "dry_run": args.dry_run}, indent=1, sort_keys=True))
    return 1 if report["row_errors"] else 0


if __name__ == "__main__":
    sys.exit(main())
