package ssrf

import (
	"net/netip"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"1.1.1.1", false},
		{"93.184.216.34", false}, // example.com
		{"2606:2800:220:1:248:1893:25c8:1946", false},
		{"127.0.0.1", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"10.0.0.5", true},
		{"172.16.9.9", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"169.254.1.1", true},     // link-local
		{"fd00::1", true},         // ULA
		{"::ffff:10.0.0.1", true}, // v4-mapped private
	}
	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			ip := netip.MustParseAddr(tc.ip)
			if got := BlockedIP(ip); got != tc.blocked {
				t.Errorf("BlockedIP(%s) = %v, want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	ok := []string{
		"https://example.com/hook",
		"https://api.customer.io/webhooks/abc",
	}
	for _, u := range ok {
		if _, err := ValidateURL(u, false, false); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", u, err)
		}
	}

	bad := []string{
		"http://example.com/hook",  // not https
		"ftp://example.com",        // wrong scheme
		"https://127.0.0.1/hook",   // loopback literal
		"https://10.1.2.3/hook",    // private literal
		"https://169.254.169.254/", // metadata literal
		"https://[::1]/hook",       // ipv6 loopback
		"not a url at all ::::",    // unparseable
	}
	for _, u := range bad {
		if _, err := ValidateURL(u, false, false); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", u)
		}
	}

	// http allowed when opted in; private literal allowed when opted in
	if _, err := ValidateURL("http://example.com", true, false); err != nil {
		t.Errorf("insecure opt-in: %v", err)
	}
	if _, err := ValidateURL("https://10.0.0.9/hook", false, true); err != nil {
		t.Errorf("private opt-in: %v", err)
	}
}
