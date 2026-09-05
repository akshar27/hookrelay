// Package filter compiles an endpoint's event-type subscription and matches
// event types against it. This is the reference matcher and the test oracle;
// fan-out does the equivalent match in SQL for speed.
package filter

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Filter is {"mode":"all"} or {"mode":"types","types":[...]} where each entry is
// an exact type ("order.created") or a trailing glob ("invoice.*", "*").
type Filter struct {
	Mode  string   `json:"mode"`
	Types []string `json:"types,omitempty"`
}

// Parse validates and returns a Filter from its JSON form.
func Parse(raw json.RawMessage) (Filter, error) {
	if len(raw) == 0 {
		return Filter{Mode: "all"}, nil
	}
	var f Filter
	if err := json.Unmarshal(raw, &f); err != nil {
		return Filter{}, fmt.Errorf("invalid filter json: %w", err)
	}
	switch f.Mode {
	case "all":
		return Filter{Mode: "all"}, nil
	case "types":
		if len(f.Types) == 0 {
			return Filter{}, fmt.Errorf(`filter mode "types" needs a non-empty "types" list`)
		}
		for _, p := range f.Types {
			if p == "" || strings.ContainsAny(p, " \t\n") {
				return Filter{}, fmt.Errorf("invalid type pattern %q", p)
			}
		}
		return f, nil
	default:
		return Filter{}, fmt.Errorf(`filter mode must be "all" or "types", got %q`, f.Mode)
	}
}

// Matches reports whether an event type is covered by this filter.
func (f Filter) Matches(eventType string) bool {
	if f.Mode == "all" {
		return true
	}
	for _, pat := range f.Types {
		if matchPattern(pat, eventType) {
			return true
		}
	}
	return false
}

func matchPattern(pat, typ string) bool {
	switch {
	case pat == "*":
		return true
	case pat == typ:
		return true
	case strings.HasSuffix(pat, ".*"):
		prefix := pat[:len(pat)-1] // keep the trailing "."
		return strings.HasPrefix(typ, prefix)
	default:
		return false
	}
}

// MarshalJSON gives a stable canonical form for storage.
func (f Filter) MarshalJSON() ([]byte, error) {
	type alias Filter
	return json.Marshal(alias(f))
}
