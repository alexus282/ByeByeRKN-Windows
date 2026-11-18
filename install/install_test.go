package install

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSamePathIgnoresCaseAndRelativeParts(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "ByeByeRKN.exe")
	b := filepath.Join(dir, ".", "BYEBYERKN.EXE")
	if !samePath(a, b) {
		t.Fatalf("%q and %q are the same file on Windows", a, b)
	}
	if samePath(a, filepath.Join(dir, "other.exe")) {
		t.Fatal("different files reported as equal")
	}
}

// The shortcut is created by PowerShell, where a single quote would end the string.
func TestPowerShellPathEscaping(t *testing.T) {
	if got := quotePowerShell(`C:\O'Brien\ByeByeRKN.exe`); got != `C:\O''Brien\ByeByeRKN.exe` {
		t.Fatalf("got %q", got)
	}
}

func TestInstallPathsStayUnderProgramFilesAndProgramData(t *testing.T) {
	for name, path := range map[string]string{"exe": Exe(), "data": SettingsFile(), "shortcut": Shortcut()} {
		if !filepath.IsAbs(path) {
			t.Fatalf("%s is not absolute: %q", name, path)
		}
		if strings.ContainsAny(path, "\r\n") {
			t.Fatalf("%s has a line break: %q", name, path)
		}
	}
}
