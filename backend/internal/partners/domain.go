// Package partners implements third parties (Dolibarr llx_societe: customers,
// suppliers, prospects) with contacts (llx_socpeople), parent hierarchies,
// per-entity customer/supplier codes, and cross-entity categories.
package partners

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
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
	ID             int64         `json:"id"`
	EntityID       int64         `json:"entity_id"`
	Name           string        `json:"name"` // llx_societe.nom
	Alias          string        `json:"alias"` // name_alias
	RefExt         string        `json:"ref_ext"` // external reference
	ParentID       *int64        `json:"parent_id"` // parent subsidiary hierarchy (llx_societe.parent)
	Status         OrgStatus     `json:"status"`
	IsCustomer     bool          `json:"is_customer"`
	IsSupplier     bool          `json:"is_supplier"`
	IsProspect     bool          `json:"is_prospect"`
	CustomerCode   string        `json:"customer_code"` // code_client, unique per entity
	SupplierCode   string        `json:"supplier_code"` // code_fournisseur, unique per entity
	Email          string        `json:"email"`
	Phone          string        `json:"phone"`
	Address        Address       `json:"address"`
	AcctCustomer   string        `json:"acct_customer"` // accountancy_code_customer_general
	AcctSupplier   string        `json:"acct_supplier"` // accountancy_code_supplier_general
	CustomFields   map[string]any `json:"custom_fields"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	CreatedBy      *int64        `json:"created_by"`
	UpdatedBy      *int64        `json:"updated_by"`
	RowVersion     int64         `json:"row_version"`
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
	ID           int64          `json:"id"`
	EntityID     int64          `json:"entity_id"`
	OrgID        int64          `json:"org_id"`
	FirstName    string         `json:"first_name"`
	LastName     string         `json:"last_name"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	Role         string         `json:"role"` // billing | shipping | technical | sales | other
	IsDefault    bool           `json:"is_default"`
	CustomFields map[string]any `json:"custom_fields"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	CreatedBy    *int64         `json:"created_by"`
	UpdatedBy    *int64         `json:"updated_by"`
	RowVersion   int64          `json:"row_version"`
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
	ID       int64  `json:"id"`
	EntityID int64  `json:"entity_id"`
	Code     string `json:"code"`
	Label    string `json:"label"`
	Scope    string `json:"scope"` // organization | contact | product | ...
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
