// Setup.exe is the one-file installer: it carries the program and wintun.dll, asks where to put
// them and which shortcuts to create, then installs the Windows service. The manifest asks for
// elevation once, here.
package main

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"golang.org/x/sys/windows"

	"github.com/alexus282/ByeByeRKN-Windows/install"
)

// version is set by build.ps1 from the Android app's versionName.
var version = "0.1.0"

//go:embed payload
var payload embed.FS

var errPath = errors.New("укажите полный путь установки, например C:\\Program Files\\ByeByeRKN")

type installer struct {
	win *app.Window
	th  *material.Theme

	dir       *widget.Editor
	startMenu bool
	desktop   bool
	autostart bool
	launch    bool

	browseClick, startClick, desktopClick, autoClick, launchClick widget.Clickable
	installClick, cancelClick, closeClick, runClick               widget.Clickable

	mu         sync.Mutex
	installing bool
	done       bool
	errText    string
	resultDir  string
	launched   bool
	tempDir    string
	hwnd       uintptr
}

func main() {
	// The installer needs one UAC prompt. Asking for it here instead of in the manifest keeps
	// the exe runnable by tests and lets a cancelled prompt be reported politely.
	if !windows.GetCurrentProcessToken().IsElevated() {
		if hasArg("--elevated") {
			messageBox("Установка требует прав администратора.", windows.MB_OK|windows.MB_ICONERROR)
			return
		}
		if err := relaunchElevated(); err != nil {
			messageBox("Без прав администратора установка невозможна.", windows.MB_OK|windows.MB_ICONERROR)
		}
		return
	}

	in := &installer{th: newTheme(), startMenu: true, launch: true}
	in.dir = &widget.Editor{SingleLine: true}
	in.dir.SetText(install.Location())
	in.win = new(app.Window)
	in.win.Option(app.Title("Установка ByeByeRKN"), app.Size(640, 600), app.MinSize(560, 520))

	go in.loop()
	app.Main()
}

func (in *installer) loop() {
	var ops op.Ops
	for {
		switch event := in.win.Event().(type) {
		case app.Win32ViewEvent:
			if event.Valid() {
				in.hwnd = event.HWND
				styleWindow(in.hwnd)
			}
		case *app.ClosingEvent:
			if in.isInstalling() {
				event.Abort()
			}
		case app.DestroyEvent:
			in.mu.Lock()
			temp := in.tempDir
			in.mu.Unlock()
			if temp != "" {
				os.RemoveAll(temp)
			}
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, event)
			in.layout(gtx)
			event.Frame(gtx.Ops)
		}
	}
}

func (in *installer) isInstalling() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.installing
}

func (in *installer) state() (installing, done bool, errText, resultDir string, launched bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.installing, in.done, in.errText, in.resultDir, in.launched
}

func (in *installer) install() {
	in.mu.Lock()
	if in.installing || in.done {
		in.mu.Unlock()
		return
	}
	in.installing = true
	in.errText = ""
	in.mu.Unlock()
	in.win.Invalidate()

	dir := normalizeDir(in.dir.Text())
	startMenu, desktop, autostart, launch := in.startMenu, in.desktop, in.autostart, in.launch
	go func() {
		err := in.runInstall(dir, startMenu, desktop, autostart)
		launched := false
		if err == nil && launch {
			launched = launchApp(filepath.Join(dir, "ByeByeRKN.exe"))
		}
		in.mu.Lock()
		in.installing = false
		if err != nil {
			in.errText = err.Error()
		} else {
			in.done = true
			in.resultDir = dir
			in.launched = launched
		}
		in.mu.Unlock()
		in.win.Invalidate()
	}()
}

func (in *installer) runInstall(dir string, startMenu, desktop, autostart bool) error {
	dir = normalizeDir(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return errPath
	}
	if filepath.Dir(dir) == dir {
		return errors.New("выберите папку, а не корень диска")
	}
	source, err := in.payloadDir()
	if err != nil {
		return err
	}
	_, err = install.Install(install.Options{
		Dir:       dir,
		SourceDir: source,
		Version:   version,
		StartMenu: startMenu,
		Desktop:   desktop,
		Autostart: autostart,
	})
	return err
}

// payloadDir unpacks the embedded files once; install copies them from there.
func (in *installer) payloadDir() (string, error) {
	in.mu.Lock()
	if in.tempDir != "" {
		dir := in.tempDir
		in.mu.Unlock()
		return dir, nil
	}
	in.mu.Unlock()

	dir, err := os.MkdirTemp("", "ByeByeRKN-setup-")
	if err != nil {
		return "", err
	}
	err = fs.WalkDir(payload, "payload", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == ".keep" || entry.Name() == "placeholder.txt" {
			return nil
		}
		data, err := payload.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o755)
	})
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	in.mu.Lock()
	in.tempDir = dir
	in.mu.Unlock()
	return dir, nil
}

func launchApp(exe string) bool {
	if _, err := os.Stat(exe); err != nil {
		return false
	}
	// Explorer starts the window with the ordinary user token instead of the installer's elevation.
	if err := exec.Command("explorer.exe", exe).Start(); err != nil {
		return false
	}
	return true
}

func (in *installer) browseFolder() {
	dir := browseForFolder(in.hwnd, in.dir.Text())
	if dir != "" {
		in.dir.SetText(dir)
		in.win.Invalidate()
	}
}

func (in *installer) close() {
	if in.isInstalling() {
		return
	}
	in.win.Perform(system.ActionClose)
}

func (in *installer) runApp() {
	_, _, _, dir, _ := in.state()
	launchApp(filepath.Join(dir, "ByeByeRKN.exe"))
}

// normalizeDir makes the typed path comparable with what install.Install will use, so the done
// page and the auto-launch button point at the real folder rather than at "%ProgramFiles%\...".
func normalizeDir(path string) string {
	return filepath.Clean(expandEnv(strings.Trim(strings.TrimSpace(path), `"`)))
}

// expandEnv accepts %ProgramFiles%-style variables, which is easier to type than a full path.
func expandEnv(path string) string {
	src, err := windows.UTF16FromString(path)
	if err != nil {
		return path
	}
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size, err := windows.ExpandEnvironmentStrings(&src[0], &buffer[0], uint32(len(buffer)))
	if err != nil || size == 0 || int(size) > len(buffer) {
		return path
	}
	return windows.UTF16ToString(buffer[:])
}

func hasArg(name string) bool {
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, name) {
			return true
		}
	}
	return false
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--elevated")
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, args, dir, windows.SW_SHOW)
}

func messageBox(text string, flags uint32) {
	title, _ := windows.UTF16PtrFromString("ByeByeRKN — установка")
	body, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, body, title, flags)
}
