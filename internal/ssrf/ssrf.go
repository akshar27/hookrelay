// Package ssrf classifies destination addresses and validates endpoint URLs so
// HookRelay never delivers to loopback, private, link-local, or cloud-metadata
// addresses. Used at endpoint-create time (URL literal) and again at
// delivery time after DNS resolution (M5), which defeats DNS rebinding.
package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// Error describes why an address or URL was rejected.
type Error struct{ Reason string }

func (e *Error) Error() string { return e.Reason }

var cloudMetadata = netip.MustParseAddr("169.254.169.254")

// BlockedIP reports whether an IP must not be a webhook destination.
func BlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	switch {
	case ip == cloudMetadata:
		return true
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(), ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return true
	case ip.IsPrivate(): // RFC1918 + RFC4193 ULA
		return true
	default:
		return false
	}
}

// ValidateURL parses a webhook endpoint URL and rejects it if the scheme is
// wrong or the host is an IP literal in a blocked range. When allowInsecure is
// false, only https is accepted. Hostnames are resolved at delivery time, not
// here.
func ValidateURL(raw string, allowInsecure, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, &Error{Reason: fmt.Sprintf("invalid url: %v", err)}
	}
	if u.Scheme != "https" && !(allowInsecure && u.Scheme == "http") {
		return nil, &Error{Reason: "url scheme must be https"}
	}
	host := u.Hostname()
	if host == "" {
		return nil, &Error{Reason: "url has no host"}
	}
	if ip, perr := netip.ParseAddr(host); perr == nil {
		if BlockedIP(ip) && !allowPrivate {
			return nil, &Error{Reason: fmt.Sprintf("url resolves to a blocked address (%s)", ip)}
		}
	}
	return u, nil
}

// ResolveAndCheck resolves host and returns its IPs, erroring if ANY resolved IP
// is blocked (unless allowPrivate). Used on the delivery path.
func ResolveAndCheck(ctx context.Context, resolver *net.Resolver, host string, allowPrivate bool) ([]netip.Addr, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, &Error{Reason: fmt.Sprintf("dns lookup failed: %v", err)}
	}
	for _, a := range addrs {
		if BlockedIP(a) && !allowPrivate {
			return nil, &Error{Reason: fmt.Sprintf("host %s resolves to a blocked address (%s)", host, a)}
		}
	}
	return addrs, nil
}
