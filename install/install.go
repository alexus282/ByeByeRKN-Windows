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

// Options are the choices the installer offers. The zero value installs to Program Files without
// shortcuts; callers decide which ones to create.
type Options struct {
	Dir       string
	SourceDir string
	Version   string
	StartMenu bool
	Desktop   bool
	Autostart bool
}

// Install copies the program, creates the service and the shortcuts. It must run elevated and is
// safe over an existing installation: the service is restarted with the new binary.
func Install(opts Options) (string, error) {
	dir := filepath.Clean(opts.Dir)
	if opts.Dir == "" {
		dir = DefaultDir()
	}
	if opts.SourceDir == "" {
		return "", errors.New("не указана папка с файлами программы")
	}
	source := ExeIn(opts.SourceDir)
	dll := filepath.Join(opts.SourceDir, "wintun.dll")
	for _, file := range []string{source, dll} {
		if _, err := os.Stat(file); err != nil {
			return "", fmt.Errorf("нет файла %s: %w", file, err)
		}
	}
	target := ExeIn(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// A running window uses the exe that is about to be replaced; a fresh install has none and
	// must not wait.
	if instance.SignalQuit() {
		time.Sleep(3 * time.Second)
	}
	if err := removeService(); err != nil {
		return "", fmt.Errorf("остановить старую службу: %w", err)
	}
	if !samePath(source, target) {
		if err := copyFile(source, target); err != nil {
			return "", fmt.Errorf("скопировать программу: %w", err)
		}
	}
	dllTarget := filepath.Join(dir, "wintun.dll")
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
	// Reinstall with a box cleared must remove the old shortcut as well.
	removeShortcuts()
	if opts.StartMenu {
		createShortcut(StartMenuShortcut(), target)
	}
	if opts.Desktop {
		createShortcut(DesktopShortcut(), target)
	}
	if opts.Autostart {
		createShortcut(AutostartShortcut(), target)
	}
	writeUninstallEntry(opts.Version, target, dir)
	return target, nil
}

// Uninstall closes the window, removes the service, so no route survives, then the files.
// With removeData it also forgets the WARP account and every setting.
func Uninstall(removeData bool) error {
	dir := Location()
	// The window may still be running from the folder that is about to disappear.
	if instance.SignalQuit() {
		time.Sleep(3 * time.Second)
	}

	if err := removeService(); err != nil {
		return fmt.Errorf("удалить службу: %w", err)
	}
	if removeData {
		if err := os.RemoveAll(DataDir()); err != nil {
			return err
		}
	}
	removeShortcuts()
	registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
	removeInstallDir(dir)
	return nil
}

func removeShortcuts() {
	os.Remove(StartMenuShortcut())
	os.Remove(DesktopShortcut())
	os.Remove(AutostartShortcut())
}

// removeInstallDir deletes everything now, including the running uninstaller: a running image can
// be renamed but not deleted, so the exe moves to TEMP and a tiny cmd removes it after exit.
func removeInstallDir(dir string) {
	exe := ExeIn(dir)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	if os.Remove(dir) == nil {
		return
	}
	moved := filepath.Join(os.TempDir(), fmt.Sprintf("ByeByeRKN-uninstall-%d.exe", os.Getpid()))
	// The window was asked to quit; give it up to ten seconds to release the image.
	for range 20 {
		if err := os.Rename(exe, moved); err == nil {
			os.RemoveAll(dir)
			cmd := exec.Command("cmd.exe", "/c", fmt.Sprintf("timeout /t 10 /nobreak >nul & del /q \"%s\"", moved))
			cmd.SysProcAttr = &sysprocHide
			cmd.Start()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Renaming failed too: Windows deletes the folder at the next boot.
	from, _ := windows.UTF16PtrFromString(exe)
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
	// With over-the-shoulder UAC the elevated process may run as another administrator; the
	// window that follows is launched by the user at the console, so that is the SID to trust.
	sid := ""
	if session := windows.WTSGetActiveConsoleSessionId(); session != 0xFFFFFFFF {
		var token windows.Token
		if err := windows.WTSQueryUserToken(session, &token); err == nil {
			if user, err := token.GetTokenUser(); err == nil {
				sid = user.User.Sid.String()
			}
			token.Close()
		}
	}
	if sid == "" {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return
		}
		sid = user.User.Sid.String()
	}
	os.WriteFile(filepath.Join(DataDir(), "client.sid"), []byte(sid), 0o600)
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

func createShortcut(link, target string) {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return
	}
	script := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Description='Cloudflare WARP на AmneziaWG';$s.Save()`,
		quotePowerShell(link), quotePowerShell(target), quotePowerShell(filepath.Dir(target)))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &sysprocHide
	cmd.Run()
}

func quotePowerShell(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}

func writeUninstallEntry(version, exe, dir string) {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.WRITE)
	if err != nil {
		return
	}
	defer key.Close()
	key.SetStringValue("DisplayName", "ByeByeRKN")
	key.SetStringValue("DisplayVersion", version)
	key.SetStringValue("Publisher", "ByeByeRKN")
	key.SetStringValue("InstallLocation", dir)
	key.SetStringValue("DisplayIcon", exe+",0")
	key.SetStringValue("UninstallString", `"`+exe+`" --uninstall`)
	key.SetStringValue("QuietUninstallString", `"`+exe+`" --uninstall --quiet`)
	key.SetStringValue("URLInfoAbout", "https://github.com/alexus282/ByeByeRKN")
	key.SetDWordValue("NoModify", 1)
	key.SetDWordValue("NoRepair", 1)
}
