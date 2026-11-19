// ByeByeRKN for Windows: a Cloudflare WARP client on AmneziaWG, the port of the Android app.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/alexus282/ByeByeRKN-Windows/catalog"
	"github.com/alexus282/ByeByeRKN-Windows/engine"
	"github.com/alexus282/ByeByeRKN-Windows/install"
	"github.com/alexus282/ByeByeRKN-Windows/instance"
	"github.com/alexus282/ByeByeRKN-Windows/store"
	"github.com/alexus282/ByeByeRKN-Windows/ui"
	"github.com/alexus282/ByeByeRKN-Windows/winsvc"
)

func main() {
	quiet := hasArg("--quiet")
	switch {
	case hasArg("--service"):
		// The SCM or a developer console owns this process; the window talks to it over the pipe.
		// A message box from session 0 would never be seen, so failures go to the exit code and
		// to service-error.log inside winsvc.
		if err := winsvc.Run(); err != nil {
			os.Exit(1)
		}
	case hasArg("--install"):
		installMode(quiet)
	case hasArg("--uninstall"):
		uninstallMode(quiet)
	default:
		runWindow()
	}
}

// runWindow shows the window. With the service installed it needs no rights at all; the portable
// fallback keeps the old behaviour for a folder someone runs without installing.
func runWindow() {
	// A copy already runs, perhaps hidden in the tray: show it instead of asking for UAC again.
	if instance.Signal() {
		return
	}
	show, quit, err := instance.Listen()
	if err != nil {
		return
	}

	if backend, err := ui.DialRemote(2 * time.Second); err == nil {
		ui.Run(backend, show, quit)
		return
	}

	if windows.GetCurrentProcessToken().IsElevated() {
		runPortable(show, quit)
		return
	}

	answer := message(
		"Служба ByeByeRKN не запущена.\n\n"+
			"Установить её? Тогда приложение будет запускаться без прав администратора, а VPN продолжит работать при закрытом окне.\n\n"+
			"«Нет» — запустить portable-режим: приложение запросит права администратора и будет управлять туннелем само.",
		windows.MB_YESNOCANCEL|windows.MB_ICONQUESTION)
	switch answer {
	case idYes:
		if err := relaunchElevated("--install"); err != nil {
			message("Не удалось запустить установку: "+err.Error(), windows.MB_ICONERROR)
		}
	case idNo:
		if err := relaunchElevated(); err != nil {
			message("Не удалось перезапуститься: "+err.Error(), windows.MB_ICONERROR)
		}
	}
}

func runPortable(show, quit <-chan struct{}) {
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
	cache := catalog.CachePath(filepath.Dir(path))
	catalog.LoadCache(cache)
	go catalog.Refresh(context.Background(), cache)
	ctrl := engine.New(st)
	ui.Run(ui.NewLocal(ctrl, st), show, quit)
}

func installMode(quiet bool) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		if err := relaunchElevated("--install"); err != nil {
			message("Не удалось запросить права администратора: "+err.Error(), windows.MB_ICONERROR)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		message(err.Error(), windows.MB_ICONERROR)
		return
	}
	target, err := install.Install(install.Options{
		Dir:       argValue("--dir"),
		SourceDir: filepath.Dir(exe),
		Version:   ui.Version,
		StartMenu: !hasArg("--no-start-menu"),
		Desktop:   hasArg("--desktop"),
		Autostart: hasArg("--autostart"),
	})
	if err != nil {
		failure(quiet, "Установка не удалась:\n\n"+err.Error())
		return
	}
	if quiet {
		return
	}
	if message("ByeByeRKN установлен.\r\n\r\nЗапустить сейчас?", windows.MB_YESNO|windows.MB_ICONINFORMATION) == idYes {
		// Explorer starts the window with the ordinary user token, not the installer's elevation.
		exec.Command("explorer.exe", target).Start()
	}
}

func uninstallMode(quiet bool) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		if err := relaunchElevated("--uninstall"); err != nil {
			message("Не удалось запросить права администратора: "+err.Error(), windows.MB_ICONERROR)
		}
		return
	}
	if !quiet {
		if message("Удалить ByeByeRKN и службу VPN?\r\n\r\nНастройки и аккаунт WARP тоже будут удалены.",
			windows.MB_YESNO|windows.MB_ICONWARNING) != idYes {
			return
		}
	}
	if err := install.Uninstall(true); err != nil {
		failure(quiet, "Удаление не удалось:\n\n"+err.Error())
		return
	}
	if !quiet {
		message("ByeByeRKN удалён.", windows.MB_ICONINFORMATION)
	}
}

const (
	idYes = 6
	idNo  = 7
)

// failure is silent when Setup.exe drives the operation: it reports the child's exit code itself.
func failure(quiet bool, text string) {
	if quiet {
		os.Exit(1)
	}
	message(text, windows.MB_ICONERROR)
}

func message(text string, flags uint32) int32 {
	title, _ := windows.UTF16PtrFromString("ByeByeRKN")
	body, _ := windows.UTF16PtrFromString(text)
	answer, _ := windows.MessageBox(0, body, title, flags)
	return answer
}

// relaunchElevated asks UAC for the same exe as administrator, keeping any arguments.
func relaunchElevated(extra ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append(append([]string{}, os.Args[1:]...), extra...)
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	arguments, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, arguments, dir, windows.SW_SHOW)
}

func hasArg(name string) bool {
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, name) {
			return true
		}
	}
	return false
}

// argValue reads --name=value or --name value.
func argValue(name string) string {
	for i, arg := range os.Args[1:] {
		if strings.EqualFold(arg, name) && i+2 < len(os.Args) {
			return os.Args[i+2]
		}
		if len(arg) > len(name) && strings.EqualFold(arg[:len(name)], name) && arg[len(name)] == '=' {
			return arg[len(name)+1:]
		}
	}
	return ""
}
