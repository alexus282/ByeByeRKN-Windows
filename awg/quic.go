package awg

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// QUIC Initial disguise: a genuine QUIC v1 Initial packet (RFC 9000 / RFC 9001) carrying a TLS
// ClientHello for an SNI. AmneziaWG sends it as I1 before the WireGuard handshake, so a DPI box
// that decrypts Initial packets sees an ordinary HTTP/3 connection to that site.

var initialSaltV1, _ = hex.DecodeString("38762cf7f55934b34d179ae6a4c80cadccbb7f0a")

const (
	minDatagram = 1200
	pnLength    = 4
	tagLength   = 16
)

type InitialKeys struct{ Key, IV, HP []byte }

func ClientInitialKeys(dcid []byte) InitialKeys {
	initialSecret := hmacSHA256(initialSaltV1, dcid)
	clientSecret := expandLabel(initialSecret, "client in", 32)
	return InitialKeys{
		Key: expandLabel(clientSecret, "quic key", 16),
		IV:  expandLabel(clientSecret, "quic iv", 12),
		HP:  expandLabel(clientSecret, "quic hp", 16),
	}
}

func QuicInitial(sni string) []byte {
	dcid := randomBytes(8)
	keys := ClientInitialKeys(dcid)
	crypto := cryptoFrame(clientHello(sni))

	var h bytes.Buffer
	h.WriteByte(0xC0 | (pnLength - 1)) // long header, Initial, 4-byte packet number
	h.Write([]byte{0, 0, 0, 1})        // QUIC v1
	h.WriteByte(byte(len(dcid)))
	h.Write(dcid)
	h.WriteByte(0) // empty source connection id
	h.WriteByte(0) // no token
	headerWithoutLength := h.Bytes()

	payloadLength := minDatagram - len(headerWithoutLength) - 2 - pnLength - tagLength
	if payloadLength < len(crypto) {
		panic("ClientHello does not fit into one datagram")
	}
	payload := make([]byte, payloadLength) // the rest is PADDING frames (zeros)
	copy(payload, crypto)
	header := append(append([]byte{}, headerWithoutLength...), varint(uint64(pnLength+len(payload)+tagLength), 2)...)
	header = append(header, 0, 0, 0, 0) // packet number 0

	nonce := append([]byte{}, keys.IV...)
	for i := 0; i < pnLength; i++ {
		nonce[len(nonce)-pnLength+i] ^= header[len(header)-pnLength+i]
	}
	block, _ := aes.NewCipher(keys.Key)
	aead, _ := cipher.NewGCM(block)
	ciphertext := aead.Seal(nil, nonce, payload, header)

	// Header protection: the sample starts 4 bytes after the packet number field.
	sample := ciphertext[4-pnLength : 4-pnLength+16]
	mask := HeaderProtectionMask(keys.HP, sample)
	header[0] ^= mask[0] & 0x0f
	pnOffset := len(header) - pnLength
	for i := 0; i < pnLength; i++ {
		header[pnOffset+i] ^= mask[1+i]
	}
	return append(header, ciphertext...)
}

func HeaderProtectionMask(hp, sample []byte) []byte {
	block, _ := aes.NewCipher(hp)
	out := make([]byte, 16)
	block.Encrypt(out, sample)
	return out
}

func expandLabel(secret []byte, label string, length int) []byte {
	full := []byte("tls13 " + label)
	info := make([]byte, 0, 4+len(full))
	info = binary.BigEndian.AppendUint16(info, uint16(length))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, 0)                              // empty context
	return hmacSHA256(secret, append(info, 1))[:length] // one HKDF block is enough
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func cryptoFrame(data []byte) []byte {
	out := []byte{0x06}
	out = append(out, varint(0, 0)...)
	out = append(out, varint(uint64(len(data)), 0)...)
	return append(out, data...)
}

func u16(v int) []byte { return []byte{byte(v >> 8), byte(v)} }

func clientHello(sni string) []byte {
	var ext bytes.Buffer
	add := func(typ int, body []byte) {
		ext.Write(u16(typ))
		ext.Write(u16(len(body)))
		ext.Write(body)
	}
	name := []byte(sni)
	add(0x0000, concat(u16(len(name)+3), []byte{0}, u16(len(name)), name))           // server_name
	add(0x000a, concat(u16(6), u16(0x001d), u16(0x0017), u16(0x0018)))               // supported_groups
	sigAlgs := []int{0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601} // signature_algorithms
	var algs []byte
	for _, a := range sigAlgs {
		algs = append(algs, u16(a)...)
	}
	add(0x000d, concat(u16(len(algs)), algs))
	add(0x0010, concat(u16(3), []byte{2}, []byte("h3")))                // ALPN
	add(0x002b, concat([]byte{2}, u16(0x0304)))                         // supported_versions: TLS 1.3
	add(0x002d, []byte{1, 1})                                           // psk_key_exchange_modes
	add(0x0033, concat(u16(36), u16(0x001d), u16(32), randomBytes(32))) // key_share x25519
	add(0x0039, transportParameters())

	var body bytes.Buffer
	body.Write(u16(0x0303))
	body.Write(randomBytes(32))
	body.WriteByte(0) // empty legacy_session_id, as QUIC requires
	body.Write(concat(u16(6), u16(0x1301), u16(0x1302), u16(0x1303)))
	body.Write([]byte{1, 0}) // null compression
	body.Write(u16(ext.Len()))
	body.Write(ext.Bytes())
	n := body.Len()
	return concat([]byte{1, byte(n >> 16), byte(n >> 8), byte(n)}, body.Bytes())
}

func transportParameters() []byte {
	param := func(id, value uint64) []byte {
		v := varint(value, 0)
		return concat(varint(id, 0), varint(uint64(len(v)), 0), v)
	}
	return concat(
		param(0x01, 30_000),     // max_idle_timeout
		param(0x03, 1472),       // max_udp_payload_size
		param(0x04, 15_728_640), // initial_max_data
		param(0x05, 6_291_456), param(0x06, 6_291_456), param(0x07, 6_291_456),
		param(0x08, 100), param(0x09, 103),
		varint(0x0f, 0), varint(0, 0), // initial_source_connection_id (empty)
	)
}

func varint(v uint64, forceLength int) []byte {
	length := forceLength
	if length == 0 {
		switch {
		case v < 0x40:
			length = 1
		case v < 0x4000:
			length = 2
		case v < 0x40000000:
			length = 4
		default:
			length = 8
		}
	}
	prefix := map[int]byte{1: 0x00, 2: 0x40, 4: 0x80, 8: 0xC0}[length]
	out := make([]byte, length)
	for i := range out {
		out[i] = byte(v >> (8 * (length - 1 - i)))
	}
	out[0] |= prefix
	return out
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
