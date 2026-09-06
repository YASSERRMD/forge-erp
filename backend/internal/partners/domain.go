// Package partners implements third parties (Dolibarr llx_societe: customers,
// suppliers, prospects) with contacts (llx_socpeople), parent hierarchies,
// per-entity customer/supplier codes, and cross-entity categories.
package partners

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Organization status (Dolibarr llx_societe.status: 1 active, 0 closed).
type OrgStatus int16

const (
	OrgActive   OrgStatus = 1
	OrgInactive OrgStatus = 0
)

// Customer/supplier codes mirror llx_societe.code_client/code_fournisseur (varchar(24)).
var codePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,23}$`)

// Address is a postal address value object (Dolibarr: address/zip/town/fk_departement/fk_pays).
type Address struct {
	Street  string `json:"street"`
	ZIP     string `json:"zip"`
	Town    string `json:"town"`
	State   string `json:"state"`
	Country string `json:"country"` // ISO-3166 alpha-2 (Dolibarr fk_pays equivalent)
}

// Organization is a third party scoped to an entity (Dolibarr llx_societe + entity).
type Organization struct {
	ID             int64
	EntityID       int64
	Name           string // llx_societe.nom
	Alias          string // name_alias
	RefExt         string // external reference
	ParentID       *int64 // parent subsidiary hierarchy (llx_societe.parent)
	Status         OrgStatus
	IsCustomer     bool
	IsSupplier     bool
	IsProspect     bool
	CustomerCode   string // code_client, unique per entity
	SupplierCode   string // code_fournisseur, unique per entity
	Email          string
	Phone          string
	Address        Address
	AcctCustomer   string // accountancy_code_customer_general
	AcctSupplier   string // accountancy_code_supplier_general
	CustomFields   map[string]any
	CreatedBy      *int64
	UpdatedBy      *int64
	RowVersion     int64
}

// Validate enforces master-data rules (characterized from Societe.class.php:
// name mandatory, at least one commercial role, code formats).
func (o Organization) Validate() error {
	if strings.TrimSpace(o.Name) == "" {
		return errors.New("partners: organization name required")
	}
	if !o.IsCustomer && !o.IsSupplier && !o.IsProspect {
		return errors.New("partners: at least one role (customer/supplier/prospect) required")
	}
	if o.IsCustomer && o.CustomerCode != "" && !codePattern.MatchString(o.CustomerCode) {
		return fmt.Errorf("partners: bad customer code %q", o.CustomerCode)
	}
	if o.IsSupplier && o.SupplierCode != "" && !codePattern.MatchString(o.SupplierCode) {
		return fmt.Errorf("partners: bad supplier code %q", o.SupplierCode)
	}
	if o.ParentID != nil && *o.ParentID == o.ID {
		return errors.New("partners: organization cannot be its own parent")
	}
	if o.Email != "" && !strings.Contains(o.Email, "@") {
		return fmt.Errorf("partners: bad email %q", o.Email)
	}
	return nil
}

// CheckNoCycle walks the parent chain via lookup and rejects loops.
// lookup returns (parentID, hasParent, found). Missing rows fail closed.
func CheckNoCycle(id int64, parentID *int64, lookup func(id int64) (parent *int64, found bool)) error {
	seen := map[int64]bool{id: true}
	cur := parentID
	for cur != nil {
		if seen[*cur] {
			return fmt.Errorf("partners: parent hierarchy cycle at %d", *cur)
		}
		seen[*cur] = true
		p, found := lookup(*cur)
		if !found {
			return fmt.Errorf("partners: parent %d not found", *cur)
		}
		cur = p
	}
	return nil
}

// Contact is a person attached to an organization (Dolibarr llx_socpeople;
// roles generalize element_contact contact types).
type Contact struct {
	ID          int64
	EntityID    int64
	OrgID       int64
	FirstName   string
	LastName    string
	Email       string
	Phone       string
	Role        string // billing | shipping | technical | sales | other
	IsDefault   bool
	CustomFields map[string]any
	CreatedBy   *int64
	UpdatedBy   *int64
	RowVersion  int64
}

// ValidRoles enumerates contact roles.
var ValidRoles = map[string]bool{
	"billing": true, "shipping": true, "technical": true, "sales": true, "other": true,
}

// Validate enforces contact rules.
func (c Contact) Validate() error {
	if c.OrgID == 0 {
		return errors.New("partners: contact requires an organization")
	}
	if strings.TrimSpace(c.FirstName) == "" && strings.TrimSpace(c.LastName) == "" {
		return errors.New("partners: contact requires a name")
	}
	if c.Role != "" && !ValidRoles[c.Role] {
		return fmt.Errorf("partners: unknown contact role %q", c.Role)
	}
	if c.Email != "" && !strings.Contains(c.Email, "@") {
		return fmt.Errorf("partners: bad email %q", c.Email)
	}
	return nil
}

// Category scopes cross-entity tags (Dolibarr llx_categorie + categorie_* link tables,
// collapsed into one table with a scope discriminator).
type Category struct {
	ID       int64
	EntityID int64
	Code     string
	Label    string
	Scope    string // organization | contact | product | ...
}

// Validate scopes.
func (c Category) Validate() error {
	if strings.TrimSpace(c.Code) == "" || strings.TrimSpace(c.Label) == "" {
		return errors.New("partners: category code and label required")
	}
	if strings.TrimSpace(c.Scope) == "" {
		return errors.New("partners: category scope required")
	}
	return nil
}
