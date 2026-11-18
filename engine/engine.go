// Package engine drives the Windows tunnel: it registers a WARP device, scans endpoints with real
// AmneziaWG handshakes, brings the Wintun adapter and its routes up, verifies that traffic really
// leaves through Cloudflare and watches the session for stalls. It follows the Android app's
// VpnController, without MASQUE for now.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/alexus282/ByeByeRKN/windows/awg"
	"github.com/alexus282/ByeByeRKN/windows/catalog"
	"github.com/alexus282/ByeByeRKN/windows/probe"
	"github.com/alexus282/ByeByeRKN/windows/store"
	"github.com/alexus282/ByeByeRKN/windows/warp"
	"github.com/alexus282/ByeByeRKN/windows/wintunnel"
)

const (
	// Adapter is both the Wintun adapter name and the netsh interface alias. No spaces: netsh
	// arguments are passed as-is.
	Adapter = "ByeByeRKN"

	scanEndpoints   = 32
	blindAttempts   = 3
	defaultMTU      = 1280
	probeTimeout    = 1500 * time.Millisecond
	monitorEvery    = 5
	monitorGrace    = 30 * time.Second
	firstRetry      = 5 * time.Second
	maxRetry        = 300 * time.Second
	reregisterEvery = 6 * time.Hour
)

// mtuSteps are tried in order when the handshake works but full-size packets get lost on the path.
var mtuSteps = []int{1280, 1200, 1100}

// Core opens the userspace WireGuard device. Tests replace it with a fake, since a real one needs
// administrator rights.
type Core interface {
	Open(name string, mtu int, uapi string) (Session, error)
}

// Session is one running device.
type Session interface {
	Stats() (Stats, error)
	Close()
}

type State int

const (
	Idle State = iota
	Working
	Connected
	Failed
)

// Status is what the window shows. Text is a human-readable stage or error.
type Status struct {
	State    State
	Text     string
	Endpoint string
	Mask     awg.Mask
	RTT      time.Duration
	Since    time.Time
	Verified bool
	Exit     Exit
}

type Traffic struct {
	Rx, Tx int64
}

type result int

const (
	failed result = iota
	handshakeOnly
	connected
)

type Controller struct {
	store *store.Store
	core  Core
	api   *warp.Client
	net   *wintunnel.Tunnel
	trace func(context.Context) (Exit, error)

	mu      sync.Mutex
	status  Status
	traffic Traffic
	running bool
	cancel  context.CancelFunc
	done    chan struct{}

	tunnelMu sync.Mutex
	session  Session

	OnStatus  func(Status)
	OnTraffic func(Traffic)
	OnLog     func(string)
}

func New(s *store.Store) *Controller {
	c := &Controller{
		store: s,
		api:   warp.NewClient(),
		net:   wintunnel.New(nil),
		trace: FetchTrace,
	}
	c.core = wintunCore{logf: c.logf}
	c.net.Logf = c.logf
	c.api.OnRetry = func(host, mode string) {
		c.logf("WARP API: повторная попытка через %s (%s)", host, mode)
	}
	return c
}

// Connect starts the search in the background. Calling it while already running does nothing.
func (c *Controller) Connect() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	done := make(chan struct{})
	c.done = done
	c.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			c.mu.Lock()
			c.running = false
			c.cancel = nil
			c.mu.Unlock()
		}()
		c.loop(ctx)
	}()
}

// Disconnect cancels the search and waits until the routes are removed and the device is closed.
func (c *Controller) Disconnect() {
	c.mu.Lock()
	cancel, done, running := c.cancel, c.done, c.running
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if running && done != nil {
		<-done
	}
}

func (c *Controller) Current() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Controller) TrafficNow() Traffic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.traffic
}

func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// loop retries with growing pauses until the user disconnects, as the Android app does: right after
// a failure WARP can refuse a device for a few seconds and then work.
func (c *Controller) loop(ctx context.Context) {
	defer func() {
		c.closeTunnel()
		c.setTraffic(Traffic{})
		c.setStatus(Status{State: Idle, Text: "Отключено"})
	}()

	retry := firstRetry
	for {
		err := c.connectOnce(ctx, true)
		if ctx.Err() != nil {
			return
		}
		c.closeTunnel()
		if err == nil {
			return
		}
		c.logf("не подключилось: %v", err)
		c.setStatus(Status{State: Failed, Text: err.Error()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
		retry *= 2
		if retry > maxRetry {
			retry = maxRetry
		}
	}
}

// connectOnce climbs the search ladder once: the remembered route, then every disguise over the
// address families that have not answered yet, then a few blind attempts.
func (c *Controller) connectOnce(ctx context.Context, mayReregister bool) error {
	settings := c.store.Settings()
	account, err := c.ensureAccount(settings)
	if err != nil {
		return err
	}

	// Probes must leave through the physical network: a tunnel that carries nothing would swallow
	// them and report "no server answers" about a healthy network.
	c.closeTunnel()
	scanner, err := c.newScanner(account)
	if err != nil {
		return err
	}

	sawHandshake := false
	tryRoute := func(candidate store.Candidate) bool {
		outcome := c.try(ctx, account, settings, candidate)
		if outcome != failed {
			sawHandshake = true
		}
		return outcome == connected
	}

	if last := c.store.LastGood(); last != nil && last.Matches(settings) {
		c.setStatus(Status{State: Working, Text: "Проверяю последний удачный сервер…"})
		if c.answers(ctx, account, *last) {
			if tryRoute(*last) {
				return c.serve(ctx)
			}
		} else {
			c.logf("последний сервер %s больше не отвечает", last.Endpoint)
			if err := c.store.ForgetServer(); err != nil {
				c.logf("не сохранил настройки: %v", err)
			}
		}
	}

	endpoints := probe.Sample(scanEndpoints)
	if settings.ManualEndpoint != "" {
		endpoints = []string{settings.ManualEndpoint}
	}
	masks := awg.AutoOrder
	if settings.Obfuscation != "" {
		mask, ok := awg.ParseMask(settings.Obfuscation)
		if !ok {
			return fmt.Errorf("неизвестная обфускация %q", settings.Obfuscation)
		}
		masks = []awg.Mask{mask}
	}

	reached := make(map[string]bool)
	for _, mask := range masks {
		if ctx.Err() != nil {
			return nil
		}
		var remaining []string
		for _, endpoint := range endpoints {
			if !reached[catalog.FamilyOf(endpoint)] {
				remaining = append(remaining, endpoint)
			}
		}
		if len(remaining) == 0 {
			break
		}
		params := awg.NewParams(mask, settings.QuicSNI)
		hits := scanner.Scan(remaining, []awg.Params{params}, func(done, total, found int) {
			c.setStatus(Status{State: Working, Text: fmt.Sprintf("Сканирую %s: %d/%d, ответили %d", mask, done, total, found)})
		})
		for _, hit := range hits {
			reached[catalog.FamilyOf(hit.Endpoint)] = true
		}
		for _, hit := range hits {
			candidate := store.Candidate{Endpoint: hit.Endpoint, Params: hit.Params, RTTMs: hit.RTT.Milliseconds(), MTU: defaultMTU}
			if tryRoute(candidate) {
				return c.serve(ctx)
			}
		}
	}

	if len(reached) == 0 && settings.ManualEndpoint == "" {
		// Probes may be filtered while the tunnel itself works: try a few blindly.
		c.setStatus(Status{State: Working, Text: "Пробую подключиться вслепую…"})
		for _, endpoint := range probe.Sample(blindAttempts) {
			candidate := store.Candidate{Endpoint: endpoint, Params: awg.NewParams(awg.QUIC, settings.QuicSNI), MTU: defaultMTU}
			if tryRoute(candidate) {
				return c.serve(ctx)
			}
		}
	}

	// Handshakes that fail everywhere can mean Cloudflare no longer accepts this key.
	if !sawHandshake && mayReregister && time.Since(c.store.LastRegisterAt()) > reregisterEvery {
		c.logf("ни один сервер не ответил на handshake: регистрирую новое устройство")
		if err := c.store.ResetAccount(); err != nil {
			return err
		}
		return c.connectOnce(ctx, false)
	}
	return errors.New("не нашлось ни одного рабочего сервера WARP")
}

// try connects through one route, falling back to a smaller MTU when the handshake works but
// traffic does not. On success it leaves the tunnel up and remembers the route.
func (c *Controller) try(ctx context.Context, account *warp.WarpAccount, settings store.Settings, candidate store.Candidate) result {
	if candidate.MTU == 0 {
		candidate.MTU = defaultMTU
	}
	steps := []int{candidate.MTU}
	for _, mtu := range mtuSteps {
		if mtu != candidate.MTU {
			steps = append(steps, mtu)
		}
	}
	for i, mtu := range steps {
		if ctx.Err() != nil {
			return failed
		}
		route := candidate
		route.MTU = mtu
		text := fmt.Sprintf("Подключаюсь: %s · %s", route.Endpoint, route.Params.Mask)
		if i > 0 {
			text += fmt.Sprintf(" · MTU %d", mtu)
		}
		c.setStatus(Status{State: Working, Text: text})

		outcome := c.establish(ctx, account, settings, route)
		switch outcome {
		case connected:
			if err := c.store.SetLastGood(&route); err != nil {
				c.logf("не сохранил удачный маршрут: %v", err)
			}
			return outcome
		case failed:
			// A smaller MTU cannot help a handshake that never happened.
			return failed
		}
	}
	return handshakeOnly
}

// establish brings one route up. The first request through the fresh tunnel is deliberate:
// WireGuard starts a handshake only when the OS sends something into the tunnel, so it both forces
// the handshake and proves that traffic really goes through WARP.
func (c *Controller) establish(ctx context.Context, account *warp.WarpAccount, settings store.Settings, candidate store.Candidate) result {
	endpoint, err := ResolveEndpoint(ctx, candidate.Endpoint)
	if err != nil {
		c.logf("%v", err)
		return failed
	}
	uapi, err := RenderUAPI(account.PrivateKey, account.PeerPublicKey, endpoint, candidate.Params)
	if err != nil {
		c.logf("конфигурация: %v", err)
		return failed
	}

	c.tunnelMu.Lock()
	defer c.tunnelMu.Unlock()
	// Remove routes and DNS while the interface alias still resolves. Closing Wintun first makes
	// netsh fail, and Windows can then reuse the interface index with stale tunnel routes.
	c.net.Teardown()
	c.closeSessionLocked()

	session, err := c.core.Open(Adapter, candidate.MTU, uapi)
	if err != nil {
		c.logf("не удалось создать адаптер: %v", err)
		return failed
	}
	c.session = session
	up := false
	defer func() {
		if !up {
			c.net.Teardown()
			c.closeSessionLocked()
		}
	}()

	if err := c.net.Configure(Adapter, account.AddressV4, account.AddressV6, candidate.MTU, store.DNSServers(settings.DNS)); err != nil {
		c.logf("настройка адаптера: %v", err)
		return failed
	}
	if err := c.net.PinEndpoint(Adapter, endpoint); err != nil {
		c.logf("маршрут до сервера: %v", err)
		return failed
	}
	if err := c.net.DefaultRoute(Adapter); err != nil {
		c.logf("основной маршрут: %v", err)
		return failed
	}

	exit, traceErr := c.trace(ctx)
	stats, statsErr := session.Stats()
	if statsErr != nil || stats.Handshake.IsZero() {
		c.logf("handshake не прошёл: %v", traceErr)
		return failed
	}
	if traceErr != nil {
		c.logf("handshake есть, но запрос через туннель не выполнен: %v", traceErr)
		c.logf("счётчики: rx=%d tx=%d; диагностика Windows: %s", stats.Rx, stats.Tx, c.net.Diagnose(Adapter, "1.1.1.1"))
		return handshakeOnly
	}
	if !exit.Warp {
		c.logf("handshake есть, но trace показывает warp=off: трафик идёт мимо туннеля")
		return handshakeOnly
	}

	// Traffic leaving through WARP is the whole proof. Extra probes only delay the connection and
	// cannot predict how an app will behave through an approximate exit.
	c.logf("подключено: %s · %s, выход %s (%s)", candidate.Endpoint, candidate.Params.Mask, exit.Colo, exit.Country)
	c.setStatus(Status{
		State: Connected, Text: "Подключено", Endpoint: candidate.Endpoint, Mask: candidate.Params.Mask,
		RTT: time.Duration(candidate.RTTMs) * time.Millisecond, Since: time.Now(), Verified: true,
		Exit: exit,
	})
	up = true
	return connected
}

// serve watches a connected session until it is cancelled or looks dead. Counters only raise a
// suspicion; a real request through the tunnel decides, to avoid flapping on an idle machine.
func (c *Controller) serve(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	detector := &stallDetector{}
	quietUntil := time.Now().Add(monitorGrace)
	ticks := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			stats, err := c.stats()
			if err != nil {
				return fmt.Errorf("статистика туннеля: %w", err)
			}
			c.setTraffic(Traffic{Rx: stats.Rx, Tx: stats.Tx})
			ticks++
			if ticks%monitorEvery != 0 {
				continue
			}
			now := time.Now()
			reason := detector.update(now, stats.Rx, stats.Tx, stats.Handshake)
			if reason == "" || now.Before(quietUntil) {
				continue
			}
			c.logf("соединение подозрительно: %s", reason)
			if exit, err := c.trace(ctx); err == nil && exit.Warp {
				detector.reset()
				quietUntil = now.Add(monitorGrace)
				continue
			}
			return fmt.Errorf("соединение потеряно: %s", reason)
		}
	}
}

func (c *Controller) stats() (Stats, error) {
	c.tunnelMu.Lock()
	defer c.tunnelMu.Unlock()
	if c.session == nil {
		return Stats{}, errors.New("туннель не запущен")
	}
	return c.session.Stats()
}

func (c *Controller) closeTunnel() {
	c.tunnelMu.Lock()
	c.net.Teardown()
	c.closeSessionLocked()
	c.tunnelMu.Unlock()
}

func (c *Controller) closeSessionLocked() {
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}
}

func (c *Controller) ensureAccount(settings store.Settings) (*warp.WarpAccount, error) {
	if account := c.store.Account(); account != nil {
		return account, nil
	}
	c.setStatus(Status{State: Working, Text: "Регистрирую устройство в WARP…"})
	account, err := c.api.Register()
	if err != nil {
		return nil, fmt.Errorf("регистрация WARP: %w", err)
	}
	if err := c.store.SetAccount(account); err != nil {
		return nil, err
	}
	if err := c.store.SetLastRegisterAt(time.Now()); err != nil {
		c.logf("не сохранил время регистрации: %v", err)
	}
	// A fresh device starts as free WARP: the stored WARP+ key, if any, goes on it too.
	if settings.WarpPlusKey != "" {
		if _, err := c.api.ApplyLicense(account, settings.WarpPlusKey); err != nil {
			c.logf("WARP+ ключ не привязан: %v", err)
		}
	}
	return account, nil
}

// ApplyWarpPlus binds a WARP+ key and remembers it for later registrations.
func (c *Controller) ApplyWarpPlus(key string) (string, error) {
	license := strings.TrimSpace(key)
	if !warp.IsLicense(license) {
		return "", errors.New("ключ должен выглядеть как xxxxxxxx-xxxxxxxx-xxxxxxxx")
	}
	settings := c.store.Settings()
	account, err := c.ensureAccount(settings)
	if err != nil {
		return "", err
	}
	plus, err := c.api.ApplyLicense(account, license)
	if err != nil {
		return "", err
	}
	settings.WarpPlusKey = license
	if err := c.store.SetSettings(settings); err != nil {
		return "", err
	}
	if plus.WarpPlus {
		return "WARP+ активирован", nil
	}
	return "Ключ принят: " + plus.AccountType, nil
}

// ExportConfig is the session as a wg-quick config, for import into another AmneziaWG client.
func (c *Controller) ExportConfig() (string, error) {
	account := c.store.Account()
	if account == nil {
		return "", errors.New("аккаунт WARP ещё не создан: подключитесь один раз")
	}
	settings := c.store.Settings()
	candidate := store.Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.QUIC, settings.QuicSNI), MTU: defaultMTU}
	if settings.ManualEndpoint != "" {
		candidate.Endpoint = settings.ManualEndpoint
	}
	if mask, ok := awg.ParseMask(settings.Obfuscation); ok {
		candidate.Params = awg.NewParams(mask, settings.QuicSNI)
	}
	if last := c.store.LastGood(); last != nil && last.Matches(settings) {
		candidate = *last
	}
	if candidate.MTU == 0 {
		candidate.MTU = defaultMTU
	}
	return RenderConfig(account, candidate.Endpoint, candidate.Params, store.DNSServers(settings.DNS), candidate.MTU), nil
}

func (c *Controller) newScanner(account *warp.WarpAccount) (*probe.Scanner, error) {
	p, err := probe.NewFromBase64(account.PrivateKey, account.PeerPublicKey, nil)
	if err != nil {
		return nil, err
	}
	scanner := probe.NewScanner(p)
	scanner.Timeout = probeTimeout
	return scanner, nil
}

func (c *Controller) answers(ctx context.Context, account *warp.WarpAccount, candidate store.Candidate) bool {
	p, err := probe.NewFromBase64(account.PrivateKey, account.PeerPublicKey, nil)
	if err != nil {
		c.logf("probe: %v", err)
		return false
	}
	endpoint, err := ResolveEndpoint(ctx, candidate.Endpoint)
	if err != nil {
		return false
	}
	address, err := net.ResolveUDPAddr("udp", endpoint)
	if err != nil {
		return false
	}
	_, err = p.Probe(address, candidate.Params, probeTimeout)
	return err == nil
}

func (c *Controller) setStatus(status Status) {
	c.mu.Lock()
	c.status = status
	callback := c.OnStatus
	c.mu.Unlock()
	if callback != nil {
		callback(status)
	}
}

func (c *Controller) setTraffic(traffic Traffic) {
	c.mu.Lock()
	c.traffic = traffic
	callback := c.OnTraffic
	c.mu.Unlock()
	if callback != nil {
		callback(traffic)
	}
}

func (c *Controller) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	c.mu.Lock()
	callback := c.OnLog
	c.mu.Unlock()
	if callback != nil {
		callback(line)
	}
}
