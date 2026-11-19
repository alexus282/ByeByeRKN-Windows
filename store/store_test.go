package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
	"github.com/alexus282/ByeByeRKN-Windows/warp"
)

func TestStoreSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ByeByeRKN", "settings.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{Obfuscation: "QUIC", ManualEndpoint: "162.159.192.1:2408", DNS: "GOOGLE", QuicSNI: "ya.ru", WarpPlusKey: "key", AutoConnect: true}
	if err := s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	params := awg.NewParams(awg.QUIC, "ya.ru")
	if err := s.SetAccount(&warp.WarpAccount{ID: "id", PrivateKey: "private", AddressV4: "172.16.0.2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastGood(&Candidate{Endpoint: "162.159.192.1:2408", Params: params, RTTMs: 42, MTU: 1200}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastRegisterAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	back, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Settings() != settings {
		t.Fatalf("settings: %+v", back.Settings())
	}
	if back.Account() == nil || back.Account().ID != "id" || back.Account().PrivateKey != "private" {
		t.Fatalf("account: %+v", back.Account())
	}
	last := back.LastGood()
	if last == nil || last.RTTMs != 42 || last.MTU != 1200 || last.Params.Mask != awg.QUIC {
		t.Fatalf("last good: %+v", last)
	}
	if !back.LastRegisterAt().Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("registration time: %v", back.LastRegisterAt())
	}
}

func TestMissingAndBrokenFiles(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || s.Account() != nil {
		t.Fatalf("a missing file must start fresh: %v", err)
	}
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a broken file must not silently reset the account")
	}
}

func TestCandidateRespectsPinnedSettings(t *testing.T) {
	c := Candidate{Endpoint: "162.159.192.1:2408", Params: awg.NewParams(awg.QUIC, "")}
	if !c.Matches(Settings{}) || c.Matches(Settings{Obfuscation: "STUN"}) || c.Matches(Settings{ManualEndpoint: "1.1.1.1:80"}) {
		t.Fatal("candidate matching is wrong")
	}
	if !c.Matches(Settings{Obfuscation: "QUIC", ManualEndpoint: c.Endpoint}) {
		t.Fatal("the exact route must match")
	}
}

func TestDNSServers(t *testing.T) {
	if got := DNSServers("GOOGLE")[0]; got != "8.8.8.8" {
		t.Fatalf("got %s", got)
	}
	if got := DNSServers(""); got[0] != "1.1.1.1" || len(got) != 4 {
		t.Fatalf("default: %v", got)
	}
}
