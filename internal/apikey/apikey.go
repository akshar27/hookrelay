// Package apikey generates and verifies HookRelay ingest API keys.
//
// Wire format: hr_<prefix><random>. The first 12 chars after "hr_" are the
// non-secret prefix used to look the row up; the whole raw key is SHA-256'd and
// compared in constant time. High-entropy keys don't need a slow KDF.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

const (
	scheme    = "hr_"
	prefixLen = 12
	randomLen = 24 // bytes
)

// Generated is a freshly minted key: the raw string (shown once), its lookup
// prefix, and the hash to store.
type Generated struct {
	Raw    string
	Prefix string
	Hash   string
}

// Generate mints a new key.
func Generate() Generated {
	buf := make([]byte, prefixLen+randomLen)
	_, _ = rand.Read(buf)
	body := base64.RawURLEncoding.EncodeToString(buf)
	raw := scheme + body
	return Generated{
		Raw:    raw,
		Prefix: body[:prefixLen],
		Hash:   Hash(raw),
	}
}

// Hash returns the hex SHA-256 of a raw key.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Prefix extracts the lookup prefix from a presented raw key, or "" if malformed.
func Prefix(raw string) string {
	if !strings.HasPrefix(raw, scheme) {
		return ""
	}
	body := raw[len(scheme):]
	if len(body) < prefixLen+8 {
		return ""
	}
	return body[:prefixLen]
}

// Verify reports whether a presented raw key matches a stored hash.
func Verify(raw, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(raw)), []byte(storedHash)) == 1
}
