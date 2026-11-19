package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"github.com/alexus282/ByeByeRKN-Windows/engine"
	"github.com/alexus282/ByeByeRKN-Windows/store"
)

const maxServerLogs = 500

// Server owns the engine inside the service. It accepts window connections and forwards
// commands, while engine events are broadcast to every connected window.
type Server struct {
	store *store.Store
	ctrl  *engine.Controller
	// PipeName is overridable so tests do not fight the installed service.
	PipeName string
	dataDir  string

	mu       sync.Mutex
	listener net.Listener
	clients  map[*pipeClient]struct{}
	logs     []LogLine
	stopped  bool

	logMu   sync.Mutex
	logFile *os.File
}

func NewServer(st *store.Store) *Server {
	s := &Server{
		store: st, ctrl: engine.New(st), PipeName: PipeName, dataDir: filepath.Dir(st.Path()),
		clients: make(map[*pipeClient]struct{}),
	}
	s.ctrl.OnStatus = func(status engine.Status) {
		// The engine reports Idle from inside the loop, before its goroutine clears the running
		// flag; sending that stale true would leave the window stuck on "Подключение…".
		running := s.ctrl.Running()
		if status.State == engine.Idle {
			running = false
		}
		s.broadcast(Message{Type: MsgStatus, Status: ptr(StatusOf(status)), Running: running})
	}
	s.ctrl.OnTraffic = func(traffic engine.Traffic) {
		s.broadcast(Message{Type: MsgTraffic, Traffic: ptr(TrafficOf(traffic))})
	}
	s.ctrl.OnLog = func(text string) { s.log(LogLine{At: time.Now(), Text: text}) }
	return s
}

func ptr[T any](value T) *T { return &value }

// ListenAndServe blocks until Stop is called. Only one service may own the pipe.
func (s *Server) ListenAndServe() error {
	listener, err := winio.ListenPipe(s.PipeName, &winio.PipeConfig{SecurityDescriptor: s.descriptor()})
	if err != nil {
		return fmt.Errorf("named pipe: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	for {
		conn, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			stopped := s.stopped
			s.mu.Unlock()
			if stopped {
				return nil
			}
			return err
		}
		go s.serve(conn)
	}
}

// Stop tears the tunnel down before the service exits, so no routes are left behind.
func (s *Server) Stop() {
	s.mu.Lock()
	already := s.stopped
	s.stopped = true
	listener := s.listener
	clients := make([]*pipeClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	if already {
		return
	}
	if listener != nil {
		listener.Close()
	}
	s.ctrl.Disconnect()
	for _, client := range clients {
		client.conn.Close()
	}
	s.logMu.Lock()
	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}
	s.logMu.Unlock()
}

type pipeClient struct {
	conn net.Conn
	mu   sync.Mutex
	enc  *json.Encoder
}

func (c *pipeClient) send(message Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enc.Encode(message)
}

func (s *Server) serve(conn net.Conn) {
	client := &pipeClient{conn: conn, enc: json.NewEncoder(conn)}
	settings := s.store.Settings()
	client.send(Message{
		Type: MsgHello, Settings: &settings, Path: s.store.Path(),
		HasAccount: s.store.Account() != nil, Running: s.ctrl.Running(),
		Status: ptr(StatusOf(s.ctrl.Current())), Traffic: ptr(TrafficOf(s.ctrl.TrafficNow())),
		Logs: s.recentLogs(),
	})

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.clients[client] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
		conn.Close()
	}()

	decoder := json.NewDecoder(conn)
	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			return
		}
		s.handle(client, request)
	}
}

func (s *Server) handle(client *pipeClient, request Request) {
	switch request.Cmd {
	case CmdPing:
		client.send(Message{Type: MsgPong, ID: request.ID})
	case CmdConnect:
		s.ctrl.Connect()
		// The first engine status may take a moment (registering, DNS); let the window switch to
		// "Подключение…" at once.
		s.broadcast(Message{Type: MsgStatus, Status: ptr(StatusOf(s.ctrl.Current())), Running: s.ctrl.Running()})
		client.send(Message{Type: MsgDone, ID: request.ID})
	case CmdDisconnect:
		s.disconnect(request.ID, client)
	case CmdSettings:
		if request.Settings == nil {
			s.reply(client, request.ID, MsgError, "", errors.New("пустые настройки"))
			return
		}
		if err := s.store.SetSettings(*request.Settings); err != nil {
			s.reply(client, request.ID, MsgError, "", err)
			return
		}
		s.broadcastSettings()
		s.reply(client, request.ID, MsgDone, "", nil)
	case CmdForget:
		if err := s.store.ForgetServer(); err != nil {
			s.reply(client, request.ID, MsgError, "", err)
			return
		}
		s.reply(client, request.ID, MsgDone, "", nil)
	case CmdExport:
		go func() {
			text, err := s.ctrl.ExportConfig()
			if err != nil {
				s.reply(client, request.ID, MsgError, "", err)
				return
			}
			s.reply(client, request.ID, MsgExport, text, nil)
		}()
	case CmdWarpPlus:
		go func() {
			text, err := s.ctrl.ApplyWarpPlus(request.Key)
			if err != nil {
				s.reply(client, request.ID, MsgError, "", err)
				return
			}
			s.broadcastSettings()
			s.broadcast(Message{Type: MsgAccount, HasAccount: s.store.Account() != nil})
			s.reply(client, request.ID, MsgWarpPlus, text, nil)
		}()
	case CmdReset:
		go func() {
			s.ctrl.Disconnect()
			if err := s.store.ResetAccount(); err != nil {
				s.reply(client, request.ID, MsgError, "", err)
				return
			}
			s.broadcast(Message{Type: MsgAccount, HasAccount: false})
			s.reply(client, request.ID, MsgDone, "", nil)
		}()
	default:
		s.reply(client, request.ID, MsgError, "", fmt.Errorf("неизвестная команда %q", request.Cmd))
	}
}

func (s *Server) disconnect(id int, client *pipeClient) {
	if id == 0 {
		go s.ctrl.Disconnect()
		return
	}
	go func() {
		s.ctrl.Disconnect()
		s.reply(client, id, MsgDone, "", nil)
	}()
}

func (s *Server) reply(client *pipeClient, id int, kind, text string, err error) {
	if id == 0 {
		return
	}
	if err != nil {
		client.send(Message{Type: MsgError, ID: id, Error: err.Error()})
		return
	}
	client.send(Message{Type: kind, ID: id, Text: text})
}

func (s *Server) broadcastSettings() {
	settings := s.store.Settings()
	s.broadcast(Message{Type: MsgSettings, Settings: &settings})
}

func (s *Server) broadcast(message Message) {
	s.mu.Lock()
	clients := make([]*pipeClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	for _, client := range clients {
		client.send(message)
	}
}

func (s *Server) log(line LogLine) {
	s.mu.Lock()
	s.logs = append(s.logs, line)
	if len(s.logs) > maxServerLogs {
		s.logs = append([]LogLine(nil), s.logs[len(s.logs)-maxServerLogs:]...)
	}
	s.mu.Unlock()
	s.writeLogFile(line)
	s.broadcast(Message{Type: MsgLog, Log: &line})
}

func (s *Server) recentLogs() []LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogLine(nil), s.logs...)
}

// writeLogFile keeps a copy outside the window: a service that fails before any window connects
// must still leave something to read.
func (s *Server) writeLogFile(line LogLine) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logFile == nil {
		if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
			return
		}
		file, err := os.OpenFile(filepath.Join(s.dataDir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		s.logFile = file
	}
	if info, err := s.logFile.Stat(); err == nil && info.Size() > 2<<20 {
		s.logFile.Truncate(0)
		s.logFile.Seek(0, 0)
	}
	fmt.Fprintf(s.logFile, "%s  %s\n", line.At.Format("2006-01-02 15:04:05"), line.Text)
}

// descriptor lets the installing user talk to the service. A private WARP key travels over this
// pipe, so other accounts are left out; without the recorded SID it falls back to the Users group.
func (s *Server) descriptor() string {
	const base = "D:(A;;GA;;;SY)(A;;GA;;;BA)"
	data, err := os.ReadFile(filepath.Join(s.dataDir, "client.sid"))
	if err == nil {
		sid := strings.TrimSpace(string(data))
		if strings.HasPrefix(sid, "S-1-") {
			return base + "(A;;GA;;;" + sid + ")"
		}
	}
	return base + "(A;;GRGW;;;AU)"
}
