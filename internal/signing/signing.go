// Package signing implements the Standard Webhooks signature scheme
// (https://www.standardwebhooks.com/): an HMAC-SHA256 over
// "<id>.<timestamp>.<body>" with a per-endpoint secret, plus a timestamp header
// so receivers can reject replays outside a tolerance window.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Headers are the three headers sent with every delivery.
type Headers struct {
	ID        string // Webhook-Id — the delivery id, stable across retries
	Timestamp string // Webhook-Timestamp — unix seconds
	Signature string // Webhook-Signature — "v1,<base64(hmac)>"
}

// Sign produces the headers for a delivery.
func Sign(secret, deliveryID string, ts time.Time, body []byte) Headers {
	tsStr := strconv.FormatInt(ts.Unix(), 10)
	return Headers{
		ID:        deliveryID,
		Timestamp: tsStr,
		Signature: "v1," + computeMAC(secret, deliveryID, tsStr, body),
	}
}

// Verify checks a received signature. It accepts multiple space-separated
// signatures (secret rotation) and enforces the timestamp tolerance.
func Verify(secret, deliveryID, tsHeader, sigHeader string, body []byte, tolerance time.Duration) error {
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("bad timestamp header: %w", err)
	}
	age := time.Since(time.Unix(ts, 0))
	if age > tolerance || age < -tolerance {
		return fmt.Errorf("timestamp outside tolerance (%s)", age)
	}
	want := computeMAC(secret, deliveryID, tsHeader, body)
	for _, sig := range strings.Fields(sigHeader) {
		got := strings.TrimPrefix(sig, "v1,")
		if hmac.Equal([]byte(got), []byte(want)) {
			return nil
		}
	}
	return fmt.Errorf("no matching signature")
}

func computeMAC(secret, id, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	mac.Write([]byte("."))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
