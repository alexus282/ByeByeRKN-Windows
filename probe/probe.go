package probe

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net"
	"time"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
)

// Probe sends exactly what AmneziaWG sends when connecting (I1, junk packets, handshake
// initiation) and waits for the server's handshake response. One probe uses a fresh UDP socket,
// so nothing keeps state between attempts.
type Probe struct {
	privateKey []byte
	publicKey  []byte
	peerPublic []byte
	// bind turns a socket into one the tunnel cannot capture; nil when the VPN is down.
	bind func(*net.UDPConn) error
}

// New builds a probe from the account's private key and the peer's public key, both raw bytes.
func New(privateKey, peerPublic []byte, bind func(*net.UDPConn) error) *Probe {
	return &Probe{
		privateKey: privateKey,
		publicKey:  PublicKey(privateKey),
		peerPublic: peerPublic,
		bind:       bind,
	}
}

// NewFromBase64 is [New] for keys as the WARP API hands them out (base64, 32 bytes).
func NewFromBase64(privateKey, peerPublic string, bind func(*net.UDPConn) error) (*Probe, error) {
	private, err := decodeKey(privateKey)
	if err != nil {
		return nil, err
	}
	peer, err := decodeKey(peerPublic)
	if err != nil {
		return nil, err
	}
	return New(private, peer, bind), nil
}

func decodeKey(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	return raw, nil
}

// Probe round-trips one handshake and returns how long it took, or an error when nothing
// answered in time. The address must already be resolved: a scan must not wait for DNS.
func (p *Probe) Probe(address *net.UDPAddr, params awg.Params, timeout time.Duration) (time.Duration, error) {
	if address == nil {
		return 0, errors.New("probe: address is not resolved")
	}
	socket, err := net.DialUDP("udp", nil, address)
	if err != nil {
		return 0, err
	}
	defer socket.Close()
	if p.bind != nil {
		if err := p.bind(socket); err != nil {
			return 0, err
		}
	}
	socket.SetDeadline(time.Now().Add(timeout))

	if params.I1 != nil {
		if _, err := socket.Write(params.I1); err != nil {
			return 0, err
		}
	}
	for i := 0; i < params.Jc; i++ {
		junk := make([]byte, params.Jmin+randInt(params.Jmax-params.Jmin+1))
		if _, err := rand.Read(junk); err != nil {
			return 0, err
		}
		if _, err := socket.Write(junk); err != nil {
			return 0, err
		}
	}

	senderIndex := randUint32()
	initiation, err := Initiation(p.privateKey, p.publicKey, p.peerPublic, senderIndex)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if _, err := socket.Write(initiation); err != nil {
		return 0, err
	}

	buffer := make([]byte, 512)
	for {
		n, err := socket.Read(buffer)
		if err != nil {
			return 0, err
		}
		if IsReplyTo(buffer[:n], senderIndex) {
			return time.Since(start), nil
		}
	}
}

func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	var b [8]byte
	rand.Read(b[:])
	return int(binary.LittleEndian.Uint64(b[:]) % uint64(n))
}

func randUint32() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}
