package filter

import (
	"encoding/json"
	"testing"
)

func TestMatches(t *testing.T) {
	tests := []struct {
		name   string
		filter string
		typ    string
		want   bool
	}{
		{"all mode matches anything", `{"mode":"all"}`, "invoice.paid", true},
		{"exact match", `{"mode":"types","types":["order.created"]}`, "order.created", true},
		{"exact non-match", `{"mode":"types","types":["order.created"]}`, "order.shipped", false},
		{"star matches all", `{"mode":"types","types":["*"]}`, "anything.at.all", true},
		{"prefix glob matches child", `{"mode":"types","types":["invoice.*"]}`, "invoice.paid", true},
		{"prefix glob matches deep child", `{"mode":"types","types":["invoice.*"]}`, "invoice.line.added", true},
		{"prefix glob does not match sibling", `{"mode":"types","types":["invoice.*"]}`, "invoices.paid", false},
		{"prefix glob does not match bare stem", `{"mode":"types","types":["invoice.*"]}`, "invoice", false},
		{"any of several", `{"mode":"types","types":["a.x","b.*"]}`, "b.y", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Parse(json.RawMessage(tc.filter))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := f.Matches(tc.typ); got != tc.want {
				t.Errorf("Matches(%q) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

func TestParseRejectsBadFilters(t *testing.T) {
	bad := []string{
		`{"mode":"nonsense"}`,
		`{"mode":"types"}`,
		`{"mode":"types","types":[]}`,
		`{"mode":"types","types":["has space"]}`,
		`{not json`,
	}
	for _, in := range bad {
		if _, err := Parse(json.RawMessage(in)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}

func TestParseEmptyDefaultsToAll(t *testing.T) {
	f, err := Parse(nil)
	if err != nil || f.Mode != "all" {
		t.Fatalf("got %+v, %v", f, err)
	}
}
