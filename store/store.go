// Package store keeps settings, the WARP device and the route that worked last time in one JSON
// file in the user's profile. It is the Windows counterpart of the Android app's preferences.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alexus282/ByeByeRKN/windows/awg"
	"github.com/alexus282/ByeByeRKN/windows/warp"
)

// Settings is the small set of choices the Windows app exposes. An empty obfuscation or endpoint
// means "choose automatically".
type Settings struct {
	Obfuscation    string `json:"obfuscation,omitempty"`
	ManualEndpoint string `json:"manualEndpoint,omitempty"`
	DNS            string `json:"dns,omitempty"`
	QuicSNI        string `json:"quicSni,omitempty"`
	WarpPlusKey    string `json:"warpPlusKey,omitempty"`
	AutoConnect    bool   `json:"autoConnect,omitempty"`
	// ExitOnClose makes the window's close button quit instead of hiding to the tray.
	ExitOnClose bool `json:"exitOnClose,omitempty"`
	// TrayHintShown remembers that the user was told the app keeps running in the tray.
	TrayHintShown bool `json:"trayHintShown,omitempty"`
}

// Candidate is an endpoint with the exact obfuscation parameters that were generated for it.
type Candidate struct {
	Endpoint string     `json:"endpoint"`
	Params   awg.Params `json:"params"`
	RTTMs    int64      `json:"rttMs"`
	MTU      int        `json:"mtu"`
}

// Matches tells whether a remembered route still fits the user's settings.
func (c Candidate) Matches(s Settings) bool {
	if s.ManualEndpoint != "" && c.Endpoint != s.ManualEndpoint {
		return false
	}
	if s.Obfuscation != "" {
		mask, ok := awg.ParseMask(s.Obfuscation)
		if !ok || c.Params.Mask != mask {
			return false
		}
	}
	return true
}

// Data is the on-disk document.
type Data struct {
	Settings       Settings          `json:"settings"`
	Account        *warp.WarpAccount `json:"account,omitempty"`
	LastGood       *Candidate        `json:"lastGood,omitempty"`
	LastRegisterAt time.Time         `json:"lastRegisterAt,omitempty"`
}

type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

// Open loads path. A missing file is a fresh start; a malformed one is an error rather than a
// silent reset of the user's WARP device.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		return nil, err
	}
	return s, nil
}

// DefaultPath is %AppData%\ByeByeRKN\settings.json, the portable, per-user location.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ByeByeRKN", "settings.json"), nil
}

// MachinePath is %ProgramData%\ByeByeRKN\settings.json. The service owns one tunnel for the
// machine, so its account and choices cannot live in a single user's profile.
func MachinePath() (string, error) {
	dir := os.Getenv("ProgramData")
	if dir == "" {
		return "", errors.New("ProgramData is not set")
	}
	return filepath.Join(dir, "ByeByeRKN", "settings.json"), nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Settings
}

func (s *Store) SetSettings(settings Settings) error {
	s.mu.Lock()
	s.data.Settings = settings
	return s.saveLocked()
}

func (s *Store) Account() *warp.WarpAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Account
}

func (s *Store) SetAccount(account *warp.WarpAccount) error {
	s.mu.Lock()
	s.data.Account = account
	return s.saveLocked()
}

func (s *Store) LastGood() *Candidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastGood
}

func (s *Store) SetLastGood(candidate *Candidate) error {
	s.mu.Lock()
	s.data.LastGood = candidate
	return s.saveLocked()
}

func (s *Store) LastRegisterAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastRegisterAt
}

func (s *Store) SetLastRegisterAt(at time.Time) error {
	s.mu.Lock()
	s.data.LastRegisterAt = at
	return s.saveLocked()
}

// ForgetServer forgets measured routes, so the next connection searches from scratch.
func (s *Store) ForgetServer() error {
	s.mu.Lock()
	s.data.LastGood = nil
	return s.saveLocked()
}

// ResetAccount removes the WARP device too. The next connection registers a fresh one.
func (s *Store) ResetAccount() error {
	s.mu.Lock()
	s.data.Account = nil
	s.data.LastGood = nil
	s.data.LastRegisterAt = time.Time{}
	return s.saveLocked()
}

// saveLocked writes via a temporary file so a crash cannot leave half a JSON document.
func (s *Store) saveLocked() error {
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// DNSServerLists are the same resolver presets as on Android, in display order.
var DNSServerLists = map[string][]string{
	"CLOUDFLARE": {"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"},
	"GOOGLE":     {"8.8.8.8", "8.8.4.4", "2001:4860:4860::8888", "2001:4860:4860::8844"},
	"ADGUARD":    {"94.140.14.14", "94.140.15.15", "2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff"},
	"QUAD9":      {"9.9.9.9", "149.112.112.112", "2620:fe::fe", "2620:fe::9"},
}

// DNSServers returns the selected preset, defaulting to Cloudflare.
func DNSServers(name string) []string {
	if servers := DNSServerLists[name]; len(servers) > 0 {
		return servers
	}
	return DNSServerLists["CLOUDFLARE"]
}
