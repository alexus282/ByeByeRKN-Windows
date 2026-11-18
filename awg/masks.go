package awg

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"
)

// First-packet disguises for AmneziaWG's I1. Each builds a well-formed packet of a common UDP
// protocol, so a DPI box classifying the flow by its first datagram sees something harmless.

var dnsNames = []string{
	"yandex.ru", "vk.com", "mail.ru", "ozon.ru", "wildberries.ru", "gosuslugi.ru",
	"avito.ru", "dzen.ru", "rutube.ru", "sberbank.ru",
}

// DNSQuery is a recursive DNS query (A or HTTPS record) with an EDNS0 OPT record, like a stub
// resolver sends; name is random when empty.
func DNSQuery(name string) []byte {
	if name == "" {
		name = dnsNames[randInt(len(dnsNames))]
	}
	var out bytes.Buffer
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:], uint16(randInt(0x10000))) // transaction id
	binary.BigEndian.PutUint16(header[2:], 0x0120)                   // standard query, recursion desired, AD
	binary.BigEndian.PutUint16(header[4:], 1)                        // 1 question
	binary.BigEndian.PutUint16(header[10:], 1)                       // 1 additional (OPT)
	out.Write(header)
	for _, label := range strings.Split(name, ".") {
		out.WriteByte(byte(len(label)))
		out.WriteString(label)
	}
	out.WriteByte(0)
	qtype := byte(1)
	if randInt(2) == 1 {
		qtype = 65
	}
	out.Write([]byte{0, qtype, 0, 1})                         // QTYPE A or HTTPS, QCLASS IN
	out.Write([]byte{0, 0, 41, 0x04, 0xD0, 0, 0, 0, 0, 0, 0}) // OPT, 1232-byte UDP payload
	return out.Bytes()
}

// STUNBindingRequest is an ICE connectivity check: a STUN Binding Request with USERNAME,
// ICE-CONTROLLING, PRIORITY, MESSAGE-INTEGRITY and FINGERPRINT, the shape WebRTC calls use
// (RFC 5389 / RFC 8445).
func STUNBindingRequest() []byte {
	transactionID := randomBytes(12)
	var attrs bytes.Buffer
	attribute := func(typ uint16, value []byte) {
		attrs.Write(binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint16(nil, typ), uint16(len(value))))
		attrs.Write(value)
		attrs.Write(make([]byte, (4-len(value)%4)%4))
	}
	header := func(length int) []byte {
		h := make([]byte, 20)
		binary.BigEndian.PutUint16(h[0:], 0x0001) // Binding Request
		binary.BigEndian.PutUint16(h[2:], uint16(length))
		binary.BigEndian.PutUint32(h[4:], 0x2112A442) // magic cookie
		copy(h[8:], transactionID)
		return h
	}
	attribute(0x0006, []byte(randomToken(4)+":"+randomToken(4)))                             // USERNAME
	attribute(0x802A, randomBytes(8))                                                        // ICE-CONTROLLING
	attribute(0x0024, binary.BigEndian.AppendUint32(nil, uint32(0x6E7F00FF-randInt(0x100)))) // PRIORITY

	// MESSAGE-INTEGRITY covers the message with the length already counting itself (24 bytes).
	before := bytes.Clone(attrs.Bytes())
	m := hmac.New(sha1.New, []byte(randomToken(22)))
	m.Write(header(len(before) + 24))
	m.Write(before)
	attribute(0x0008, m.Sum(nil))
	// FINGERPRINT is the CRC-32 of everything before it, length counting the 8-byte attribute.
	before = bytes.Clone(attrs.Bytes())
	crc := crc32.ChecksumIEEE(append(header(len(before)+8), before...))
	attribute(0x8028, binary.BigEndian.AppendUint32(nil, crc^0x5354554E))
	return append(header(attrs.Len()), attrs.Bytes()...)
}

// SIPOptions is a SIP OPTIONS keep-alive, as softphones send to their registrar.
func SIPOptions() string {
	user := fmt.Sprintf("user%d", randInt(9000)+1000)
	host := []string{"sip.zadarma.com", "sip.mtt.ru", "sip.beeline.ru", "voip.ms"}[randInt(4)]
	localIP := fmt.Sprintf("192.168.%d.%d", 1+randInt(254), 2+randInt(252))
	const port = 5060
	var b strings.Builder
	fmt.Fprintf(&b, "OPTIONS sip:%s SIP/2.0\r\n", host)
	fmt.Fprintf(&b, "Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%s\r\n", localIP, port, randomToken(10))
	b.WriteString("Max-Forwards: 70\r\n")
	fmt.Fprintf(&b, "From: <sip:%s@%s>;tag=%s\r\n", user, host, randomToken(8))
	fmt.Fprintf(&b, "To: <sip:%s@%s>\r\n", user, host)
	fmt.Fprintf(&b, "Call-ID: %s@%s\r\n", randomToken(16), localIP)
	fmt.Fprintf(&b, "CSeq: %d OPTIONS\r\n", 1+randInt(499))
	fmt.Fprintf(&b, "Contact: <sip:%s@%s:%d>\r\n", user, localIP, port)
	b.WriteString("User-Agent: MicroSIP/3.21.4\r\n")
	b.WriteString("Accept: application/sdp\r\n")
	b.WriteString("Content-Length: 0\r\n\r\n")
	return b.String()
}

func randomToken(length int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = alphabet[randInt(len(alphabet))]
	}
	return string(b)
}
