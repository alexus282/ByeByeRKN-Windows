// ByeByeRKN for Windows: a Cloudflare WARP client on AmneziaWG, the port of the Android app.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/alexus282/ByeByeRKN/windows/engine"
	"github.com/alexus282/ByeByeRKN/windows/instance"
	"github.com/alexus282/ByeByeRKN/windows/store"
	"github.com/alexus282/ByeByeRKN/windows/ui"
)

func main() {
	// A copy already runs, perhaps hidden in the tray: show it instead of asking for UAC again.
	if instance.Signal() {
		return
	}
	path, err := store.DefaultPath()
	if err != nil {
		message(err.Error(), windows.MB_ICONERROR)
		return
	}
	st, err := store.Open(path)
	if err != nil {
		message("Не удалось прочитать настройки "+path+": "+err.Error(), windows.MB_ICONERROR)
		return
	}

	if !windows.GetCurrentProcessToken().IsElevated() {
		answer := message("Для создания VPN-адаптера и изменения маршрутов нужны права администратора.\n\nПерезапустить с повышением прав?",
			windows.MB_YESNO|windows.MB_ICONQUESTION)
		const idYes = 6
		if answer == idYes {
			if err := relaunchElevated(); err != nil {
				message("Не удалось перезапуститься: "+err.Error(), windows.MB_ICONERROR)
			}
		}
		return
	}

	show, err := instance.Listen()
	if err != nil {
		// Another copy won the race and has been asked to show itself.
		return
	}
	// Never returns: quitting from the window or the tray disconnects first, then exits.
	ui.Run(engine.New(st), st, show)
}

func message(text string, flags uint32) int32 {
	title, _ := windows.UTF16PtrFromString("ByeByeRKN")
	body, _ := windows.UTF16PtrFromString(text)
	answer, _ := windows.MessageBox(0, body, title, flags)
	return answer
}

// relaunchElevated asks UAC for the same exe with the same arguments, as administrator.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(strings.Join(os.Args[1:], " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, args, dir, windows.SW_SHOW)
}
