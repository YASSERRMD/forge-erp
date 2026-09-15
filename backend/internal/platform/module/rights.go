// Rights catalogue: the starter set every deployment seeds on activation.
//
// Triples are (module, entity, action), the same vocabulary identity's
// Require() enforces. Entity names mirror the triples the handlers actually
// gate on (grep mw("…") across contexts), while the finer Dolibarr-style
// entities (sales/invoice, sales/proposal, …) cover the SPA's per-object
// buttons the way Dolibarr's $rights arrays do (lire/creer/supprimer/valider
// → read/write/delete/validate).
package module

// StarterRights returns the bundled rights catalogue for at least sales,
// partners and catalog (plus the registry's own listing right). Activate()
// seeds the activating module's subset into ferp_rights.
func StarterRights() []Right {
	return append([]Right(nil), starterRights...)
}

// RightsFor returns the starter catalogue filtered to one module name
// (empty when the module declares no starter rights).
func RightsFor(name string) []Right {
	var out []Right
	for _, r := range starterRights {
		if r.Module == name {
			out = append(out, r)
		}
	}
	return out
}

var starterRights = []Right{
	// Registry self right: ListModules (GET /api/v1/modules) gates on it so
	// the SPA bootstrap triple is grantable like any other.
	{Module: "module", Entity: "module", Action: "read"},

	// partners — handler triples: organization read/write, contact read/write,
	// category write (+read symmetric); delete mirrors Dolibarr "supprimer".
	{Module: "partners", Entity: "organization", Action: "read"},
	{Module: "partners", Entity: "organization", Action: "write"},
	{Module: "partners", Entity: "organization", Action: "delete"},
	{Module: "partners", Entity: "contact", Action: "read"},
	{Module: "partners", Entity: "contact", Action: "write"},
	{Module: "partners", Entity: "contact", Action: "delete"},
	{Module: "partners", Entity: "category", Action: "read"},
	{Module: "partners", Entity: "category", Action: "write"},

	// catalog — handler triples: product read, stock write (+read symmetric);
	// warehouse + delete mirror Dolibarr product/stock rights.
	{Module: "catalog", Entity: "product", Action: "read"},
	{Module: "catalog", Entity: "product", Action: "write"},
	{Module: "catalog", Entity: "product", Action: "delete"},
	{Module: "catalog", Entity: "warehouse", Action: "read"},
	{Module: "catalog", Entity: "warehouse", Action: "write"},
	{Module: "catalog", Entity: "stock", Action: "read"},
	{Module: "catalog", Entity: "stock", Action: "write"},

	// sales — handler triples: document read/write/validate, shipment write,
	// payment write (+read/validate symmetric); the per-object entities below
	// (proposal/order/invoice) mirror Dolibarr's propal/commande/facture
	// naming (sales/invoice/validate style) for SPA buttons.
	{Module: "sales", Entity: "document", Action: "read"},
	{Module: "sales", Entity: "document", Action: "write"},
	{Module: "sales", Entity: "document", Action: "delete"},
	{Module: "sales", Entity: "document", Action: "validate"},
	{Module: "sales", Entity: "shipment", Action: "read"},
	{Module: "sales", Entity: "shipment", Action: "write"},
	{Module: "sales", Entity: "shipment", Action: "validate"},
	{Module: "sales", Entity: "payment", Action: "read"},
	{Module: "sales", Entity: "payment", Action: "write"},
	{Module: "sales", Entity: "proposal", Action: "read"},
	{Module: "sales", Entity: "proposal", Action: "write"},
	{Module: "sales", Entity: "proposal", Action: "validate"},
	{Module: "sales", Entity: "order", Action: "read"},
	{Module: "sales", Entity: "order", Action: "write"},
	{Module: "sales", Entity: "order", Action: "validate"},
	{Module: "sales", Entity: "invoice", Action: "read"},
	{Module: "sales", Entity: "invoice", Action: "write"},
	{Module: "sales", Entity: "invoice", Action: "validate"},
	{Module: "sales", Entity: "credit", Action: "read"},
	{Module: "sales", Entity: "credit", Action: "write"},
}
