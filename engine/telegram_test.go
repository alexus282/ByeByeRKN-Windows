package engine

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestReqPQPacketMatchesMTProtoAbridgedTransport(t *testing.T) {
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	packet := reqPQPacket(time.Unix(1_700_000_000, 123_456_000), nonce)
	if len(packet) != 42 || packet[0] != 0xef || packet[1] != 10 {
		t.Fatalf("framing: %x", packet)
	}
	body := packet[2:]
	if binary.LittleEndian.Uint64(body[0:8]) != 0 || binary.LittleEndian.Uint32(body[16:20]) != 20 {
		t.Fatalf("header: %x", body)
	}
	if id := binary.LittleEndian.Uint64(body[8:16]); id%4 != 0 || id>>32 != 1_700_000_000 {
		t.Fatalf("message id: %x", id)
	}
	if binary.LittleEndian.Uint32(body[20:24]) != telegramReqPQMulti || !bytes.Equal(body[24:40], nonce) {
		t.Fatalf("request: %x", body)
	}
}

func TestReadAbridgedAndResPQ(t *testing.T) {
	payload := make([]byte, 88)
	binary.LittleEndian.PutUint32(payload[20:24], telegramResPQ)
	short := append([]byte{byte(len(payload) / 4)}, payload...)
	got, err := readAbridged(bytes.NewReader(short))
	if err != nil || !bytes.Equal(got, payload) || !isResPQ(got) {
		t.Fatalf("short packet: %v", err)
	}
	if isResPQ(make([]byte, 23)) {
		t.Fatal("a truncated packet is not res_pq")
	}

	longBody := make([]byte, 128)
	long := append([]byte{0x7f, byte(len(longBody) / 4), 0, 0}, longBody...)
	got, err = readAbridged(bytes.NewReader(long))
	if err != nil || len(got) != len(longBody) {
		t.Fatalf("extended packet: %d, %v", len(got), err)
	}
}

func TestTelegramExchangeWaitsForAProtocolAnswer(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		packet := make([]byte, 42)
		server.Read(packet)
		payload := make([]byte, 88)
		binary.LittleEndian.PutUint32(payload[20:24], telegramResPQ)
		server.Write(append([]byte{byte(len(payload) / 4)}, payload...))
	}()
	client.SetDeadline(time.Now().Add(time.Second))
	nonce := make([]byte, 16)
	if _, err := client.Write(reqPQPacket(time.Now(), nonce)); err != nil {
		t.Fatal(err)
	}
	payload, err := readAbridged(client)
	if err != nil || !isResPQ(payload) {
		t.Fatalf("answer: %v", err)
	}
}
