// Setup.exe is the one-file installer: it carries the program and wintun.dll, unpacks them into a
// temporary folder and runs the app's own --install. The manifest asks for elevation once, here.
package main

import (
	"embed"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/alexus282/ByeByeRKN/windows/install"
)

//go:embed payload
var payload embed.FS

func main() {
	dir, err := os.MkdirTemp("", "ByeByeRKN-setup-")
	if err != nil {
		errorMessage("Не удалось создать временную папку: " + err.Error())
		return
	}
	defer os.RemoveAll(dir)
	if err := extract(dir); err != nil {
		errorMessage("Установщик повреждён: " + err.Error())
		return
	}
	exe := filepath.Join(dir, "ByeByeRKN.exe")
	cmd := exec.Command(exe, "--install", "--quiet")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		text := err.Error()
		if len(output) > 0 {
			text += "\n\n" + string(output)
		}
		errorMessage("Установка не удалась:\n\n" + text)
		return
	}
	if message("ByeByeRKN установлен.\r\n\r\nЗапустить сейчас?", windows.MB_YESNO|windows.MB_ICONINFORMATION) == 6 {
		// Explorer gives the window the ordinary user token instead of the installer's elevation.
		exec.Command("explorer.exe", install.Exe()).Start()
	}
}

func extract(dir string) error {
	return fs.WalkDir(payload, "payload", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			return nil
		}
		if name == ".keep" {
			return nil
		}
		data, err := payload.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0o755)
	})
}

func message(text string, flags uint32) int32 {
	title, _ := windows.UTF16PtrFromString("ByeByeRKN — установка")
	body, _ := windows.UTF16PtrFromString(text)
	answer, _ := windows.MessageBox(0, body, title, flags)
	return answer
}

func errorMessage(text string) {
	message(text, windows.MB_OK|windows.MB_ICONERROR)
}
