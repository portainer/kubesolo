package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/portainer/kubesolo/types"
)

// Descriptor describes one setting for something that has to present it — the
// CLI's schema command, and the config API's schema endpoint.
//
// It is derived entirely from Registry(), so a setting cannot be added without
// appearing here.
type Descriptor struct {
	Path string `json:"path"`

	// Type is the JSON type of the value: string, boolean, integer, array or
	// object.
	Type string `json:"type"`

	// Default is what the setting holds when nothing sets it.
	Default any `json:"default"`

	// Flag is the deprecated CLI flag, without leading dashes. Absent for
	// settings that never had one.
	Flag  string `json:"flag,omitempty"`
	Envar string `json:"envar,omitempty"`

	// Mutability is "restart" or "immutable" — whether changing this on an
	// existing install can take effect at all.
	Mutability string `json:"mutability"`

	// Secret marks a value that is redacted when read back.
	Secret bool `json:"secret,omitempty"`
}

// Describe returns every setting, ordered by path.
func Describe() []Descriptor {
	defaults := Defaults()

	registry := Registry()
	out := make([]Descriptor, 0, len(registry))
	for _, f := range registry {
		value := f.Get(defaults)
		kind := jsonType(value)

		// A secret's default is never interesting and must not be echoed back.
		// The type is taken first, or it would fall through to "string" by
		// accident rather than by fact.
		if f.Secret {
			value = nil
		}

		out = append(out, Descriptor{
			Path:       f.ConfigPath,
			Type:       kind,
			Default:    value,
			Flag:       f.Flag,
			Envar:      f.Envar,
			Mutability: f.Mutability.String(),
			Secret:     f.Secret,
		})
	}

	slices.SortFunc(out, func(a, b Descriptor) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// jsonType names the JSON type a value serialises to.
func jsonType(v any) string {
	switch v.(type) {
	case bool, *bool:
		return "boolean"
	case int:
		return "integer"
	case []string:
		return "array"
	case map[string]string:
		return "object"
	default:
		return "string"
	}
}

// MergePatchFor builds the smallest RFC 7386 merge patch that sets one setting
// to the value it holds in cfg.
//
// A dotted config path is also the path through the JSON document — "network.
// nodeIP" is {"network":{"nodeIP":...}} — so the patch is that nesting with the
// typed value at the leaf. Taking the value from a marshalled document rather
// than from Get keeps the JSON representation authoritative: a map, a list and
// an int each land in the patch exactly as the server would read them back.
func MergePatchFor(path string, cfg *types.Config) ([]byte, error) {
	document := map[string]any{}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}

	segments := strings.Split(path, ".")

	// Walk down to the leaf's value in the full document.
	var value any = document
	for _, segment := range segments {
		container, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %q is not an object", path, segment)
		}
		value, ok = container[segment]
		if !ok {
			// Absent because of omitempty — a nil list or map, or an unset
			// pointer. Null is the correct patch value: it restores the default.
			value = nil
			break
		}
	}

	// Rebuild the nesting around it, innermost first.
	patch := value
	for i := len(segments) - 1; i >= 0; i-- {
		patch = map[string]any{segments[i]: patch}
	}

	return json.Marshal(patch)
}
