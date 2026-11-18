// Package install puts the app and its service on the machine and takes them off again. The
// window needs no administrator rights afterwards: only the service touches the adapter.
package install

import (
	"os"
	"path/filepath"
)

// Dir is %ProgramFiles%\ByeByeRKN.
func Dir() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "ByeByeRKN")
}

// Exe is the installed program.
func Exe() string {
	return filepath.Join(Dir(), "ByeByeRKN.exe")
}

// DataDir holds the service's settings and logs: one WARP account per machine.
func DataDir() string {
	return filepath.Join(os.Getenv("ProgramData"), "ByeByeRKN")
}

// SettingsFile is the service's settings document.
func SettingsFile() string {
	return filepath.Join(DataDir(), "settings.json")
}

// Shortcut is the start-menu entry, created for every user.
func Shortcut() string {
	return filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs", "ByeByeRKN.lnk")
}

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ByeByeRKN`
