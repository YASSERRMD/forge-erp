"""Unit tests for the Dolibarr structural transformers (offline, stdlib only).

Run: python3 test_transformers.py  (or: python3 -m unittest test_transformers -v)
Covers DIFFERENCES items 2-8: money vectors incl. negatives, EAV pivot,
lineage mapping, qty conversion, header discrimination, category scope,
rate snapshots, renames.
"""

import unittest

from transformers import (
    ferp_table,
    header_target,
    lineage_for_target,
    map_category_link,
    map_header,
    money_to_minor,
    pivot_extrafields,
    qty_had_fraction,
    qty_to_base_units,
    rate_to_base_micro,
)


class MoneyTest(unittest.TestCase):
    def test_basic(self):
        self.assertEqual(money_to_minor(10.00), 1000)
        self.assertEqual(money_to_minor("19.99"), 1999)
        self.assertEqual(money_to_minor(0), 0)
        self.assertEqual(money_to_minor(0.01), 1)

    def test_half_up_positive(self):
        # 1.005 cannot be represented exactly; use values that are exact-ish
        # in binary: 2.675 in float is 2.67499999... -> documents float risk.
        self.assertEqual(money_to_minor(1.015), 102)   # 101.5 float-safe side
        self.assertEqual(money_to_minor(2.5, 0), 3)
        self.assertEqual(money_to_minor(1.5, 0), 2)

    def test_half_up_negative_away_from_zero(self):
        self.assertEqual(money_to_minor(-19.99), -1999)
        self.assertEqual(money_to_minor(-1.5, 0), -2)
        self.assertEqual(money_to_minor(-2.5, 0), -3)
        # symmetry: |f(-x)| == |f(x)|
        for v in (19.99, 0.01, 100.455, 7.5):
            self.assertEqual(abs(money_to_minor(-v)), abs(money_to_minor(v)))

    def test_not_bankers(self):
        # round() would give 2 for 2.5 at 0 digits; half-up gives 3.
        self.assertEqual(money_to_minor(2.5, 0), 3)
        self.assertNotEqual(round(2.5), 3)  # pins WHY we don't use round()

    def test_currency_digits(self):
        self.assertEqual(money_to_minor(10.5, 0), 11)
        self.assertEqual(money_to_minor(10.1234, 3), 10123)
        self.assertEqual(money_to_minor(10.1235, 3), 10124)  # half-up at 3dp

    def test_rejects_garbage(self):
        for bad in ("abc", None, float("nan"), float("inf")):
            with self.assertRaises(ValueError):
                money_to_minor(bad)

    def test_large_amounts_exact(self):
        self.assertEqual(money_to_minor(999999999.99), 99999999999)


class RateTest(unittest.TestCase):
    def test_snapshot_micro(self):
        self.assertEqual(rate_to_base_micro(1.0), 1000000)
        self.assertEqual(rate_to_base_micro(1.23456789), 1234568)  # half-up
        self.assertEqual(rate_to_base_micro(0.0), 0)


class QtyTest(unittest.TestCase):
    def test_whole(self):
        self.assertEqual(qty_to_base_units(5), 5)
        self.assertEqual(qty_to_base_units("3.0"), 3)

    def test_fraction_rounds_half_up(self):
        self.assertEqual(qty_to_base_units(2.4), 2)
        self.assertEqual(qty_to_base_units(2.5), 3)
        self.assertEqual(qty_to_base_units(-2.5), -3)

    def test_fraction_flag(self):
        self.assertFalse(qty_had_fraction(4))
        self.assertFalse(qty_had_fraction(4.0))
        self.assertTrue(qty_had_fraction(4.25))


class EavTest(unittest.TestCase):
    def test_pivot_groups_by_entity(self):
        rows = [
            {"object_type": "product", "object_id": 1, "field": "color", "value": "red"},
            {"object_type": "product", "object_id": 1, "field": "size", "value": "XL"},
            {"object_type": "org", "object_id": 9, "field": "vat_number", "value": "FR123"},
        ]
        out = pivot_extrafields(rows)
        self.assertEqual(out[("product", 1)], {"color": "red", "size": "XL"})
        self.assertEqual(out[("org", 9)], {"vat_number": "FR123"})

    def test_none_drops_key(self):
        rows = [{"object_type": "p", "object_id": 1, "field": "x", "value": "a"},
                {"object_type": "p", "object_id": 1, "field": "x", "value": None}]
        self.assertEqual(pivot_extrafields(rows), {("p", 1): {}})

    def test_empty_string_kept(self):
        rows = [{"object_type": "p", "object_id": 1, "field": "x", "value": ""}]
        self.assertEqual(pivot_extrafields(rows)[("p", 1)], {"x": ""})


class LineageTest(unittest.TestCase):
    LINKS = [
        {"rowid": 7, "srctype": "propal", "srcid": 3, "targettype": "facture", "targetid": 11},
        {"rowid": 2, "srctype": "commande", "srcid": 5, "targettype": "facture", "targetid": 11},
    ]

    def test_first_rowid_wins_with_vocab_map(self):
        st, sid, surplus = lineage_for_target(self.LINKS, "facture", 11)
        self.assertEqual((st, sid), ("order", 5))  # rowid 2 < 7, commande->order
        self.assertEqual(surplus, 1)

    def test_no_links(self):
        self.assertEqual(lineage_for_target(self.LINKS, "facture", 99), (None, None, 0))

    def test_unknown_type_passthrough(self):
        links = [{"rowid": 1, "srctype": "contrat", "srcid": 4,
                  "targettype": "facture", "targetid": 1}]
        st, sid, surplus = lineage_for_target(links, "facture", 1)
        self.assertEqual((st, sid, surplus), ("contrat", 4, 0))


class HeaderTest(unittest.TestCase):
    def test_discrimination(self):
        self.assertEqual(header_target("llx_facture"), ("ferp_documents", "invoice"))
        self.assertEqual(header_target("llx_propal"), ("ferp_documents", "quote"))
        self.assertEqual(header_target("llx_commande"), ("ferp_documents", "order"))
        self.assertEqual(header_target("llx_facture_fournisseur"),
                         ("ferp_supplier_docs", "supplier_invoice"))
        self.assertIsNone(header_target("llx_societe"))

    def test_map_header_money_and_lineage(self):
        row = {"rowid": 1, "facnumber": "FA-1", "entity": 2, "total_ttc": 119.6, "fk_statut": 1}
        out = map_header("llx_facture", row, lineage=("order", 5, 0),
                         custom_fields={"x": "y"}, rate=1.1)
        self.assertEqual(out["_table"], "ferp_documents")
        self.assertEqual(out["type"], "invoice")
        self.assertEqual(out["total_minor"], 11960)
        self.assertEqual(out["source_type"], "order")
        self.assertEqual(out["source_id"], 5)
        self.assertEqual(out["rate_to_base"], 1100000)

    def test_map_header_defaults(self):
        out = map_header("llx_propal", {"rowid": 9})
        self.assertEqual(out["entity_id"], 1)
        self.assertEqual(out["rate_to_base"], 1000000)
        self.assertEqual(out["custom_fields"], {})

    def test_unknown_table_none(self):
        self.assertIsNone(map_header("llx_societe", {"rowid": 1}))


class CategoryTest(unittest.TestCase):
    def test_scope_discriminator(self):
        link = map_category_link(1, "product", 42, 7)
        self.assertEqual(link["_table"], "ferp_category_links")
        self.assertEqual(link["scope"], "product")


class RenameTest(unittest.TestCase):
    def test_plain_rename(self):
        self.assertEqual(ferp_table("llx_societe"), "ferp_societe")
        self.assertEqual(ferp_table("llx_product"), "ferp_product")

    def test_doc_tables_merge(self):
        self.assertEqual(ferp_table("llx_facture"), "ferp_documents")

    def test_folded_tables_none(self):
        self.assertIsNone(ferp_table("llx_element_element"))
        self.assertIsNone(ferp_table("llx_product_extrafields"))
        self.assertIsNone(ferp_table("llx_categorie_product"))


if __name__ == "__main__":
    unittest.main()
