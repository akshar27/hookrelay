package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// allowPrivateKey carries the per-delivery "allow private destinations" flag
// through the request context down to the dialer.
type allowPrivateKey struct{}

// WithAllowPrivate returns a context that permits the guarded dialer to connect
// to otherwise-blocked addresses. Set this per delivery from Endpoint.AllowPrivate.
func WithAllowPrivate(ctx context.Context, allow bool) context.Context {
	return context.WithValue(ctx, allowPrivateKey{}, allow)
}

func allowPrivateFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(allowPrivateKey{}).(bool)
	return v
}

// checkDialAddr validates the concrete IP:port the dialer is about to connect
// to. It runs *after* DNS resolution and *immediately before* the socket
// connects, so the address checked is the address used — closing the
// resolve-then-connect (DNS-rebinding) window that a separate pre-flight
// lookup leaves open.
func checkDialAddr(ctx context.Context, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return &Error{Reason: fmt.Sprintf("cannot parse dial address %q: %v", address, err)}
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return &Error{Reason: fmt.Sprintf("dial address is not a literal IP: %q", host)}
	}
	if BlockedIP(ip) && !allowPrivateFromContext(ctx) {
		return &Error{Reason: fmt.Sprintf("connection to blocked address %s refused", ip)}
	}
	return nil
}

// GuardedDialContext returns a DialContext for http.Transport that refuses to
// open a socket to a loopback / private / link-local / cloud-metadata address,
// honoring WithAllowPrivate(ctx). Pass nil for a sensible default dialer.
func GuardedDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if base != nil {
		d = *base
	}
	d.ControlContext = func(ctx context.Context, network, address string, _ syscall.RawConn) error {
		return checkDialAddr(ctx, address)
	}
	return d.DialContext
}
