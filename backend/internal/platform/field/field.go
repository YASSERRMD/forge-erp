// Package field implements Kernel 6: custom-field definitions, validation,
// and search indexing over the custom_fields JSONB columns carried by entity
// rows (see docs/DIFFERENCES.md item 3: EAV tables eliminated in favor of
// JSONB with context-level validation).
//
// Definitions scope (entity_id, scope, key) triples to a typed, optionally
// required and searchable field. Context stores validate entity payloads
// through Validator; searchable definitions get a per-key expression index
// via EnsureSearchIndex so JSONB lookups stay indexed.
package field

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// FieldType enumerates custom-field value types (mirrors the DB CHECK).
type FieldType string

// Supported field types.
const (
	TypeText    FieldType = "text"
	TypeNumber  FieldType = "number" // integer only — never float (minor-units rule)
	TypeSelect  FieldType = "select"
	TypeDate    FieldType = "date"
	TypeBoolean FieldType = "boolean"
)

// Well-known definition scopes.
const (
	ScopeOrganization = "organization"
	ScopeProduct      = "product"
)

// Definition is one custom-field declaration for an entity's scope.
type Definition struct {
	ID             int64     `json:"id"`
	EntityID       int64     `json:"entity_id"`
	Scope          string    `json:"scope"`
	Key            string    `json:"key"`
	Type           FieldType `json:"type"`
	Label          string    `json:"label"`
	Required       bool      `json:"required"`
	Options        []string  `json:"options"`
	ValidationRule string    `json:"validation_rule"`
	DisplayOrder   int       `json:"display_order"`
	Searchable     bool      `json:"searchable"`
}

var (
	keyPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	scopePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
)

// ValidateDefinition enforces definition-level rules (entity scoping,
// key/scope shape, known type, select carries options, rule compiles).
func ValidateDefinition(d Definition) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("field: definition: "+format+": %w", append(args, platform.ErrValidation)...)
	}
	if d.EntityID == 0 {
		return fail("entity_id required")
	}
	if !scopePattern.MatchString(d.Scope) {
		return fail("bad scope %q", d.Scope)
	}
	if !keyPattern.MatchString(d.Key) {
		return fail("bad key %q", d.Key)
	}
	switch d.Type {
	case TypeText, TypeNumber, TypeSelect, TypeDate, TypeBoolean:
	default:
		return fail("unknown type %q", d.Type)
	}
	if d.Type == TypeSelect && len(d.Options) == 0 {
		return fail("select field %q requires options", d.Key)
	}
	if d.ValidationRule != "" {
		if _, err := regexp.Compile(d.ValidationRule); err != nil {
			return fail("bad validation rule %q", d.ValidationRule)
		}
	}
	return nil
}

// scopeTables maps a definition scope to the table holding its rows'
// custom_fields column. Seeded with organizations and products; other
// contexts register theirs via RegisterScopeTable. Both singular and plural
// scope spellings resolve to the same table.
var (
	scopeMu     sync.RWMutex
	scopeTables = map[string]string{
		"organization":  "ferp_organizations",
		"organizations": "ferp_organizations",
		"product":       "ferp_products",
		"products":      "ferp_products",
	}
)

// TableForScope resolves the table holding a scope's custom_fields.
func TableForScope(scope string) (string, bool) {
	scopeMu.RLock()
	defer scopeMu.RUnlock()
	t, ok := scopeTables[strings.ToLower(scope)]
	return t, ok
}

// RegisterScopeTable maps an additional scope to its table (context setup).
// Both names must be plain identifiers; the table must carry custom_fields.
func RegisterScopeTable(scope, table string) error {
	if !scopePattern.MatchString(scope) {
		return fmt.Errorf("field: bad scope %q: %w", scope, platform.ErrValidation)
	}
	if !keyPattern.MatchString(table) {
		return fmt.Errorf("field: bad table %q: %w", table, platform.ErrValidation)
	}
	scopeMu.Lock()
	defer scopeMu.Unlock()
	scopeTables[strings.ToLower(scope)] = table
	return nil
}

// Validator checks custom-field payloads against loaded definitions
// (typically Store.ListDefinitions output). The zero value and nil both
// validate nothing, so stores skip validation when no validator is wired.
type Validator struct {
	defs []Definition
}

// NewValidator loads definitions into a validator.
func NewValidator(defs []Definition) *Validator { return &Validator{defs: defs} }

// Validate enforces required/type/options/rule constraints for the defs
// matching (scope, entityID). Keys without a definition are ignored.
func (v *Validator) Validate(scope string, entityID int64, fields map[string]any) error {
	if v == nil {
		return nil
	}
	for _, d := range v.defs {
		if d.EntityID != entityID || d.Scope != scope {
			continue
		}
		val, present := fields[d.Key]
		if !present || isMissing(val) {
			if d.Required {
				return fmt.Errorf("field: %s.%s: required field missing: %w",
					scope, d.Key, platform.ErrValidation)
			}
			continue
		}
		if err := checkType(scope, d, val); err != nil {
			return err
		}
		if d.ValidationRule != "" {
			re, err := regexp.Compile(d.ValidationRule)
			if err != nil {
				return fmt.Errorf("field: %s.%s: bad validation rule: %w",
					scope, d.Key, platform.ErrValidation)
			}
			if !re.MatchString(StringValue(val)) {
				return fmt.Errorf("field: %s.%s: value fails validation rule: %w",
					scope, d.Key, platform.ErrValidation)
			}
		}
	}
	return nil
}

func isMissing(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

func checkType(scope string, d Definition, val any) error {
	fail := func(want string) error {
		return fmt.Errorf("field: %s.%s: expected %s: %w",
			scope, d.Key, want, platform.ErrValidation)
	}
	switch d.Type {
	case TypeText:
		if _, ok := val.(string); !ok {
			return fail("text")
		}
	case TypeNumber:
		if _, ok := asInteger(val); !ok {
			return fail("integer number")
		}
	case TypeSelect:
		s, ok := val.(string)
		if !ok {
			return fail("select option (string)")
		}
		for _, o := range d.Options {
			if o == s {
				return nil
			}
		}
		return fmt.Errorf("field: %s.%s: %q not one of [%s]: %w",
			scope, d.Key, s, strings.Join(d.Options, ","), platform.ErrValidation)
	case TypeDate:
		s, ok := val.(string)
		if !ok || !isDateString(s) {
			return fail("date (YYYY-MM-DD or RFC3339)")
		}
	case TypeBoolean:
		if _, ok := val.(bool); !ok {
			return fail("boolean")
		}
	default:
		return fail(fmt.Sprintf("known type (got %q)", d.Type))
	}
	return nil
}

func isDateString(s string) bool {
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// asInteger coerces whole-number values (never floats with a fraction — the
// minor-units rule). Integral float64/float32 pass because JSON decoding
// produces float64; fractional values are rejected.
func asInteger(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return uintToInt(uint64(n))
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return uintToInt(n)
	case float64:
		return floatToInt(n)
	case float32:
		return floatToInt(float64(n))
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil {
			return floatToInt(f)
		}
		return 0, false
	default:
		return 0, false
	}
}

func uintToInt(n uint64) (int64, bool) {
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func floatToInt(f float64) (int64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return 0, false
	}
	if f > math.MaxInt64 || f < math.MinInt64 {
		return 0, false
	}
	return int64(f), true
}

// StringValue renders a field value in its JSONB text form (what
// custom_fields->>'key' returns): strings verbatim, integers decimal,
// booleans true/false. Partners' memory filter uses this so memory and PG
// comparisons agree.
func StringValue(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case bool:
		return strconv.FormatBool(n)
	case int:
		return strconv.FormatInt(int64(n), 10)
	case int8:
		return strconv.FormatInt(int64(n), 10)
	case int16:
		return strconv.FormatInt(int64(n), 10)
	case int32:
		return strconv.FormatInt(int64(n), 10)
	case int64:
		return strconv.FormatInt(n, 10)
	case uint:
		return strconv.FormatUint(uint64(n), 10)
	case uint8:
		return strconv.FormatUint(uint64(n), 10)
	case uint16:
		return strconv.FormatUint(uint64(n), 10)
	case uint32:
		return strconv.FormatUint(uint64(n), 10)
	case uint64:
		return strconv.FormatUint(n, 10)
	case float64:
		if i, ok := floatToInt(n); ok {
			return strconv.FormatInt(i, 10)
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case float32:
		return StringValue(float64(n))
	case json.Number:
		return n.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// IndexName returns the deterministic expression-index name for a searchable
// definition. Overlong keys fall back to a sha1 suffix (identifiers cap at
// 63 bytes).
func IndexName(table, key string) string {
	base := table + "_cf_" + key + "_idx"
	if len(base) <= 60 {
		return base
	}
	sum := sha1.Sum([]byte(table + "." + key))
	return table + "_cf_" + hex.EncodeToString(sum[:])[:8] + "_idx"
}

// EnsureSearchIndex creates the expression index for a searchable definition:
//
//	CREATE INDEX IF NOT EXISTS <name> ON <table> ((custom_fields->>'key'))
//
// Non-searchable definitions are a no-op (nil). Unknown scopes and bad keys
// are validation errors; the DDL is idempotent, so concurrent callers and
// repeat runs are safe.
func EnsureSearchIndex(ctx context.Context, db platform.DBTX, def Definition) error {
	if !def.Searchable {
		return nil
	}
	table, ok := TableForScope(def.Scope)
	if !ok {
		return fmt.Errorf("field: unknown scope %q: %w", def.Scope, platform.ErrValidation)
	}
	if !keyPattern.MatchString(def.Key) {
		return fmt.Errorf("field: bad key %q: %w", def.Key, platform.ErrValidation)
	}
	// Table and key are registry-validated identifiers; quoting is belt only.
	sql := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON "%s" ((custom_fields->>'%s'))`,
		IndexName(table, def.Key), table, def.Key)
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("field: ensure index %s.%s: %w", def.Scope, def.Key, err)
	}
	return nil
}

// FieldEqualsClause returns a SQL predicate fragment comparing one custom
// field's text form to the placeholder $arg:
//
//	(custom_fields->>'key') = $arg
//
// Callers splice it into a WHERE clause (see partners ListOrgsByField).
func FieldEqualsClause(key string, arg int) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", fmt.Errorf("field: bad key %q: %w", key, platform.ErrValidation)
	}
	if arg <= 0 {
		return "", fmt.Errorf("field: bad placeholder index %d: %w", arg, platform.ErrValidation)
	}
	return fmt.Sprintf("(custom_fields->>'%s') = $%d", key, arg), nil
}
