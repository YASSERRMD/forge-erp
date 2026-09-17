"""Pure structural transformers: Dolibarr llx_* rows -> ForgeERP ferp_* rows.

Implements the structural transformations recorded in docs/DIFFERENCES.md
items 2-8. All functions are pure (no I/O) so they unit-test offline.
Money math never touches float after the boundary: the float->minor-units
conversion happens exactly once, here, with documented half-up rounding.

Item map:
  2. float money -> BIGINT minor units (half-up, negatives away from zero)
  3. extrafields EAV tables -> custom_fields JSONB per entity
  4. element_element / element_contact link tables -> source_type/source_id
  5. fractional quantities -> integer base units
  6. one table per document type -> discriminated headers (ferp_documents /
     ferp_supplier_docs + type column)
  7. categorie_* per-entity link tables -> ferp_categories +
     ferp_category_links with scope discriminator
  8. multicurrency rate snapshot -> rate_to_base integer (x1e6)
  (1). llx_ prefix -> ferp_ table renames (RENAME map)
"""

import math
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP

# --- Item 2: money -----------------------------------------------------------

HALF_UP = "half-up"


def _str_to_minor(text, currency_digits):
    """Exact decimal path for string inputs (CSV dumps): Decimal avoids the
    binary-float boundary error (float 1.015 is 1.01499999...)."""
    d = (Decimal(text) * (10 ** currency_digits)).to_integral_value(rounding=ROUND_HALF_UP)
    return int(d)


def money_to_minor(value, currency_digits=2):
    """Convert a Dolibarr float money amount to integer minor units.

    Half-up rounding: 1.015 -> 102, -1.015 -> -102 (halves round AWAY from
    zero). Python's round() is banker's rounding and MUST NOT be used for
    money. Float inputs convert via Decimal(str(value)): str() is the
    shortest round-trip repr, so float 1.015 behaves as Decimal("1.015")
    (102), not as its binary expansion (101). Raises ValueError on
    non-numeric or non-finite input.
    """
    if isinstance(value, str):
        try:
            return _str_to_minor(value.strip(), currency_digits)
        except (InvalidOperation, ValueError):
            raise ValueError("money_to_minor: non-numeric %r" % (value,))
    try:
        f = float(value)
    except (TypeError, ValueError):
        raise ValueError("money_to_minor: non-numeric %r" % (value,))
    if math.isnan(f) or math.isinf(f):
        raise ValueError("money_to_minor: non-finite %r" % (value,))
    try:
        return _str_to_minor(str(f), currency_digits)
    except (InvalidOperation, ValueError):
        raise ValueError("money_to_minor: non-numeric %r" % (value,))


# --- Item 8: multicurrency snapshot ------------------------------------------

def rate_to_base_micro(rate):
    """Convert a float currency rate to integer micro-units (x1e6, half-up).

    Mirrors money_to_minor at 6 digits: rate 1.23456789 -> 1234568.
    """
    return money_to_minor(rate, currency_digits=6)


# --- Item 5: quantities ------------------------------------------------------

def qty_to_base_units(qty):
    """Convert a (possibly fractional) Dolibarr quantity to integer base units.

    ForgeERP stores integer base units only (DIFFERENCES item 5). Fractional
    remainders are rounded half-up and the loss is flagged by
    qty_had_fraction() so the reconciliation report can count affected rows.
    Float inputs convert via Decimal(str(qty)) (same repr-exactness as
    money_to_minor).
    """
    if isinstance(qty, str):
        try:
            return int(Decimal(qty.strip()).to_integral_value(rounding=ROUND_HALF_UP))
        except (InvalidOperation, ValueError):
            raise ValueError("qty_to_base_units: non-numeric %r" % (qty,))
    try:
        f = float(qty)
    except (TypeError, ValueError):
        raise ValueError("qty_to_base_units: non-numeric %r" % (qty,))
    if math.isnan(f) or math.isinf(f):
        raise ValueError("qty_to_base_units: non-finite %r" % (qty,))
    try:
        return int(Decimal(str(f)).to_integral_value(rounding=ROUND_HALF_UP))
    except (InvalidOperation, ValueError):
        raise ValueError("qty_to_base_units: non-numeric %r" % (qty,))


def qty_had_fraction(qty):
    """True when the source quantity was not already a whole number."""
    try:
        f = float(qty)
    except (TypeError, ValueError):
        return False
    return f != math.floor(f)


# --- Item 3: EAV pivot -------------------------------------------------------

def pivot_extrafields(eav_rows):
    """Pivot llx_*_extrafields EAV rows into custom_fields per entity.

    Input rows: dicts with object_type, object_id, field, value.
    Output: {(object_type, object_id): {field: value}} — the JSONB object
    stored in ferp custom_fields. Later rows for the same field win
    (deterministic: callers sort by rowid first). Empty-string values are
    kept verbatim (Dolibarr semantics); a None value drops the key.
    """
    out = {}
    for r in eav_rows:
        key = (r["object_type"], r["object_id"])
        bucket = out.setdefault(key, {})
        if r["value"] is None:
            bucket.pop(r["field"], None)
        else:
            bucket[r["field"]] = r["value"]
    return out


# --- Item 4: lineage ----------------------------------------------------------

# Dolibarr (srctype, targettype) pairs seen in element_element, mapped to the
# ferp_documents.source_type discriminator vocabulary.
LINEAGE_TYPES = {
    "propal": "quote",
    "commande": "order",
    "facture": "invoice",
    "commande_fournisseur": "supplier_order",
    "facture_fournisseur": "supplier_invoice",
}


def lineage_for_target(link_rows, target_type, target_id):
    """Fold element_element rows into one (source_type, source_id) pair.

    The target document carries a single lineage pair; when several source
    links exist the lowest rowid wins deterministically and the surplus
    count is returned so the reconciliation report can flag fan-in loss.
    Returns (source_type, source_id, surplus_count); (None, None, 0) when
    the target has no inbound links.
    """
    inbound = [r for r in link_rows
               if r["targettype"] == target_type and r["targetid"] == target_id]
    inbound.sort(key=lambda r: r.get("rowid", 0))
    if not inbound:
        return None, None, 0
    first = inbound[0]
    return (LINEAGE_TYPES.get(first["srctype"], first["srctype"]),
            first["srcid"], len(inbound) - 1)


# --- Item 6: discriminated headers --------------------------------------------

# Dolibarr per-type table -> (ferp table, discriminated type).
DOC_TYPE_MAP = {
    "llx_propal": ("ferp_documents", "quote"),
    "llx_commande": ("ferp_documents", "order"),
    "llx_facture": ("ferp_documents", "invoice"),
    "llx_commande_fournisseur": ("ferp_supplier_docs", "supplier_order"),
    "llx_facture_fournisseur": ("ferp_supplier_docs", "supplier_invoice"),
}


def header_target(llx_table):
    """Return (ferp_table, type) for a Dolibarr document table, or None."""
    return DOC_TYPE_MAP.get(llx_table)


def map_header(llx_table, row, lineage=None, custom_fields=None, rate=None):
    """Map one Dolibarr document row to a ferp header row.

    Applies items 2 (total), 6 (table+type), 4 (lineage), 3 (custom_fields),
    8 (rate snapshot). Returns None for unknown tables.
    """
    target = header_target(llx_table)
    if target is None:
        return None
    ferp_table, dtype = target
    out = {
        "_table": ferp_table,
        "ref": row.get("ref") or row.get("facnumber") or "%s-%s" % (llx_table, row.get("rowid")),
        "type": dtype,
        "entity_id": int(row.get("entity", 1)),
        "total_minor": money_to_minor(row.get("total_ttc", 0)),
        "status": row.get("fk_statut", 0),
    }
    if lineage is not None:
        out["source_type"], out["source_id"] = lineage[0], lineage[1]
    else:
        out["source_type"], out["source_id"] = None, None
    out["custom_fields"] = custom_fields or {}
    out["rate_to_base"] = rate_to_base_micro(rate) if rate is not None else 1000000
    return out


# --- Item 7: categories -------------------------------------------------------

def map_category_link(entity, object_type, object_id, category_id):
    """Map one categorie_* link row to a ferp_category_links row.

    The scope discriminator is "<object_type>" (product|org|member|...):
    per-entity Dolibarr link tables collapse into one table keyed by scope.
    """
    return {
        "_table": "ferp_category_links",
        "entity_id": int(entity),
        "scope": object_type,
        "object_id": int(object_id),
        "category_id": int(category_id),
    }


# --- Item 1: renames -----------------------------------------------------------

def ferp_table(llx_table):
    """Map a generic llx_* table to its ferp_* name (RENAME rule).

    Document tables are handled by header_target (they merge); this covers
    the 1:1 renames: strip llx_, prefix ferp_. Returns None for EAV/link
    tables, which fold rather than rename (items 3/4/7).
    """
    folded = {"llx_element_element", "llx_element_contact"}
    if llx_table in folded or llx_table.endswith("_extrafields") \
            or llx_table.startswith("llx_categorie_"):
        return None
    if not llx_table.startswith("llx_"):
        return None
    doc_tables = set(DOC_TYPE_MAP)
    if llx_table in doc_tables:
        return DOC_TYPE_MAP[llx_table][0]
    return "ferp_" + llx_table[len("llx_"):]
