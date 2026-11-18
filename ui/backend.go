package ui

import (
	"errors"
	"time"

	"github.com/alexus282/ByeByeRKN/windows/control"
	"github.com/alexus282/ByeByeRKN/windows/engine"
	"github.com/alexus282/ByeByeRKN/windows/store"
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

func (b *localBackend) Running() bool                  { return b.ctrl.Running() }
func (b *localBackend) Status() engine.Status          { return b.ctrl.Current() }
func (b *localBackend) Traffic() engine.Traffic        { return b.ctrl.TrafficNow() }
func (b *localBackend) Connect()                       { b.ctrl.Connect() }
func (b *localBackend) Disconnect()                    { go b.ctrl.Disconnect() }
func (b *localBackend) DisconnectWait(t time.Duration) { b.ctrl.Disconnect() }

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

type remoteBackend struct {
	client *control.Client
}

// NewRemote is the normal mode: the service owns the tunnel and this process needs no rights.
func NewRemote(client *control.Client) Backend {
	return &remoteBackend{client: client}
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

func (b *remoteBackend) Settings() store.Settings { return b.client.Settings() }
func (b *remoteBackend) Path() string             { return b.client.Path() }
func (b *remoteBackend) HasAccount() bool         { return b.client.HasAccount() }
func (b *remoteBackend) ServiceMode() bool        { return true }

func (b *remoteBackend) Running() bool                  { return b.client.Running() }
func (b *remoteBackend) Status() engine.Status          { return b.client.Status() }
func (b *remoteBackend) Traffic() engine.Traffic        { return b.client.Traffic() }
func (b *remoteBackend) Connect()                       { b.client.Connect() }
func (b *remoteBackend) Disconnect()                    { b.client.Disconnect() }
func (b *remoteBackend) DisconnectWait(t time.Duration) { b.client.DisconnectWait(t) }

func (b *remoteBackend) ExportConfig() (string, error)            { return b.client.ExportConfig() }
func (b *remoteBackend) ApplyWarpPlus(key string) (string, error) { return b.client.ApplyWarpPlus(key) }
func (b *remoteBackend) ForgetServer() error                      { return b.client.ForgetServer() }
func (b *remoteBackend) ResetAccount() error                      { return b.client.ResetAccount() }
func (b *remoteBackend) SetSettings(settings store.Settings) error {
	return b.client.SetSettings(settings)
}

func (b *remoteBackend) SetHandlers(onStatus func(engine.Status), onTraffic func(engine.Traffic), onLog func(time.Time, string)) {
	b.client.SetHandler(func(message control.Message) {
		switch message.Type {
		case control.MsgStatus:
			if message.Status != nil {
				onStatus(message.Status.Engine())
			}
		case control.MsgTraffic:
			if message.Traffic != nil {
				onTraffic(message.Traffic.Engine())
			}
		case control.MsgLog:
			if message.Log != nil {
				onLog(message.Log.At, message.Log.Text)
			}
		}
	})
}

func (b *remoteBackend) InitialLogs() []logLine {
	lines := b.client.Logs()
	out := make([]logLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, logLine{at: line.At, text: line.Text})
	}
	return out
}

func (b *remoteBackend) Close() { b.client.Close() }

// ErrNoService explains the missing pipe without leaking winio's wording into the dialog.
var ErrNoService = errors.New("служба ByeByeRKN не установлена")
