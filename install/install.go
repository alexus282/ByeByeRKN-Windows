package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/alexus282/ByeByeRKN/windows/instance"
	"github.com/alexus282/ByeByeRKN/windows/store"
)

// Install copies the program, creates the service and the start-menu entry. It must run elevated
// and is safe to run over an existing installation: the service is restarted with the new binary.
func Install(source, version string) (string, error) {
	target := Exe()
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return "", err
	}
	// A running window uses the exe that is about to be replaced.
	instance.SignalQuit()
	time.Sleep(3 * time.Second)
	if err := removeService(); err != nil {
		return "", fmt.Errorf("остановить старую службу: %w", err)
	}
	if !samePath(source, target) {
		if err := copyFile(source, target); err != nil {
			return "", fmt.Errorf("скопировать программу: %w", err)
		}
	}
	dll := filepath.Join(filepath.Dir(source), "wintun.dll")
	dllTarget := filepath.Join(Dir(), "wintun.dll")
	if !samePath(dll, dllTarget) {
		if err := copyFile(dll, dllTarget); err != nil {
			return "", fmt.Errorf("скопировать wintun.dll: %w", err)
		}
	}
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return "", err
	}
	// The settings hold the WARP private key: only the service and administrators may read them.
	if err := secureDataDir(); err != nil {
		return "", err
	}
	migrateSettings()
	writeClientSID()

	if err := createService(target); err != nil {
		return "", err
	}
	if err := startService(); err != nil {
		return "", err
	}
	createShortcut(target)
	writeUninstallEntry(version)
	return target, nil
}

// Uninstall closes the window, removes the service, so no route survives, then the files.
// With removeData it also forgets the WARP account and every setting.
func Uninstall(removeData bool) error {
	// The window may still be running from the folder that is about to disappear.
	instance.SignalQuit()
	time.Sleep(3 * time.Second)

	if err := removeService(); err != nil {
		return fmt.Errorf("удалить службу: %w", err)
	}
	if removeData {
		if err := os.RemoveAll(DataDir()); err != nil {
			return err
		}
	}
	os.Remove(Shortcut())
	registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
	removeInstallDir()
	return nil
}

// removeInstallDir deletes everything now, including the running uninstaller: a running image can
// be renamed but not deleted, so the exe moves to TEMP and a tiny cmd removes it after exit.
func removeInstallDir() {
	entries, _ := os.ReadDir(Dir())
	for _, entry := range entries {
		if !entry.IsDir() {
			os.Remove(filepath.Join(Dir(), entry.Name()))
		}
	}
	if os.Remove(Dir()) == nil {
		return
	}
	moved := filepath.Join(os.TempDir(), fmt.Sprintf("ByeByeRKN-uninstall-%d.exe", os.Getpid()))
	// The window was asked to quit; give it up to ten seconds to release the image.
	for range 20 {
		if err := os.Rename(Exe(), moved); err == nil {
			os.RemoveAll(Dir())
			cmd := exec.Command("cmd.exe", "/c", fmt.Sprintf("timeout /t 10 /nobreak >nul & del /q \"%s\"", moved))
			cmd.SysProcAttr = &sysprocHide
			cmd.Start()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Renaming failed too: Windows deletes the folder at the next boot.
	from, _ := windows.UTF16PtrFromString(Exe())
	windows.MoveFileEx(from, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

func samePath(a, b string) bool {
	left, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	right, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(left, right)
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	// Overwrite in place rather than rename: a running window may hold the old exe open until it
	// finishes disconnecting.
	var target *os.File
	for range 20 {
		target, err = os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

func migrateSettings() {
	user, err := store.DefaultPath()
	if err != nil {
		return
	}
	machine := SettingsFile()
	if _, err := os.Stat(machine); err == nil || !errors.Is(err, os.ErrNotExist) {
		return
	}
	data, err := os.ReadFile(user)
	if err != nil {
		return
	}
	os.WriteFile(machine, data, 0o600)
}

// writeClientSID lets exactly the installing account talk to the service; its private WARP key
// must not be readable by every user on the machine.
func writeClientSID() {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(DataDir(), "client.sid"), []byte(user.User.Sid.String()), 0o600)
}

func secureDataDir() error {
	if os.Getenv("ProgramData") == "" {
		return errors.New("ProgramData is not set")
	}
	// *SID form is language-independent: SYSTEM, Administrators.
	cmd := exec.Command("icacls.exe", DataDir(),
		"/inheritance:r",
		"/grant:r", "*S-1-5-18:(OI)(CI)F",
		"/grant:r", "*S-1-5-32-544:(OI)(CI)F")
	cmd.SysProcAttr = &sysprocHide
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("права на %s: %s", DataDir(), strings.TrimSpace(string(output)))
	}
	return nil
}

func createShortcut(target string) {
	link := Shortcut()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return
	}
	script := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Description='Cloudflare WARP на AmneziaWG';$s.Save()`,
		quotePowerShell(link), quotePowerShell(target), quotePowerShell(Dir()))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &sysprocHide
	cmd.Run()
}

func quotePowerShell(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}

func writeUninstallEntry(version string) {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.WRITE)
	if err != nil {
		return
	}
	defer key.Close()
	key.SetStringValue("DisplayName", "ByeByeRKN")
	key.SetStringValue("DisplayVersion", version)
	key.SetStringValue("Publisher", "ByeByeRKN")
	key.SetStringValue("InstallLocation", Dir())
	key.SetStringValue("DisplayIcon", Exe()+",0")
	key.SetStringValue("UninstallString", `"`+Exe()+`" --uninstall`)
	key.SetStringValue("QuietUninstallString", `"`+Exe()+`" --uninstall --quiet`)
	key.SetStringValue("URLInfoAbout", "https://github.com/alexus282/ByeByeRKN")
	key.SetDWordValue("NoModify", 1)
	key.SetDWordValue("NoRepair", 1)
}
