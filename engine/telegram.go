package engine

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// Telegram's data centres are checked with the first MTProto key-exchange message, not merely a
// TCP connect: a filtered route can accept the connection while dropping the protocol itself.
var telegramTargets = []string{
	"149.154.167.51:443", // DC2
	"149.154.167.91:443", // DC4
}

const (
	telegramReqPQMulti = 0xbe7e8ef1
	telegramResPQ      = 0x05162463
	telegramTimeout    = 2500 * time.Millisecond
)

// TelegramAnswers asks two data centres at once; one broken data centre must not reject a route.
func TelegramAnswers(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, telegramTimeout)
	defer cancel()
	answers := make(chan bool, len(telegramTargets))
	for _, target := range telegramTargets {
		go func() {
			answers <- telegramCheck(ctx, target)
		}()
	}
	for range telegramTargets {
		if <-answers {
			return true
		}
	}
	return false
}

func telegramCheck(ctx context.Context, target string) bool {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", target)
	if err != nil {
		return false
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return false
	}
	if _, err := conn.Write(reqPQPacket(time.Now(), nonce)); err != nil {
		return false
	}
	payload, err := readAbridged(conn)
	return err == nil && isResPQ(payload)
}

// reqPQPacket is abridged transport followed by an unencrypted req_pq_multi. The message ID encodes
// the current Unix time and must be divisible by four, as Telegram's protocol requires.
func reqPQPacket(now time.Time, nonce []byte) []byte {
	body := make([]byte, 40)
	binary.LittleEndian.PutUint64(body[0:8], 0) // no authorization key yet
	seconds := uint64(now.Unix())
	fraction := uint64(now.Nanosecond()/int(time.Millisecond)) * 4_000_000
	binary.LittleEndian.PutUint64(body[8:16], seconds<<32|fraction&0xfffffffc)
	binary.LittleEndian.PutUint32(body[16:20], 20)
	binary.LittleEndian.PutUint32(body[20:24], telegramReqPQMulti)
	copy(body[24:40], nonce)
	return append([]byte{0xef, byte(len(body) / 4)}, body...)
}

func isResPQ(payload []byte) bool {
	return len(payload) >= 24 && binary.LittleEndian.Uint32(payload[20:24]) == telegramResPQ
}

func readAbridged(r io.Reader) ([]byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return nil, err
	}
	words := int(first[0])
	if first[0] == 0x7f {
		var extended [3]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return nil, err
		}
		words = int(extended[0]) | int(extended[1])<<8 | int(extended[2])<<16
	}
	if words <= 0 || words > 1024*1024 {
		return nil, fmt.Errorf("telegram: invalid packet length %d", words)
	}
	payload := make([]byte, words*4)
	_, err := io.ReadFull(r, payload)
	return payload, err
}
