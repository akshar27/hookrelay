package apikey

import (
	"strings"
	"testing"
)

func TestGenerateVerifyPrefix(t *testing.T) {
	g := Generate()

	if !strings.HasPrefix(g.Raw, "hr_") {
		t.Fatalf("raw key missing scheme: %q", g.Raw)
	}
	if Prefix(g.Raw) != g.Prefix {
		t.Fatalf("Prefix(raw)=%q, want %q", Prefix(g.Raw), g.Prefix)
	}
	if !Verify(g.Raw, g.Hash) {
		t.Fatal("Verify(raw, hash) = false")
	}
	if Verify(g.Raw+"x", g.Hash) {
		t.Fatal("Verify accepted a modified key")
	}
}

func TestGenerateIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		g := Generate()
		if seen[g.Raw] {
			t.Fatal("duplicate key generated")
		}
		seen[g.Raw] = true
	}
}

func TestPrefixRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "nope", "hr_", "hr_short"} {
		if Prefix(in) != "" {
			t.Errorf("Prefix(%q) = %q, want empty", in, Prefix(in))
		}
	}
}
