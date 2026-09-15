-- 0029_dictionary_seed down (manual recovery only): remove the Kernel 7
-- core seed rows and their dictionary headers. Non-core (admin-added) rows
-- are untouched.
DELETE FROM ferp_dictionary_entries
WHERE is_core
  AND dictionary IN ('country', 'region', 'currency', 'payment_term',
                     'payment_method', 'vat_rate', 'unit', 'civility',
                     'incoterm', 'shipping_mode', 'transport_mode');
DELETE FROM ferp_dictionaries
WHERE code IN ('country', 'region', 'currency', 'payment_term',
               'payment_method', 'vat_rate', 'unit', 'civility',
               'incoterm', 'shipping_mode', 'transport_mode');
