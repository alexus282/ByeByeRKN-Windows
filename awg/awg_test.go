package awg

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"strings"
	"testing"
)

// RFC 9001, appendix A.1.
func TestInitialKeysMatchRFC9001(t *testing.T) {
	dcid, _ := hex.DecodeString("8394c8f03e515708")
	keys := ClientInitialKeys(dcid)
	for name, got := range map[string][]byte{
		"1f369613dd76d5467730efcbe3b1a22d": keys.Key,
		"fa044b2f42a3fd3b46fb255c":         keys.IV,
		"9f50449e04a0e810283a1e9933adedd2": keys.HP,
	} {
		if hex.EncodeToString(got) != name {
			t.Errorf("got %x, want %s", got, name)
		}
	}
}

// Decrypts the packet the way a DPI box (or a server) would and finds the SNI in the ClientHello.
func TestQuicInitialDecryptsToClientHello(t *testing.T) {
	packet := QuicInitial("ya.ru")
	if len(packet) != minDatagram {
		t.Fatalf("datagram is %d bytes, want %d", len(packet), minDatagram)
	}
	dcidLen := int(packet[5])
	dcid := packet[6 : 6+dcidLen]
	keys := ClientInitialKeys(dcid)
	pnOffset := 6 + dcidLen + 1 + 1 + 2
	sample := packet[pnOffset+4 : pnOffset+20]
	mask := HeaderProtectionMask(keys.HP, sample)
	header := bytes.Clone(packet[:pnOffset+4])
	header[0] ^= mask[0] & 0x0f
	for i := 0; i < 4; i++ {
		header[pnOffset+i] ^= mask[1+i]
	}
	if header[0] != 0xC3 || binary.BigEndian.Uint32(header[pnOffset:]) != 0 {
		t.Fatalf("header protection not removed: %x", header[:1])
	}
	nonce := bytes.Clone(keys.IV)
	block, _ := aes.NewCipher(keys.Key)
	aead, _ := cipher.NewGCM(block)
	plain, err := aead.Open(nil, nonce, packet[pnOffset+4:], header)
	if err != nil {
		t.Fatalf("does not decrypt: %v", err)
	}
	// CRYPTO frame: type, offset varint, length varint, then the handshake message.
	// A QUIC varint's two high bits of the first byte say how many bytes it spans.
	size := func(at int) int { return 1 << (plain[at] >> 6) }
	i := 1 + size(1) // skip the offset
	i += size(i)     // skip the length
	if plain[0] != 0x06 || plain[i] != 0x01 {
		t.Fatalf("no CRYPTO frame with a ClientHello: %x", plain[:6])
	}
	if !bytes.Contains(plain, []byte("ya.ru")) || !bytes.Contains(plain, []byte("h3")) {
		t.Fatal("SNI or ALPN missing")
	}
}

func TestStunFingerprintIsValid(t *testing.T) {
	p := STUNBindingRequest()
	if binary.BigEndian.Uint16(p) != 1 || int(binary.BigEndian.Uint16(p[2:]))+20 != len(p) {
		t.Fatal("bad STUN header")
	}
	crc := crc32.ChecksumIEEE(p[:len(p)-8]) ^ 0x5354554E
	if binary.BigEndian.Uint16(p[len(p)-8:]) != 0x8028 || binary.BigEndian.Uint32(p[len(p)-4:]) != crc {
		t.Fatal("bad FINGERPRINT")
	}
}

func TestDNSAndSIPLookReal(t *testing.T) {
	q := DNSQuery("ya.ru")
	if !bytes.Contains(q, []byte{2, 'y', 'a', 2, 'r', 'u', 0}) {
		t.Fatalf("no question name: %x", q)
	}
	s := SIPOptions()
	if !strings.HasPrefix(s, "OPTIONS sip:") || !strings.HasSuffix(s, "\r\n\r\n") {
		t.Fatal("bad SIP OPTIONS")
	}
}

func TestParamsRoundTripAndRenderEveryKey(t *testing.T) {
	for _, m := range AutoOrder {
		p := NewParams(m, "ya.ru")
		data, _ := json.Marshal(p)
		var back Params
		if err := json.Unmarshal(data, &back); err != nil || back.Mask != m || !bytes.Equal(back.I1, p.I1) || back.Jmax != p.Jmax {
			t.Fatalf("%v does not survive JSON: %v", m, err)
		}
		uapi := p.UAPI()
		// Every key is set even for NONE, so a running device drops the previous preset.
		for _, key := range []string{"jc=", "jmin=", "jmax=", "s1=0", "h4=4"} {
			if !strings.Contains(uapi, key) {
				t.Fatalf("%v: %q missing in %q", m, key, uapi)
			}
		}
		if (p.I1 != nil) != strings.Contains(uapi, "i1=<b 0x") {
			t.Fatalf("%v: I1 rendering", m)
		}
		if p.Jmax > maxJunk || (p.Jc > 0 && p.Jmax <= p.Jmin) {
			t.Fatalf("%v: bad junk sizes %d..%d", m, p.Jmin, p.Jmax)
		}
	}
}
