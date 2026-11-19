package engine

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
	"github.com/alexus282/ByeByeRKN-Windows/warp"
)

// RenderUAPI builds amneziawg-go's configuration protocol: the device section up to the first
// public_key, then the single WARP peer. An empty line ends the operation.
func RenderUAPI(privateKey, peerPublicKey, endpoint string, params awg.Params) (string, error) {
	own, err := keyHex(privateKey)
	if err != nil {
		return "", fmt.Errorf("private key: %w", err)
	}
	peer, err := keyHex(peerPublicKey)
	if err != nil {
		return "", fmt.Errorf("peer key: %w", err)
	}
	address, err := netip.ParseAddrPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint %q: %w", endpoint, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=0\nreplace_peers=true\n", own)
	b.WriteString(params.UAPI())
	fmt.Fprintf(&b, "public_key=%s\nendpoint=%s\n", peer, address.String())
	b.WriteString("allowed_ip=0.0.0.0/0\nallowed_ip=::/0\npersistent_keepalive_interval=25\n\n")
	return b.String(), nil
}

func keyHex(key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	if len(raw) != 32 {
		return "", errors.New("must be 32 bytes")
	}
	return hex.EncodeToString(raw), nil
}

// ResolveEndpoint turns a manual endpoint, which may be a host name, into the literal address the
// UAPI requires. Names are looked up before the tunnel exists: afterwards DNS points at it.
func ResolveEndpoint(ctx context.Context, endpoint string) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint %q: %w", endpoint, err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("endpoint %q: bad port", endpoint)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(ip, uint16(number)).String(), nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("endpoint %q: %w", endpoint, err)
	}
	for _, address := range addresses {
		if ip, ok := netip.AddrFromSlice(address.IP); ok && ip.Is4() {
			return netip.AddrPortFrom(ip, uint16(number)).String(), nil
		}
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("endpoint %q: no addresses", endpoint)
	}
	ip, _ := netip.AddrFromSlice(addresses[0].IP)
	return netip.AddrPortFrom(ip, uint16(number)).String(), nil
}

// Stats is the part of IpcGet the app needs: when the peer last answered and how much went both
// ways.
type Stats struct {
	Handshake time.Time
	Rx, Tx    int64
	Endpoint  string
}

func ParseIpcGet(text string) Stats {
	var (
		stats       Stats
		seconds     int64
		nanoseconds int64
	)
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "last_handshake_time_sec":
			seconds, _ = strconv.ParseInt(value, 10, 64)
		case "last_handshake_time_nsec":
			nanoseconds, _ = strconv.ParseInt(value, 10, 64)
		case "rx_bytes":
			stats.Rx, _ = strconv.ParseInt(value, 10, 64)
		case "tx_bytes":
			stats.Tx, _ = strconv.ParseInt(value, 10, 64)
		case "endpoint":
			stats.Endpoint = value
		}
	}
	if seconds > 0 {
		stats.Handshake = time.Unix(seconds, nanoseconds)
	}
	return stats
}

// RenderConfig is the wg-quick form of the same session, for import into another AmneziaWG client.
func RenderConfig(account *warp.WarpAccount, endpoint string, params awg.Params, dns []string, mtu int) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", account.PrivateKey)
	fmt.Fprintf(&b, "Address = %s/32, %s/128\n", account.AddressV4, account.AddressV6)
	fmt.Fprintf(&b, "DNS = %s\nMTU = %d\n", strings.Join(dns, ", "), mtu)
	for _, line := range params.ConfigLines() {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", account.PeerPublicKey)
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s\nPersistentKeepalive = 25\n", endpoint)
	return b.String()
}
