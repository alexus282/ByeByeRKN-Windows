// Package install puts the app and its service on the machine and takes them off again. The
// window needs no administrator rights afterwards: only the service touches the adapter.
package install

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// DefaultDir is %ProgramFiles%\ByeByeRKN.
func DefaultDir() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "ByeByeRKN")
}

// ExeIn is the program inside an installation directory.
func ExeIn(dir string) string {
	return filepath.Join(dir, "ByeByeRKN.exe")
}

// Location is where the app was installed, or DefaultDir before the first install.
func Location() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		if dir, _, err := key.GetStringValue("InstallLocation"); err == nil && dir != "" {
			return dir
		}
	}
	return DefaultDir()
}

// DataDir holds the service's settings and logs: one WARP account per machine.
func DataDir() string {
	return filepath.Join(os.Getenv("ProgramData"), "ByeByeRKN")
}

// SettingsFile is the service's settings document.
func SettingsFile() string {
	return filepath.Join(DataDir(), "settings.json")
}

// StartMenuShortcut is the entry every user sees in the start menu.
func StartMenuShortcut() string {
	return filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs", "ByeByeRKN.lnk")
}

// DesktopShortcut is on the public desktop, so it exists for every account.
func DesktopShortcut() string {
	dir := os.Getenv("PUBLIC")
	if dir == "" {
		dir = `C:\Users\Public`
	}
	return filepath.Join(dir, "Desktop", "ByeByeRKN.lnk")
}

// AutostartShortcut starts the window when the user signs in.
func AutostartShortcut() string {
	return filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "ByeByeRKN.lnk")
}

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ByeByeRKN`
