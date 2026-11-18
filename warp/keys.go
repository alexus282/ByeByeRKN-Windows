package warp

import (
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/curve25519"
)

// KeyPair is a Curve25519 key pair in the base64 the WARP API expects.
type KeyPair struct {
	Private string
	Public  string
}

// GenerateKeyPair creates the device's WireGuard key pair. The private half never leaves the
// device.
func GenerateKeyPair() (KeyPair, error) {
	private := make([]byte, 32)
	if _, err := rand.Read(private); err != nil {
		return KeyPair{}, err
	}
	// Clamp as X25519 requires; the API only ever sees the public half.
	private[0] &= 248
	private[31] = (private[31] & 127) | 64
	public, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{
		Private: base64.StdEncoding.EncodeToString(private),
		Public:  base64.StdEncoding.EncodeToString(public),
	}, nil
}
