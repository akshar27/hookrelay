package ssrf

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckDialAddr(t *testing.T) {
	bg := context.Background()
	allow := WithAllowPrivate(bg, true)

	cases := []struct {
		name    string
		ctx     context.Context
		addr    string
		wantErr bool
	}{
		{"public ip ok", bg, "93.184.216.34:443", false},
		{"loopback blocked", bg, "127.0.0.1:8080", true},
		{"private blocked", bg, "10.1.2.3:443", true},
		{"link-local blocked", bg, "169.254.169.254:80", true},
		{"loopback allowed with flag", allow, "127.0.0.1:8080", false},
		{"metadata still blocked even with flag? no", allow, "169.254.169.254:80", false},
		{"not an ip", bg, "example.com:443", true},
		{"garbage", bg, "not-an-address", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkDialAddr(c.ctx, c.addr)
			if (err != nil) != c.wantErr {
				t.Fatalf("checkDialAddr(%q) err=%v, wantErr=%v", c.addr, err, c.wantErr)
			}
		})
	}
}

// A client using GuardedDialContext must refuse to connect to a loopback
// server even though the URL host and DNS both say 127.0.0.1 — the check
// happens on the socket the dialer is about to open, not on a separate lookup.
func TestGuardedDialContext_RefusesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "should never reach here")
	}))
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{DialContext: GuardedDialContext(nil)}}

	_, err := client.Get(srv.URL) // srv.URL is http://127.0.0.1:PORT
	if err == nil {
		t.Fatal("expected the guarded dialer to refuse a loopback connection")
	}
	if !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("expected an SSRF block error, got: %v", err)
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error does not unwrap to *ssrf.Error: %v", err)
	}
}

func TestGuardedDialContext_AllowsLoopbackWhenPermitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{DialContext: GuardedDialContext(nil)}}

	req, _ := http.NewRequestWithContext(WithAllowPrivate(context.Background(), true), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected the request to succeed with AllowPrivate, got: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %q", body)
	}
}
