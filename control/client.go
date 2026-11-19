package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"github.com/alexus282/ByeByeRKN-Windows/engine"
	"github.com/alexus282/ByeByeRKN-Windows/store"
)

// Client is the window's side of the pipe. It keeps the last state the service sent, so the UI
// can read settings, status and counters without blocking, and offers synchronous helpers for the
// commands that must return a value.
type Client struct {
	conn net.Conn

	mu       sync.Mutex
	enc      *json.Encoder
	nextID   int
	pending  map[int]chan Message
	closed   bool
	done     chan struct{}
	handler  func(Message)
	settings store.Settings
	path     string
	account  bool
	running  bool
	status   Status
	traffic  Traffic
	logs     []LogLine
	ready    chan struct{}
	readyOne sync.Once
}

// Dial connects to the installed service.
func Dial(timeout time.Duration) (*Client, error) {
	return DialPipe(PipeName, timeout)
}

// DialPipe is Dial for a named pipe; tests use their own name so they cannot disturb a real
// service running on the machine.
func DialPipe(path string, timeout time.Duration) (*Client, error) {
	conn, err := winio.DialPipe(path, &timeout)
	if err != nil {
		return nil, fmt.Errorf("служба ByeByeRKN не отвечает: %w", err)
	}
	c := &Client{
		conn: conn, enc: json.NewEncoder(conn), pending: make(map[int]chan Message),
		done: make(chan struct{}), ready: make(chan struct{}),
	}
	go c.read()
	select {
	case <-c.ready:
		return c, nil
	case <-c.done:
		return nil, errors.New("служба закрыла соединение при подключении")
	case <-time.After(timeout):
		c.Close()
		return nil, errors.New("служба не прислала состояние")
	}
}

func (c *Client) read() {
	decoder := json.NewDecoder(c.conn)
	for {
		var message Message
		if err := decoder.Decode(&message); err != nil {
			c.close()
			return
		}
		c.cache(message)
		if message.ID != 0 {
			c.mu.Lock()
			reply := c.pending[message.ID]
			delete(c.pending, message.ID)
			c.mu.Unlock()
			if reply != nil {
				reply <- message
			}
			continue
		}
		c.mu.Lock()
		handler := c.handler
		c.mu.Unlock()
		if handler != nil {
			handler(message)
		}
	}
}

func (c *Client) cache(message Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch message.Type {
	case MsgHello:
		if message.Settings != nil {
			c.settings = *message.Settings
		}
		c.path = message.Path
		c.account = message.HasAccount
		c.running = message.Running
		if message.Status != nil {
			c.status = *message.Status
		}
		if message.Traffic != nil {
			c.traffic = *message.Traffic
		}
		c.logs = append([]LogLine(nil), message.Logs...)
		c.readyOne.Do(func() { close(c.ready) })
	case MsgStatus:
		if message.Status != nil {
			c.status = *message.Status
		}
		c.running = message.Running
	case MsgTraffic:
		if message.Traffic != nil {
			c.traffic = *message.Traffic
		}
	case MsgSettings:
		if message.Settings != nil {
			c.settings = *message.Settings
		}
	case MsgAccount:
		c.account = message.HasAccount
	case MsgLog:
		if message.Log != nil {
			c.logs = append(c.logs, *message.Log)
			if len(c.logs) > maxClientLogs {
				c.logs = append([]LogLine(nil), c.logs[len(c.logs)-maxClientLogs:]...)
			}
		}
	}
}

const maxClientLogs = 1000

func (c *Client) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	c.mu.Unlock()
	c.conn.Close()
}

// Close ends this window's connection; the service and the tunnel keep running.
func (c *Client) Close() { c.close() }

// Done is closed when the service goes away or the pipe breaks, so the window can reconnect.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) SetHandler(handler func(Message)) {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

func (c *Client) Settings() store.Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

func (c *Client) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

func (c *Client) HasAccount() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.account
}

func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *Client) Status() engine.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status.Engine()
}

func (c *Client) Traffic() engine.Traffic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.traffic.Engine()
}

func (c *Client) Logs() []LogLine {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LogLine(nil), c.logs...)
}

func (c *Client) send(request Request) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("соединение со службой закрыто")
	}
	err := c.enc.Encode(request)
	c.mu.Unlock()
	if err != nil {
		c.close()
	}
	return err
}

// request sends a command and waits for its reply.
func (c *Client) request(cmd string, timeout time.Duration, fill func(*Request)) (Message, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Message{}, errors.New("соединение со службой закрыто")
	}
	c.nextID++
	id := c.nextID
	reply := make(chan Message, 1)
	c.pending[id] = reply
	c.mu.Unlock()

	request := Request{ID: id, Cmd: cmd}
	if fill != nil {
		fill(&request)
	}
	if err := c.send(request); err != nil {
		c.drop(id)
		return Message{}, err
	}
	select {
	case message := <-reply:
		if message.Type == MsgError {
			return message, errors.New(message.Error)
		}
		return message, nil
	case <-c.done:
		c.drop(id)
		return Message{}, errors.New("служба ByeByeRKN остановилась")
	case <-time.After(timeout):
		c.drop(id)
		return Message{}, errors.New("служба не ответила вовремя")
	}
}

func (c *Client) drop(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) Connect() { c.send(Request{Cmd: CmdConnect}) }

// Disconnect asks for shutdown without waiting; the UI learns about progress from status events.
func (c *Client) Disconnect() { c.send(Request{Cmd: CmdDisconnect}) }

// DisconnectWait is for quitting: the window must not disappear while its routes still exist.
func (c *Client) DisconnectWait(timeout time.Duration) {
	c.request(CmdDisconnect, timeout, nil)
}

func (c *Client) SetSettings(settings store.Settings) error {
	c.mu.Lock()
	c.settings = settings
	c.mu.Unlock()
	return c.send(Request{Cmd: CmdSettings, Settings: &settings})
}

func (c *Client) ExportConfig() (string, error) {
	message, err := c.request(CmdExport, 10*time.Second, nil)
	if err != nil {
		return "", err
	}
	if message.Text == "" {
		return "", errors.New("служба вернула пустой конфиг")
	}
	return message.Text, nil
}

func (c *Client) ApplyWarpPlus(key string) (string, error) {
	message, err := c.request(CmdWarpPlus, 40*time.Second, func(r *Request) { r.Key = key })
	if err != nil {
		return "", err
	}
	if message.Text == "" {
		return "Ключ принят", nil
	}
	return message.Text, nil
}

func (c *Client) ForgetServer() error {
	_, err := c.request(CmdForget, 10*time.Second, nil)
	return err
}

func (c *Client) ResetAccount() error {
	_, err := c.request(CmdReset, 40*time.Second, nil)
	return err
}

func (c *Client) Ping() error {
	_, err := c.request(CmdPing, 3*time.Second, nil)
	return err
}
