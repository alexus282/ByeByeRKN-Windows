package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")

	procShowWindowAsync       = user32.NewProc("ShowWindowAsync")
	procLoadImageW            = user32.NewProc("LoadImageW")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetSaveFileNameW      = comdlg32.NewProc("GetSaveFileNameW")
)

const (
	swHide = 0

	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1
	imageIcon = 1

	smCxIcon   = 11
	smCxSmIcon = 49

	// rsrc numbers resources in order: the manifest is 1, the icon group 2.
	iconResource = 2

	dwmUseImmersiveDarkMode = 20
	dwmBorderColor          = 34
	dwmCaptionColor         = 35
	dwmTextColor            = 36
)

// styleWindow gives the native title bar the app's colours and icon. Windows 10 gets a dark bar,
// Windows 11 the exact background colour; older systems ignore the attributes.
//
// Gio's window thread waits while the event loop handles an event, so everything here, like every
// call into the window from Go, must be asynchronous or run on another goroutine: a SendMessage
// from the event loop would deadlock.
func styleWindow(hwnd uintptr) {
	on := int32(1)
	procDwmSetWindowAttribute.Call(hwnd, dwmUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), 4)
	caption := colorref(bgBase)
	procDwmSetWindowAttribute.Call(hwnd, dwmCaptionColor, uintptr(unsafe.Pointer(&caption)), 4)
	procDwmSetWindowAttribute.Call(hwnd, dwmBorderColor, uintptr(unsafe.Pointer(&caption)), 4)
	text := colorref(textSecondary)
	procDwmSetWindowAttribute.Call(hwnd, dwmTextColor, uintptr(unsafe.Pointer(&text)), 4)

	// Gio looks for the icon under IDs rsrc does not use, so the window would have none.
	var module windows.Handle
	if windows.GetModuleHandleEx(0, nil, &module) != nil {
		return
	}
	for _, kind := range []struct{ which, metric uintptr }{{iconBig, smCxIcon}, {iconSmall, smCxSmIcon}} {
		size, _, _ := procGetSystemMetrics.Call(kind.metric)
		icon, _, _ := procLoadImageW.Call(uintptr(module), iconResource, imageIcon, size, size, 0)
		if icon != 0 {
			procPostMessageW.Call(hwnd, wmSetIcon, kind.which, icon)
		}
	}
}

func colorref(c interface{ RGBA() (r, g, b, a uint32) }) uint32 {
	r, g, b, _ := c.RGBA()
	return (r >> 8) | (g>>8)<<8 | (b>>8)<<16
}

func hideWindow(hwnd uintptr) {
	procShowWindowAsync.Call(hwnd, swHide)
}

func openFolder(path string) {
	exec.Command("explorer.exe", path).Start()
}

type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	Reserved      uintptr
	Reserved2     uint32
	FlagsEx       uint32
}

const (
	ofnOverwritePrompt = 0x00000002
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
	ofnExplorer        = 0x00080000
)

// saveFileDialog asks where to save content with the standard Windows dialog. An empty path
// means the user cancelled.
func saveFileDialog(owner uintptr, name, content string) (string, error) {
	// The dialog runs its own message loop and wants COM on the thread that shows it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}

	buffer := make([]uint16, windows.MAX_LONG_PATH)
	initial, _ := windows.UTF16FromString(name)
	copy(buffer, initial)
	filter := utf16List("Конфиг AmneziaWG (*.conf)", "*.conf", "Все файлы", "*.*")
	ext, _ := windows.UTF16PtrFromString("conf")
	title, _ := windows.UTF16PtrFromString("Сохранить конфиг")
	var dir *uint16
	if home, err := os.UserHomeDir(); err == nil {
		dir, _ = windows.UTF16PtrFromString(filepath.Join(home, "Desktop"))
	}
	ofn := openFileName{
		Owner:      owner,
		Filter:     &filter[0],
		File:       &buffer[0],
		MaxFile:    uint32(len(buffer)),
		InitialDir: dir,
		Title:      title,
		DefExt:     ext,
		Flags:      ofnOverwritePrompt | ofnNoChangeDir | ofnPathMustExist | ofnExplorer,
	}
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	if r, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return "", nil
	}
	path := windows.UTF16ToString(buffer)
	// Notepad and the AmneziaWG client both read CRLF; it also keeps the file tidy for people.
	text := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\n", "\r\n")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// utf16List is the double-NUL-terminated string list Win32 filters use.
func utf16List(items ...string) []uint16 {
	var out []uint16
	for _, item := range items {
		s, _ := windows.UTF16FromString(item)
		out = append(out, s...)
	}
	return append(out, 0)
}
