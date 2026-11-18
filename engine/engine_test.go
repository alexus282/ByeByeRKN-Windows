package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexus282/ByeByeRKN/windows/awg"
	"github.com/alexus282/ByeByeRKN/windows/store"
	"github.com/alexus282/ByeByeRKN/windows/warp"
	"github.com/alexus282/ByeByeRKN/windows/wintunnel"
)

func testKey(fill byte) string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestRenderUAPISplitsDeviceAndPeerSections(t *testing.T) {
	params := awg.NewParams(awg.QUIC, "ya.ru")
	text, err := RenderUAPI(testKey(1), testKey(2), "162.159.192.1:2408", params)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"private_key=" + strings.Repeat("01", 32),
		"listen_port=0",
		"replace_peers=true",
		"h1=1",
		"i1=<b 0x",
		"public_key=" + strings.Repeat("02", 32),
		"endpoint=162.159.192.1:2408",
		"allowed_ip=0.0.0.0/0",
		"allowed_ip=::/0",
		"persistent_keepalive_interval=25",
	} {
		if !strings.Contains(text, want+"\n") && !strings.HasSuffix(want, "0x") {
			t.Fatalf("%q missing in:\n%s", want, text)
		}
	}
	// Device keys must precede the peer: after public_key the parser switches sections.
	if strings.Index(text, "jc=") > strings.Index(text, "public_key=") {
		t.Fatal("obfuscation keys ended up in the peer section")
	}
	if !strings.HasSuffix(text, "\n\n") {
		t.Fatal("the operation must end with an empty line")
	}
}

func TestRenderUAPIRejectsUnresolvedEndpoints(t *testing.T) {
	if _, err := RenderUAPI(testKey(1), testKey(2), "engage.cloudflareclient.com:2408", awg.Params{}); err == nil {
		t.Fatal("a name is not a valid UAPI endpoint")
	}
}

func TestResolveEndpoint(t *testing.T) {
	for endpoint, want := range map[string]string{
		"162.159.192.1:2408":           "162.159.192.1:2408",
		"[2606:4700:d0::a29f:c001]:80": "[2606:4700:d0::a29f:c001]:80",
	} {
		got, err := ResolveEndpoint(context.Background(), endpoint)
		if err != nil || got != want {
			t.Fatalf("%s: got %q, %v", endpoint, got, err)
		}
	}
	if _, err := ResolveEndpoint(context.Background(), "162.159.192.1"); err == nil {
		t.Fatal("an endpoint without a port must be rejected")
	}
}

func TestParseIpcGet(t *testing.T) {
	stats := ParseIpcGet("private_key=00\nlisten_port=0\nlast_handshake_time_sec=1700000000\nlast_handshake_time_nsec=500\ntx_bytes=100\nrx_bytes=200\nendpoint=162.159.192.1:2408\n")
	if stats.Rx != 200 || stats.Tx != 100 || stats.Endpoint != "162.159.192.1:2408" {
		t.Fatalf("%+v", stats)
	}
	if stats.Handshake.Unix() != 1700000000 {
		t.Fatalf("handshake: %v", stats.Handshake)
	}
	if !ParseIpcGet("rx_bytes=1\n").Handshake.IsZero() {
		t.Fatal("no handshake must stay zero")
	}
}

func TestParseTrace(t *testing.T) {
	exit, ok := ParseTrace("fl=1\nip=104.28.1.1\nts=1\ncolo=FRA\nwarp=on\nloc=RU\n")
	if !ok || !exit.Warp || exit.Colo != "FRA" || exit.Country != "RU" {
		t.Fatalf("%+v %v", exit, ok)
	}
	if _, ok := ParseTrace("warp=off\n"); ok {
		t.Fatal("a response without colo is not a trace")
	}
}

func TestStallDetector(t *testing.T) {
	now := time.Now()
	d := &stallDetector{}
	// An idle session sends only keepalives and must never look dead.
	for i := 0; i < 6; i++ {
		at := now.Add(time.Duration(i) * 5 * time.Second)
		if reason := d.update(at, 1000, 1000+int64(i)*32, at); reason != "" {
			t.Fatalf("idle session flagged: %s", reason)
		}
	}
	// Sending real traffic with nothing coming back is a stall.
	d.reset()
	d.update(now, 1000, 1000, now)
	if reason := d.update(now.Add(20*time.Second), 1000, 9000, now); reason == "" {
		t.Fatal("sending without replies must be flagged")
	}
	// A stale handshake is a stall even when counters are quiet.
	d.reset()
	if reason := d.update(now, 1000, 1000, now.Add(-201*time.Second)); reason == "" {
		t.Fatal("a stale handshake must be flagged")
	}
	// Replaced tunnels restart their counters; that is not a stall.
	d.reset()
	d.update(now, 5000, 5000, now)
	if reason := d.update(now.Add(20*time.Second), 10, 10, now); reason != "" {
		t.Fatalf("counter reset flagged: %s", reason)
	}
}

type fakeCore struct {
	uapi     []string
	mtus     []int
	stats    Stats
	openErr  error
	sessions []*fakeSession
}

func (f *fakeCore) Open(name string, mtu int, uapi string) (Session, error) {
	f.uapi = append(f.uapi, uapi)
	f.mtus = append(f.mtus, mtu)
	if f.openErr != nil {
		return nil, f.openErr
	}
	session := &fakeSession{stats: f.stats}
	f.sessions = append(f.sessions, session)
	return session, nil
}

type fakeSession struct {
	stats   Stats
	closed  bool
	onClose func()
}

func (s *fakeSession) Stats() (Stats, error) { return s.stats, nil }
func (s *fakeSession) Close() {
	s.closed = true
	if s.onClose != nil {
		s.onClose()
	}
}

type recordingRunner struct{ calls []string }

func (r *recordingRunner) Run(name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}

func (r *recordingRunner) Output(name string, args ...string) (string, error) {
	return `{"iface":"Ethernet 3","next":"10.10.12.1"}`, nil
}

func newTestController(t *testing.T) (*Controller, *fakeCore, *recordingRunner) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := New(s)
	core := &fakeCore{}
	runner := &recordingRunner{}
	c.core = core
	c.net = wintunnel.New(runner)
	return c, core, runner
}

func testAccount() *warp.WarpAccount {
	return &warp.WarpAccount{
		PrivateKey: testKey(1), PeerPublicKey: testKey(2),
		AddressV4: "172.16.0.2", AddressV6: "2606:4700:110::1",
	}
}

func TestEstablishBringsUpRoutesAndKeepsThemOnSuccess(t *testing.T) {
	c, core, runner := newTestController(t)
	core.stats = Stats{Handshake: time.Now()}
	c.trace = func(context.Context) (Exit, error) { return Exit{Colo: "FRA", Warp: true}, nil }

	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.QUIC, "ya.ru"), MTU: 1280, RTTMs: 42}
	if got := c.establish(context.Background(), testAccount(), store.Settings{}, candidate); got != connected {
		t.Fatalf("got %v", got)
	}
	if len(core.uapi) != 1 || !strings.Contains(core.uapi[0], "endpoint=162.159.192.1:2408") {
		t.Fatalf("uapi: %v", core.uapi)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"netsh interface ipv4 add route 162.159.192.1/32 Ethernet 3 10.10.12.1",
		"netsh interface ipv4 add route 0.0.0.0/1 ByeByeRKN 0.0.0.0",
		"netsh interface ipv4 add route 128.0.0.0/1 ByeByeRKN 0.0.0.0",
		"netsh interface ipv6 add route ::/1 ByeByeRKN ::",
		"netsh interface ipv6 add route 8000::/1 ByeByeRKN ::",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q missing in:\n%s", want, joined)
		}
	}
	// The pin must exist before the default route, otherwise the tunnel sends into itself.
	if strings.Index(joined, "add route 162.159.192.1/32") > strings.Index(joined, "add route 0.0.0.0/1") {
		t.Fatal("the endpoint was pinned after the default route")
	}
	if core.sessions[0].closed {
		t.Fatal("a working session must stay open")
	}
	if c.Current().State != Connected || c.Current().RTT != 42*time.Millisecond {
		t.Fatalf("status: %+v", c.Current())
	}
}

// The colo is shown, not judged: traffic through WARP is the whole proof, whatever city the exit
// reports. There is no extra probe that could delay the connection or warn about a working VPN.
func TestEstablishAcceptsRussianExitWithoutExtraChecks(t *testing.T) {
	c, core, _ := newTestController(t)
	core.stats = Stats{Handshake: time.Now()}
	c.trace = func(context.Context) (Exit, error) { return Exit{Colo: "DME", Country: "RU", Warp: true}, nil }
	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.QUIC, ""), MTU: 1280}
	started := time.Now()
	if got := c.establish(context.Background(), testAccount(), store.Settings{}, candidate); got != connected {
		t.Fatalf("got %v", got)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a working route took %v; nothing should be probed after the trace", elapsed)
	}
	if status := c.Current(); status.Text != "Подключено" {
		t.Fatalf("status: %+v", status)
	}
}

func TestReplacementRemovesOldRoutesBeforeClosingTheAdapter(t *testing.T) {
	c, core, runner := newTestController(t)
	core.stats = Stats{Handshake: time.Now()}
	c.trace = func(context.Context) (Exit, error) { return Exit{Colo: "FRA", Warp: true}, nil }
	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.QUIC, ""), MTU: 1280}
	if got := c.establish(context.Background(), testAccount(), store.Settings{}, candidate); got != connected {
		t.Fatalf("first establish: %v", got)
	}
	removedBeforeClose := false
	core.sessions[0].onClose = func() {
		removedBeforeClose = strings.Contains(strings.Join(runner.calls, "\n"), "delete route 128.0.0.0/1 ByeByeRKN")
	}
	candidate.Endpoint = "162.159.192.5:1701"
	if got := c.establish(context.Background(), testAccount(), store.Settings{}, candidate); got != connected {
		t.Fatalf("second establish: %v", got)
	}
	if !removedBeforeClose {
		t.Fatal("Wintun was closed while its routes and DNS still had to be removed")
	}
}

func TestEstablishTearsDownWhenNothingAnswers(t *testing.T) {
	c, core, runner := newTestController(t)
	core.stats = Stats{} // no handshake
	c.trace = func(context.Context) (Exit, error) { return Exit{}, errors.New("timeout") }

	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.None, ""), MTU: 1280}
	if got := c.establish(context.Background(), testAccount(), store.Settings{}, candidate); got != failed {
		t.Fatalf("got %v", got)
	}
	if !core.sessions[0].closed {
		t.Fatal("a failed session must be closed")
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "delete route 128.0.0.0/1 ByeByeRKN") {
		t.Fatal("the default route must be removed after a failure")
	}
}

func TestTryFallsBackToSmallerMTUOnlyAfterAHandshake(t *testing.T) {
	c, core, _ := newTestController(t)
	core.stats = Stats{Handshake: time.Now()}
	calls := 0
	c.trace = func(context.Context) (Exit, error) {
		calls++
		if calls == 1 {
			return Exit{}, errors.New("full-size packets lost")
		}
		return Exit{Colo: "FRA", Warp: true}, nil
	}
	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.QUIC, "ya.ru"), MTU: 1280}
	if got := c.try(context.Background(), testAccount(), store.Settings{}, candidate); got != connected {
		t.Fatalf("got %v", got)
	}
	if len(core.mtus) != 2 || core.mtus[0] != 1280 || core.mtus[1] != 1200 {
		t.Fatalf("mtu steps: %v", core.mtus)
	}
	if last := c.store.LastGood(); last == nil || last.MTU != 1200 {
		t.Fatalf("remembered route: %+v", last)
	}
}

func TestTryDoesNotRetryMTUWithoutAHandshake(t *testing.T) {
	c, core, _ := newTestController(t)
	core.stats = Stats{}
	c.trace = func(context.Context) (Exit, error) { return Exit{}, errors.New("no answer") }
	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.None, ""), MTU: 1280}
	if got := c.try(context.Background(), testAccount(), store.Settings{}, candidate); got != failed {
		t.Fatalf("got %v", got)
	}
	if len(core.mtus) != 1 {
		t.Fatalf("smaller MTU cannot fix a missing handshake: %v", core.mtus)
	}
}

func TestRenderConfig(t *testing.T) {
	text := RenderConfig(testAccount(), "162.159.192.1:2408", awg.NewParams(awg.None, ""), []string{"1.1.1.1"}, 1280)
	for _, want := range []string{"[Interface]", "Address = 172.16.0.2/32, 2606:4700:110::1/128", "DNS = 1.1.1.1", "MTU = 1280", "[Peer]", "AllowedIPs = 0.0.0.0/0, ::/0", "PersistentKeepalive = 25"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing in:\n%s", want, text)
		}
	}
}

func TestDisconnectWithoutAConnectionReturns(t *testing.T) {
	c, _, _ := newTestController(t)
	done := make(chan struct{})
	go func() { c.Disconnect(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Disconnect must not block when nothing runs")
	}
}
