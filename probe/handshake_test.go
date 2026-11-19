package probe

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
	"golang.org/x/crypto/chacha20poly1305"
)

// The responder's side of the handshake: it knows only its own static keypair, exactly like a
// WARP server, and must recover the initiator's key and a fresh timestamp from the message.
func TestInitiationOpensOnTheServerSide(t *testing.T) {
	initiatorPrivate, initiatorPublic := keyPair()
	peerPrivate, peerPublic := keyPair()

	senderIndex := randUint32()
	msg, err := Initiation(initiatorPrivate, initiatorPublic, peerPublic, senderIndex)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg) != InitiationSize {
		t.Fatalf("message is %d bytes, want %d", len(msg), InitiationSize)
	}
	if msg[0] != 1 || senderIndex != binary.LittleEndian.Uint32(msg[4:8]) {
		t.Fatalf("bad header: %x", msg[:8])
	}
	// mac2 stays zero until the server hands out a cookie.
	if !bytes.Equal(msg[132:], make([]byte, 16)) {
		t.Fatal("mac2 must be zero")
	}

	// The server checks mac1 before touching any state.
	mac1Key := hash256(append(append([]byte{}, labelMAC1...), peerPublic...))
	if !bytes.Equal(msg[116:132], keyedHash128(mac1Key, msg[:116])) {
		t.Fatal("mac1 does not verify")
	}

	ephemeralPublic := msg[8:40]
	encryptedStatic := msg[40:88]
	encryptedTimestamp := msg[88:116]

	ck := hash256(construction)
	h := hash256(append(ck, identifier...))
	h = hash256(append(h, peerPublic...)) // the server's own key
	h = hash256(append(h, ephemeralPublic...))
	ck = kdf(ck, ephemeralPublic, 1)[0]

	ck1, staticKey := kdf2(ck, mustDH(peerPrivate, ephemeralPublic))
	staticPublic, err := open(staticKey, 0, encryptedStatic, h)
	if err != nil {
		t.Fatalf("static key does not decrypt: %v", err)
	}
	if !bytes.Equal(staticPublic, initiatorPublic) {
		t.Fatal("wrong initiator key recovered")
	}
	h = hash256(append(h, encryptedStatic...))

	_, timestampKey := kdf2(ck1, mustDH(peerPrivate, staticPublic))
	timestamp, err := open(timestampKey, 0, encryptedTimestamp, h)
	if err != nil {
		t.Fatalf("timestamp does not decrypt: %v", err)
	}
	if len(timestamp) != 12 || timestamp[0] != 0x40 {
		t.Fatalf("bad TAI64N timestamp: %x", timestamp)
	}
}

func TestIsReplyToMatchesTheSenderIndex(t *testing.T) {
	const index = uint32(0x12345678)

	response := make([]byte, ResponseSize)
	response[0] = 2
	binary.LittleEndian.PutUint32(response[8:12], index)
	if !IsReplyTo(response, index) {
		t.Fatal("response for our index rejected")
	}
	binary.LittleEndian.PutUint32(response[8:12], index+1)
	if IsReplyTo(response, index) {
		t.Fatal("response for another index accepted")
	}

	cookie := make([]byte, CookieReplySize)
	cookie[0] = 3
	binary.LittleEndian.PutUint32(cookie[4:8], index)
	if !IsReplyTo(cookie, index) {
		t.Fatal("cookie reply for our index rejected")
	}

	if IsReplyTo(response[:ResponseSize-1], index) || IsReplyTo(append(response, 0), index) {
		t.Fatal("trailing bytes accepted")
	}
}

func TestTimestampsNeverRepeat(t *testing.T) {
	first := tai64n()
	second := tai64n()
	if bytes.Compare(first, second) >= 0 {
		t.Fatalf("timestamps are not increasing: %x >= %x", first, second)
	}
}

func TestPublicKeysAreClamped(t *testing.T) {
	private := NewPrivateKey()
	if private[0]&7 != 0 || private[31]&0x80 != 0 || private[31]&0x40 == 0 {
		t.Fatalf("private key is not clamped: %x", private)
	}
	if bytes.Equal(PublicKey(private), make([]byte, 32)) {
		t.Fatal("public key is all zero")
	}
}

// A probe against a socket that answers with a crafted response must report the round trip,
// and a socket that stays silent must time out. This checks the send/receive loop without
// needing the network.
func TestProbeReadsTheReplyAndGivesUp(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback: " + err.Error())
	}
	defer server.Close()

	go func() {
		buffer := make([]byte, 2048)
		for {
			n, addr, err := server.ReadFrom(buffer)
			if err != nil {
				return
			}
			// The probe sends I1 and junk first; only the 148-byte initiation matters.
			if n != InitiationSize || buffer[0] != 1 {
				continue
			}
			// The initiation's sender index becomes the receiver index of the response.
			// The pause gives the round trip a length the test can assert on: this machine's
			// monotonic clock sometimes stands still for sub-millisecond intervals.
			reply := make([]byte, ResponseSize)
			reply[0] = 2
			binary.LittleEndian.PutUint32(reply[8:12], binary.LittleEndian.Uint32(buffer[4:8]))
			time.Sleep(30 * time.Millisecond)
			server.WriteTo(reply, addr)
			return
		}
	}()

	private, _ := keyPair()
	_, peerPublic := keyPair()
	probe := New(private, peerPublic, nil)
	address, err := Resolve(server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}

	params := awg.NewParams(awg.QUIC, "ya.ru")
	rtt, err := probe.Probe(address, params, 2*time.Second)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if rtt < 20*time.Millisecond || rtt > 2*time.Second {
		t.Fatalf("bogus round trip %v (%d ns)", rtt, rtt.Nanoseconds())
	}

	// Now a server that never answers: the probe must give up at its deadline.
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback: " + err.Error())
	}
	defer silent.Close()
	silentAddress, err := Resolve(silent.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := probe.Probe(silentAddress, awg.Params{}, 200*time.Millisecond); err == nil {
		t.Fatal("silent server reported success")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("gave up after %v, before the deadline", elapsed)
	}
}

func keyPair() (private, public []byte) {
	private = NewPrivateKey()
	return private, PublicKey(private)
}

func open(key []byte, counter uint64, ciphertext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce[4:], counter)
	return aead.Open(nil, nonce, ciphertext, aad)
}
