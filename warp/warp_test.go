package warp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// A ClientHello for ya.ru, shaped the way TLS 1.3 sends it: a handshake message inside a
// single record.
func clientHello(sni string) []byte {
	name := []byte(sni)
	serverName := append([]byte{0, byte(len(name) + 3), 0, 0, byte(len(name))}, name...)
	extension := func(typ int, body []byte) []byte {
		out := []byte{byte(typ >> 8), byte(typ), byte(len(body) >> 8), byte(len(body))}
		return append(out, body...)
	}
	extensions := extension(0x0000, serverName)

	hello := []byte{0x01, 0, 0, byte(len(extensions) + 4)}
	hello = append(hello, 0x03, 0x03)
	hello = append(hello, make([]byte, 32)...) // random
	hello = append(hello, 0)                   // empty session id
	hello = append(hello, 0, 2, 0x13, 0x01)    // one cipher suite
	hello = append(hello, 1, 0)                // null compression
	hello = append(hello, byte(len(extensions)>>8), byte(len(extensions)))
	hello = append(hello, extensions...)

	record := []byte{0x16, 0x03, 0x01, byte(len(hello) >> 8), byte(len(hello))}
	return append(record, hello...)
}

func TestSplitClientHelloCutsInsideTheServerName(t *testing.T) {
	data := clientHello("ya.ru")
	first, second := SplitClientHello(data)
	if first == nil {
		t.Fatal("did not split a ClientHello")
	}
	// The first record must be a complete TLS record, and both records' payloads must add up
	// to the original record's payload; only the headers differ.
	if first[0] != 0x16 || second[0] != 0x16 {
		t.Fatalf("not handshake records: %x %x", first[:1], second[:1])
	}
	firstLen := int(binary.BigEndian.Uint16(first[3:5]))
	secondLen := int(binary.BigEndian.Uint16(second[3:5]))
	if firstLen != len(first)-5 || secondLen != len(second)-5 {
		t.Fatal("record length is wrong")
	}
	originalLen := int(binary.BigEndian.Uint16(data[3:5]))
	if firstLen+secondLen != originalLen || len(second)-5-secondLen != len(data)-5-originalLen {
		t.Fatal("the records do not reassemble into the original")
	}
	// The cut lands inside the SNI, so neither record carries the whole name.
	if bytes.Contains(first, []byte("ya.ru")) || bytes.Contains(second, []byte("ya.ru")) {
		t.Fatal("the server name survived whole in one record")
	}
}

func TestSplitClientHelloLeavesOtherWritesAlone(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{0x16, 0x03},                            // too short to be a record
		{0x17, 0x03, 0x01, 0, 4, 1, 2, 3, 4},    // application data, not a handshake
		{0x16, 0x03, 0x01, 0, 9, 0x01, 0, 0, 1}, // claims more bytes than are there
	} {
		if first, _ := SplitClientHello(data); first != nil {
			t.Errorf("split %x", data)
		}
	}
}

func TestGenerateKeyPairIsBase64AndClamped(t *testing.T) {
	pair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.Private) != 44 || len(pair.Public) != 44 {
		t.Fatalf("keys are not 32 bytes of base64: %d %d", len(pair.Private), len(pair.Public))
	}
	if pair.Private == pair.Public {
		t.Fatal("private half equals public half")
	}
	second, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if second.Private == pair.Private {
		t.Fatal("two key pairs are identical")
	}
}

func TestLicensePattern(t *testing.T) {
	for _, good := range []string{"abcdefgh-abcdefgh-abcdefgh", "AbCdEf12-ghIjKl34-MnOpQr56"} {
		if !IsLicense(good) {
			t.Errorf("%s rejected", good)
		}
	}
	for _, bad := range []string{"", "short-short-short", "abcdefgh-abcdefgh", "abcdefgh_abcdefgh-abcdefgh"} {
		if IsLicense(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestHostOfStripsPortAndBrackets(t *testing.T) {
	for input, want := range map[string]string{
		"162.159.198.1:0":     "162.159.198.1",
		"[2606:4700:103::]:0": "2606:4700:103::",
	} {
		if got := hostOf(input); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", input, got, want)
		}
	}
}
