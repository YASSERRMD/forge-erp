// Package modulebuilder implements the plugin surface (Dolibarr
// modX.class.php): scaffold generation for new bounded contexts, structural
// manifest validation against the module.Module shape, and install/uninstall
// flows recorded in ferp_modules through a local store mirror. The
// platform/module registry and hook bus are used read-only as libraries and
// are never modified by this package.
package modulebuilder

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/module"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// RightDecl is one (module, entity, action) grant in a manifest.
type RightDecl struct {
	Module string `json:"module"`
	Entity string `json:"entity"`
	Action string `json:"action"`
}

// Validate mirrors module.Right.Validate naming rules (no empties, no
// whitespace or dots) without taking a write dependency on the registry.
func (r RightDecl) Validate() error {
	for name, v := range map[string]string{"module": r.Module, "entity": r.Entity, "action": r.Action} {
		if v == "" {
			return fmt.Errorf("modulebuilder: right %s is empty: %w", name, platform.ErrValidation)
		}
		if strings.ContainsAny(v, " \t.") {
			return fmt.Errorf("modulebuilder: right %s %q contains whitespace or dot: %w", name, v, platform.ErrValidation)
		}
	}
	return nil
}

// Manifest describes a plugin module: name/family/rights/routes present,
// plus optional boot dependencies and a version stamp.
type Manifest struct {
	Name      string      `json:"name"`
	Family    string      `json:"family"`
	DependsOn []string    `json:"depends_on,omitempty"`
	Rights    []RightDecl `json:"rights"`
	Routes    []string    `json:"routes"`
	Version   string      `json:"version,omitempty"`
}

// Validate performs structural validation of the manifest against the
// module.Module interface shape: name/family/rights/routes must be present
// (no temp-dir compile check — presence and naming rules only).
func (m Manifest) Validate() error {
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("modulebuilder: invalid module name %q: %w", m.Name, platform.ErrValidation)
	}
	if strings.TrimSpace(m.Family) == "" {
		return fmt.Errorf("modulebuilder: family required: %w", platform.ErrValidation)
	}
	if len(m.Rights) == 0 {
		return fmt.Errorf("modulebuilder: at least one right required: %w", platform.ErrValidation)
	}
	for _, r := range m.Rights {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	if len(m.Routes) == 0 {
		return fmt.Errorf("modulebuilder: at least one route required: %w", platform.ErrValidation)
	}
	for _, rt := range m.Routes {
		if !strings.HasPrefix(rt, "/") {
			return fmt.Errorf("modulebuilder: route %q must start with /: %w", rt, platform.ErrValidation)
		}
	}
	return nil
}

// AsBase adapts the manifest to the registry shape. This is the compile-time
// proof the manifest covers the module.Module surface (name/family/deps/
// rights); callers may Register the result in a registry read-only.
func (m Manifest) AsBase() module.Base {
	rights := make([]module.Right, 0, len(m.Rights))
	for _, r := range m.Rights {
		rights = append(rights, module.Right{Module: r.Module, Entity: r.Entity, Action: r.Action})
	}
	return module.Base{
		ModName:   m.Name,
		ModFamily: module.Family(m.Family),
		ModDeps:   append([]string(nil), m.DependsOn...),
		ModRights: rights,
	}
}
