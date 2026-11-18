// Package probe proves that an endpoint answers a real WireGuard handshake through the local
// DPI, without bringing the tunnel up. It builds the same Noise_IKpsk2 initiation AmneziaWG
// sends (wireguard whitepaper, section 5.4.2), so the server treats it as a genuine peer.
package probe

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"hash"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

const (
	InitiationSize  = 148
	ResponseSize    = 92
	CookieReplySize = 64
)

var (
	construction = []byte("Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s")
	identifier   = []byte("WireGuard v1 zx2c4 Jason@zx2c4.com")
	labelMAC1    = []byte("mac1----")
)

// NewPrivateKey returns a clamped random X25519 private key, as WireGuard generates them.
func NewPrivateKey() []byte {
	key := randomBytes(32)
	clamp(key)
	return key
}

// PublicKey derives the public half of an X25519 private key.
func PublicKey(private []byte) []byte {
	out, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return make([]byte, 32)
	}
	return out
}

// Initiation builds a handshake initiation: senderIndex identifies the reply, and the keys are
// the device's static pair plus the peer's public key. It fails only if the peer's key is not a
// valid curve point, which a WARP peer never is.
func Initiation(staticPrivate, staticPublic, peerPublic []byte, senderIndex uint32) ([]byte, error) {
	ck := hash256(construction)
	h := hash256(append(ck, identifier...))
	h = hash256(append(h, peerPublic...))

	ephemeralPrivate := NewPrivateKey()
	ephemeralPublic := PublicKey(ephemeralPrivate)
	ck = kdf(ck, ephemeralPublic, 1)[0]
	h = hash256(append(h, ephemeralPublic...))

	ck1, staticKey := kdf2(ck, mustDH(ephemeralPrivate, peerPublic))
	encryptedStatic := seal(staticKey, 0, staticPublic, h)
	h = hash256(append(h, encryptedStatic...))

	_, timestampKey := kdf2(ck1, mustDH(staticPrivate, peerPublic))
	encryptedTimestamp := seal(timestampKey, 0, tai64n(), h)

	msg := make([]byte, 0, InitiationSize)
	msg = append(msg, 1, 0, 0, 0) // type 1 followed by three reserved zero bytes
	msg = binary.LittleEndian.AppendUint32(msg, senderIndex)
	msg = append(msg, ephemeralPublic...)
	msg = append(msg, encryptedStatic...)
	msg = append(msg, encryptedTimestamp...)

	// mac1 lets the server cheaply reject junk before touching any state; mac2 stays zero
	// until the server hands out a cookie.
	mac1Key := hash256(append(append([]byte{}, labelMAC1...), peerPublic...))
	msg = append(msg, keyedHash128(mac1Key, msg)...)
	return append(msg, make([]byte, 16)...), nil
}

// IsReplyTo reports whether packet is a handshake response or cookie reply for senderIndex.
// The response carries the responder's own sender index at offset 4 and ours at offset 8; a
// cookie reply carries ours at offset 4.
func IsReplyTo(packet []byte, senderIndex uint32) bool {
	switch {
	case len(packet) == ResponseSize && packet[0] == 2:
		return binary.LittleEndian.Uint32(packet[8:12]) == senderIndex
	case len(packet) == CookieReplySize && packet[0] == 3:
		return binary.LittleEndian.Uint32(packet[4:8]) == senderIndex
	default:
		return false
	}
}

// lastTimestamp keeps TAI64N stamps strictly increasing: the server rejects replays.
var lastTimestamp atomic.Uint64

// tai64n is a TAI64N timestamp: 64-bit big-endian seconds since 1970 with the RFC-ish 0x40
// label, plus nanoseconds.
func tai64n() []byte {
	now := uint64(time.Now().UnixNano())
	for {
		previous := lastTimestamp.Load()
		next := now
		if next <= previous {
			next = previous + 1
		}
		if lastTimestamp.CompareAndSwap(previous, next) {
			now = next
			break
		}
	}
	out := make([]byte, 12)
	binary.BigEndian.PutUint64(out, 0x400000000000000a+now/1e9)
	binary.BigEndian.PutUint32(out[8:], uint32(now%1e9))
	return out
}

func dh(private, public []byte) ([]byte, error) { return curve25519.X25519(private, public) }

// mustDH is for the pairs the protocol already proved valid (our own keys and the peer's).
func mustDH(private, public []byte) []byte {
	out, err := dh(private, public)
	if err != nil {
		out = make([]byte, 32)
	}
	return out
}

func seal(key []byte, counter uint64, plaintext, aad []byte) []byte {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		panic(err)
	}
	// WireGuard's nonce: four zero bytes followed by a little-endian 64-bit counter.
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce[4:], counter)
	return aead.Seal(nil, nonce, plaintext, aad)
}

func hash256(data []byte) []byte {
	sum := blake2s.Sum256(data)
	return sum[:]
}

// keyedHash128 is BLAKE2s with a key and a 16-byte digest, the MAC1 construction.
func keyedHash128(key, data []byte) []byte {
	h, err := blake2s.New128(key)
	if err != nil {
		panic(err)
	}
	h.Write(data)
	var out [16]byte
	return h.Sum(out[:0])
}

// kdf is WireGuard's KDF_n: HMAC-BLAKE2s over the chaining key, then n chained outputs.
func kdf(chainingKey, input []byte, n int) [][]byte {
	prk := hmac.New(blake2sHash, chainingKey)
	prk.Write(input)
	sum := prk.Sum(nil)

	outputs := make([][]byte, n)
	var previous []byte
	for i := range outputs {
		m := hmac.New(blake2sHash, sum)
		m.Write(previous)
		m.Write([]byte{byte(i + 1)})
		previous = m.Sum(nil)
		outputs[i] = previous
	}
	return outputs
}

// blake2sHash is what HMAC needs: a keyed-less 256-bit BLAKE2s.
func blake2sHash() hash.Hash {
	h, err := blake2s.New256(nil)
	if err != nil {
		panic(err)
	}
	return h
}

func kdf2(chainingKey, input []byte) ([]byte, []byte) {
	out := kdf(chainingKey, input, 2)
	return out[0], out[1]
}

func clamp(key []byte) {
	key[0] &= 248
	key[31] = (key[31] & 127) | 64
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
