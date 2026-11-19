package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/alexus282/ByeByeRKN-Windows/store"
)

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// The installed service restricts the pipe to the installing account; the test speaks as the
	// same account, which also proves the SID SDDL is valid.
	if user, err := windows.GetCurrentProcessToken().GetTokenUser(); err == nil {
		os.WriteFile(filepath.Join(filepath.Dir(path), "client.sid"), []byte(user.User.Sid.String()), 0o600)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(st)
	server.PipeName = fmt.Sprintf(`\\.\pipe\ByeByeRKN-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	go server.ListenAndServe()
	t.Cleanup(server.Stop)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client, err := DialPipe(server.PipeName, 200*time.Millisecond); err == nil {
			client.Close()
			return server, server.PipeName
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("service pipe did not come up")
	return nil, ""
}

// The window must see the service's state on connect and its broadcasts afterwards; this is the
// contract that replaces sharing a store file with an in-process engine.
func TestClientHelloSettingsAndBroadcast(t *testing.T) {
	_, pipe := startTestServer(t)
	client, err := DialPipe(pipe, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if client.Path() == "" {
		t.Fatal("hello did not carry the settings path")
	}
	if got := client.Settings(); got.DNS != "" && got.DNS != "CLOUDFLARE" {
		t.Fatalf("unexpected settings: %+v", got)
	}
	if err := client.Ping(); err != nil {
		t.Fatal(err)
	}

	// The service writes the document and tells every window about the change.
	settings := client.Settings()
	settings.DNS = "GOOGLE"
	settings.QuicSNI = "ya.ru"
	settings.ExitOnClose = true
	if err := client.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.Settings().DNS == "GOOGLE" && client.Settings().QuicSNI == "ya.ru" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := client.Settings(); got.DNS != "GOOGLE" || got.QuicSNI != "ya.ru" || !got.ExitOnClose {
		t.Fatalf("broadcast settings: %+v", got)
	}
}

func TestCommandsReportErrorsInsteadOfHanging(t *testing.T) {
	_, pipe := startTestServer(t)
	client, err := DialPipe(pipe, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// No WARP device was registered, so export has to fail with a readable reason.
	if _, err := client.ExportConfig(); err == nil || !strings.Contains(err.Error(), "аккаунт") {
		t.Fatalf("export error: %v", err)
	}
	if err := client.ForgetServer(); err != nil {
		t.Fatal(err)
	}
}
