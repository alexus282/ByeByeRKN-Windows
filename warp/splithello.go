package warp

import (
	"context"
	"encoding/binary"
	"net"
	"time"
)

// SplitDialer opens a TCP connection whose first write, if it is a TLS ClientHello, is cut into
// two TLS records sent as separate TCP segments, with the cut inside the server name. DPI that
// reads the SNI from the first segment or record sees only a fragment; the server reassembles
// it normally. The same idea as ByeDPI's `--tlsrec` + `--split`.
func SplitDialer(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{}
	connection, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &splitConn{Conn: connection}, nil
}

type splitConn struct {
	net.Conn
	wrote bool
}

func (c *splitConn) Write(data []byte) (int, error) {
	if c.wrote {
		return c.Conn.Write(data)
	}
	c.wrote = true
	first, second := SplitClientHello(data)
	if first == nil {
		return c.Conn.Write(data)
	}
	if tcp, ok := c.Conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
	}
	if _, err := c.Conn.Write(first); err != nil {
		return 0, err
	}
	// The pause keeps the two records in separate TCP segments.
	time.Sleep(20 * time.Millisecond)
	if _, err := c.Conn.Write(second); err != nil {
		return 0, err
	}
	return len(data), nil
}

// SplitClientHello returns the two TLS records to send instead of data's first record, or nil
// when data does not start with a complete TLS handshake record.
func SplitClientHello(data []byte) ([]byte, []byte) {
	if len(data) < 6 || data[0] != 0x16 || data[1] != 0x03 {
		return nil, nil
	}
	recordLength := int(binary.BigEndian.Uint16(data[3:5]))
	if len(data) < 5+recordLength || recordLength < 2 {
		return nil, nil
	}
	payload := data[5 : 5+recordLength]
	rest := data[5+recordLength:]
	cut := recordLength / 2
	if offset := sniOffset(payload); offset >= 0 {
		cut = offset + 2
	}
	if cut < 1 {
		cut = 1
	}
	if cut > recordLength-1 {
		cut = recordLength - 1
	}

	record := func(body []byte) []byte {
		out := make([]byte, 5+len(body))
		out[0], out[1], out[2] = 0x16, data[1], data[2]
		binary.BigEndian.PutUint16(out[3:5], uint16(len(body)))
		copy(out[5:], body)
		return out
	}
	first := record(payload[:cut])
	second := append(record(payload[cut:]), rest...)
	return first, second
}

// sniOffset is the offset of the host name inside a ClientHello handshake message, or -1 when
// it has none.
func sniOffset(hello []byte) int {
	u8 := func(i int) int { return int(hello[i]) }
	u16 := func(i int) int { return u8(i)<<8 | u8(i+1) }
	at := func(i int) bool { return i >= 0 && i < len(hello) }

	if !at(0) || u8(0) != 0x01 {
		return -1
	}
	p := 4 + 2 + 32 // handshake header, legacy_version, random
	if !at(p) {
		return -1
	}
	p += 1 + u8(p) // session id
	if !at(p) {
		return -1
	}
	p += 2 + u16(p) // cipher suites
	if !at(p) {
		return -1
	}
	p += 1 + u8(p) // compression methods
	if !at(p + 1) {
		return -1
	}
	end := p + 2 + u16(p)
	p += 2
	for p+4 <= end && at(p+3) {
		typ := u16(p)
		length := u16(p + 2)
		// server_name: list length (2), name type (1), name length (2), name
		if typ == 0x0000 && length >= 5 && at(p+4+5) {
			return p + 4 + 5
		}
		p += 4 + length
	}
	return -1
}
