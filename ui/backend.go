package ui

import (
	"errors"
	"sync"
	"time"

	"github.com/alexus282/ByeByeRKN-Windows/control"
	"github.com/alexus282/ByeByeRKN-Windows/engine"
	"github.com/alexus282/ByeByeRKN-Windows/store"
)

// Backend is everything the window needs from whoever owns the tunnel: the service in a normal
// installation, or the engine inside the same process when the app runs portable and elevated.
type Backend interface {
	Settings() store.Settings
	Path() string
	HasAccount() bool
	// ServiceMode is true when the settings live in the protected service folder, which the
	// window must not offer to open.
	ServiceMode() bool

	Running() bool
	Status() engine.Status
	Traffic() engine.Traffic
	Connect()
	Disconnect()
	// DisconnectWait is for quitting: the process must not exit before the routes are gone.
	DisconnectWait(timeout time.Duration)

	ExportConfig() (string, error)
	ApplyWarpPlus(key string) (string, error)
	ForgetServer() error
	ResetAccount() error
	SetSettings(settings store.Settings) error

	SetHandlers(onStatus func(engine.Status), onTraffic func(engine.Traffic), onLog func(time.Time, string))
	InitialLogs() []logLine
	Close()
}

type localBackend struct {
	ctrl  *engine.Controller
	store *store.Store
}

// NewLocal is the portable mode: the window itself runs elevated and hosts the engine.
func NewLocal(ctrl *engine.Controller, st *store.Store) Backend {
	return &localBackend{ctrl: ctrl, store: st}
}

func (b *localBackend) Settings() store.Settings { return b.store.Settings() }
func (b *localBackend) Path() string             { return b.store.Path() }
func (b *localBackend) HasAccount() bool         { return b.store.Account() != nil }
func (b *localBackend) ServiceMode() bool        { return false }

func (b *localBackend) Running() bool                { return b.ctrl.Running() }
func (b *localBackend) Status() engine.Status        { return b.ctrl.Current() }
func (b *localBackend) Traffic() engine.Traffic      { return b.ctrl.TrafficNow() }
func (b *localBackend) Connect()                     { b.ctrl.Connect() }
func (b *localBackend) Disconnect()                  { go b.ctrl.Disconnect() }
func (b *localBackend) DisconnectWait(time.Duration) { b.ctrl.Disconnect() }

func (b *localBackend) ExportConfig() (string, error)            { return b.ctrl.ExportConfig() }
func (b *localBackend) ApplyWarpPlus(key string) (string, error) { return b.ctrl.ApplyWarpPlus(key) }
func (b *localBackend) ForgetServer() error                      { return b.store.ForgetServer() }
func (b *localBackend) ResetAccount() error {
	b.ctrl.Disconnect()
	return b.store.ResetAccount()
}
func (b *localBackend) SetSettings(settings store.Settings) error {
	return b.store.SetSettings(settings)
}

func (b *localBackend) SetHandlers(onStatus func(engine.Status), onTraffic func(engine.Traffic), onLog func(time.Time, string)) {
	b.ctrl.OnStatus = onStatus
	b.ctrl.OnTraffic = onTraffic
	b.ctrl.OnLog = func(line string) { onLog(time.Now(), line) }
}

func (b *localBackend) InitialLogs() []logLine { return nil }
func (b *localBackend) Close()                 {}

// remoteBackend talks to the service. It survives a service restart: when the pipe closes it
// retries in the background and tells the window what is happening.
type remoteBackend struct {
	mu        sync.Mutex
	client    *control.Client
	online    bool
	closed    bool
	onStatus  func(engine.Status)
	onTraffic func(engine.Traffic)
	onLog     func(time.Time, string)
}

// NewRemote is the normal mode: the service owns the tunnel and this process needs no rights.
func NewRemote(client *control.Client) Backend {
	b := &remoteBackend{client: client, online: true}
	client.SetHandler(b.dispatch)
	go b.watch(client)
	return b
}

// DialRemote connects to a running service. The error is meant for the user: it tells whether the
// service is missing or merely stopped.
func DialRemote(timeout time.Duration) (Backend, error) {
	client, err := control.Dial(timeout)
	if err != nil {
		return nil, err
	}
	return NewRemote(client), nil
}

func (b *remoteBackend) dispatch(message control.Message) {
	b.mu.Lock()
	onStatus, onTraffic, onLog := b.onStatus, b.onTraffic, b.onLog
	b.mu.Unlock()
	switch message.Type {
	case control.MsgHello, control.MsgStatus:
		if message.Status != nil && onStatus != nil {
			onStatus(message.Status.Engine())
		}
	case control.MsgTraffic:
		if message.Traffic != nil && onTraffic != nil {
			onTraffic(message.Traffic.Engine())
		}
	case control.MsgLog:
		if message.Log != nil && onLog != nil {
			onLog(message.Log.At, message.Log.Text)
		}
	}
}

// watch notices a dead pipe and reconnects. The service is restarted by SCM recovery, by an
// update, or by the user in services.msc; none of that should leave the window unusable.
func (b *remoteBackend) watch(old *control.Client) {
	resume := old.Running()
	<-old.Done()
	b.mu.Lock()
	if b.closed || b.client != old {
		b.mu.Unlock()
		return
	}
	b.online = false
	onStatus := b.onStatus
	b.mu.Unlock()
	if onStatus != nil {
		onStatus(engine.Status{State: engine.Failed, Text: "Служба ByeByeRKN остановилась. Переподключаюсь…"})
	}
	for {
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return
		}
		time.Sleep(2 * time.Second)
		client, err := control.Dial(2 * time.Second)
		if err != nil {
			continue
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			client.Close()
			return
		}
		b.client = client
		b.online = true
		b.mu.Unlock()
		client.SetHandler(b.dispatch)
		// Dial already consumed the hello, so deliver the state it carried.
		b.dispatch(control.Message{Type: control.MsgStatus, Status: ptrStatus(client.Status())})
		b.dispatch(control.Message{Type: control.MsgTraffic, Traffic: ptrTraffic(client.Traffic())})
		if resume && !client.Running() {
			// The service was updated or recovered while the window was open: keep the user's
			// VPN intent instead of silently leaving it disconnected.
			client.Connect()
		}
		return
	}
}

func ptrStatus(status engine.Status) *control.Status {
	converted := control.StatusOf(status)
	return &converted
}

func ptrTraffic(traffic engine.Traffic) *control.Traffic {
	converted := control.TrafficOf(traffic)
	return &converted
}

func (b *remoteBackend) current() (*control.Client, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.client, b.online
}

func (b *remoteBackend) Settings() store.Settings {
	client, _ := b.current()
	return client.Settings()
}

func (b *remoteBackend) Path() string {
	client, _ := b.current()
	return client.Path()
}

func (b *remoteBackend) HasAccount() bool {
	client, _ := b.current()
	return client.HasAccount()
}

func (b *remoteBackend) ServiceMode() bool { return true }

func (b *remoteBackend) Running() bool {
	client, online := b.current()
	return online && client.Running()
}

func (b *remoteBackend) Status() engine.Status {
	client, _ := b.current()
	return client.Status()
}

func (b *remoteBackend) Traffic() engine.Traffic {
	client, _ := b.current()
	return client.Traffic()
}

func (b *remoteBackend) Connect() {
	if client, online := b.current(); online {
		client.Connect()
	}
}

func (b *remoteBackend) Disconnect() {
	if client, online := b.current(); online {
		client.Disconnect()
	}
}

func (b *remoteBackend) DisconnectWait(timeout time.Duration) {
	if client, online := b.current(); online {
		client.DisconnectWait(timeout)
	}
}

var errOffline = errors.New("служба ByeByeRKN недоступна")

func (b *remoteBackend) ExportConfig() (string, error) {
	if client, online := b.current(); online {
		return client.ExportConfig()
	}
	return "", errOffline
}

func (b *remoteBackend) ApplyWarpPlus(key string) (string, error) {
	if client, online := b.current(); online {
		return client.ApplyWarpPlus(key)
	}
	return "", errOffline
}

func (b *remoteBackend) ForgetServer() error {
	if client, online := b.current(); online {
		return client.ForgetServer()
	}
	return errOffline
}

func (b *remoteBackend) ResetAccount() error {
	if client, online := b.current(); online {
		return client.ResetAccount()
	}
	return errOffline
}

func (b *remoteBackend) SetSettings(settings store.Settings) error {
	if client, online := b.current(); online {
		return client.SetSettings(settings)
	}
	return errOffline
}

func (b *remoteBackend) SetHandlers(onStatus func(engine.Status), onTraffic func(engine.Traffic), onLog func(time.Time, string)) {
	b.mu.Lock()
	b.onStatus, b.onTraffic, b.onLog = onStatus, onTraffic, onLog
	client := b.client
	b.mu.Unlock()
	client.SetHandler(b.dispatch)
}

func (b *remoteBackend) InitialLogs() []logLine {
	client, _ := b.current()
	lines := client.Logs()
	out := make([]logLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, logLine{at: line.At, text: line.Text})
	}
	return out
}

func (b *remoteBackend) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	client := b.client
	b.mu.Unlock()
	client.Close()
}
