// Package control is the line between the unelevated window and the service that owns the
// tunnel: a JSON-over-named-pipe protocol, the service side that drives the engine, and the client
// the window uses. The service runs as LocalSystem, so the window never needs administrator rights.
package control

import (
	"time"

	"github.com/alexus282/ByeByeRKN/windows/awg"
	"github.com/alexus282/ByeByeRKN/windows/engine"
	"github.com/alexus282/ByeByeRKN/windows/store"
)

const (
	// PipeName is the service endpoint. The service restricts it to its installing user, so a
	// private key is not readable by every account on the machine.
	PipeName = `\\.\pipe\ByeByeRKN`
	// ServiceName is both the SCM name and the name svc.Run registers.
	ServiceName = "ByeByeRKN"
	// ServiceTitle is what Services.msc and the uninstall entry show.
	ServiceTitle = "ByeByeRKN VPN"
)

// Commands the window sends.
const (
	CmdPing       = "ping"
	CmdConnect    = "connect"
	CmdDisconnect = "disconnect"
	CmdSettings   = "settings"
	CmdExport     = "export"
	CmdWarpPlus   = "warp-plus"
	CmdForget     = "forget-server"
	CmdReset      = "reset-account"
)

// Message types the service sends.
const (
	MsgHello    = "hello"
	MsgStatus   = "status"
	MsgTraffic  = "traffic"
	MsgLog      = "log"
	MsgSettings = "settings"
	MsgAccount  = "account"
	MsgExport   = "export"
	MsgWarpPlus = "warp-plus"
	MsgDone     = "done"
	MsgPong     = "pong"
	MsgError    = "error"
)

// Request is one command from the window. ID distinguishes replies for commands that expect one.
type Request struct {
	ID       int             `json:"id,omitempty"`
	Cmd      string          `json:"cmd"`
	Settings *store.Settings `json:"settings,omitempty"`
	Key      string          `json:"key,omitempty"`
}

// Message is everything the service sends: a snapshot, a broadcast or a reply.
type Message struct {
	Type       string          `json:"type"`
	ID         int             `json:"id,omitempty"`
	Status     *Status         `json:"status,omitempty"`
	Traffic    *Traffic        `json:"traffic,omitempty"`
	Settings   *store.Settings `json:"settings,omitempty"`
	Path       string          `json:"path,omitempty"`
	HasAccount bool            `json:"hasAccount,omitempty"`
	Running    bool            `json:"running,omitempty"`
	Logs       []LogLine       `json:"logs,omitempty"`
	Log        *LogLine        `json:"log,omitempty"`
	Text       string          `json:"text,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// LogLine is one engine message with the moment it happened.
type LogLine struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Status is engine.Status without Go types the window would have to import.
type Status struct {
	State    int       `json:"state"`
	Text     string    `json:"text,omitempty"`
	Endpoint string    `json:"endpoint,omitempty"`
	Mask     int       `json:"mask"`
	RTTMs    int64     `json:"rttMs,omitempty"`
	Since    time.Time `json:"since,omitempty"`
	Verified bool      `json:"verified,omitempty"`
	Exit     Exit      `json:"exit"`
}

type Exit struct {
	Colo    string `json:"colo,omitempty"`
	Country string `json:"country,omitempty"`
	IP      string `json:"ip,omitempty"`
	Warp    bool   `json:"warp,omitempty"`
}

type Traffic struct {
	Rx int64 `json:"rx"`
	Tx int64 `json:"tx"`
}

func StatusOf(status engine.Status) Status {
	return Status{
		State: int(status.State), Text: status.Text, Endpoint: status.Endpoint, Mask: int(status.Mask),
		RTTMs: status.RTT.Milliseconds(), Since: status.Since, Verified: status.Verified,
		Exit: Exit{Colo: status.Exit.Colo, Country: status.Exit.Country, IP: status.Exit.IP, Warp: status.Exit.Warp},
	}
}

func (s Status) Engine() engine.Status {
	return engine.Status{
		State: engine.State(s.State), Text: s.Text, Endpoint: s.Endpoint, Mask: awg.Mask(s.Mask),
		RTT: time.Duration(s.RTTMs) * time.Millisecond, Since: s.Since, Verified: s.Verified,
		Exit: engine.Exit{Colo: s.Exit.Colo, Country: s.Exit.Country, IP: s.Exit.IP, Warp: s.Exit.Warp},
	}
}

func TrafficOf(traffic engine.Traffic) Traffic {
	return Traffic{Rx: traffic.Rx, Tx: traffic.Tx}
}

func (t Traffic) Engine() engine.Traffic { return engine.Traffic{Rx: t.Rx, Tx: t.Tx} }
