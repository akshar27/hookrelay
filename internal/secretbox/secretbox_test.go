package secretbox

import (
	"bytes"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("whsec_super-secret-signing-key")

	sealed, err := box.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("sealed value contains the plaintext")
	}

	got, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, _ := New(GenerateKey())
	sealed, _ := box.Seal([]byte("hello"))
	sealed[len(sealed)-1] ^= 0xff
	if _, err := box.Open(sealed); err == nil {
		t.Fatal("expected auth failure on tampered ciphertext")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New("not-base64!!!"); err == nil {
		t.Fatal("want error for non-base64 key")
	}
	if _, err := New("c2hvcnQ="); err == nil { // "short"
		t.Fatal("want error for wrong-length key")
	}
}
