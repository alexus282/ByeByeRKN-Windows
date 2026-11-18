// Package tray is the notification-area icon: a status picture, a tooltip, a menu on right click,
// balloon messages, and the end-of-session signal Windows sends before logoff or shutdown. Plain
// Win32 on its own thread, so it keeps working while the window is hidden.
package tray

import (
	"errors"
	"image"
	"image/color"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procSetMenuDefaultItem     = user32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procCreateIconIndirect     = user32.NewProc("CreateIconIndirect")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap           = gdi32.NewProc("CreateBitmap")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
)

const (
	wmNull         = 0x0000
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmEndSession   = 0x0016
	wmCommand      = 0x0111
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmLButtonDbl   = 0x0203
	wmContextMenu  = 0x007B
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10
	nifShowTip = 0x80

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNoNotify    = 0x0080

	smCxSmIcon = 49
)

type notifyIconData struct {
	CbSize           uint32
	Wnd              windows.HWND
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         windows.GUID
	BalloonIcon      windows.Handle
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type msg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// MenuItem is one line of the right-click menu. ID must not be zero.
type MenuItem struct {
	ID        int
	Text      string
	Disabled  bool
	Default   bool
	Checked   bool
	Separator bool
}

type Options struct {
	Tooltip string
	Icon    image.Image
	// OnClick runs on a left click, usually to show the window.
	OnClick func()
	// Menu is asked for the items every time the menu opens, so it always reflects the state.
	Menu     func() []MenuItem
	OnSelect func(id int)
	// OnEndSession runs when Windows is logging off or shutting down; it may block for a few
	// seconds to clean up.
	OnEndSession func()
}

type Tray struct {
	opts           Options
	hwnd           windows.HWND
	taskbarCreated uint32

	mu      sync.Mutex
	icon    windows.Handle
	tooltip string
}

// current is the one tray icon of the process; the window procedure has no other way to find it.
var current *Tray

// New adds the icon and starts its message loop on a dedicated OS thread.
func New(opts Options) (*Tray, error) {
	if current != nil {
		return nil, errors.New("tray: already created")
	}
	darkMenus()
	t := &Tray{opts: opts, tooltip: opts.Tooltip}
	if opts.Icon != nil {
		icon, err := IconFromImage(opts.Icon)
		if err != nil {
			return nil, err
		}
		t.icon = icon
	}
	ready := make(chan error, 1)
	go t.loop(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Tray) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	instance, err := moduleHandle()
	if err != nil {
		ready <- err
		return
	}
	className, _ := windows.UTF16PtrFromString("ByeByeRKNTray")
	class := wndClassEx{
		WndProc:   windows.NewCallback(wndProc),
		Instance:  instance,
		ClassName: className,
	}
	class.Size = uint32(unsafe.Sizeof(class))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); r == 0 {
		ready <- err
		return
	}
	current = t
	// A hidden top-level window rather than a message-only one: only top-level windows hear
	// TaskbarCreated, which is how the icon comes back after Explorer restarts.
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		current = nil
		ready <- err
		return
	}
	t.hwnd = windows.HWND(hwnd)
	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(name)))
	t.taskbarCreated = uint32(r)
	if err := t.notify(nimAdd, nil); err != nil {
		procDestroyWindow.Call(hwnd)
		current = nil
		ready <- err
		return
	}
	ready <- nil

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	t := current
	if t == nil {
		r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return r
	}
	switch message {
	case wmTrayCallback:
		switch uint32(lParam & 0xFFFF) {
		case wmLButtonUp:
			if t.opts.OnClick != nil {
				t.opts.OnClick()
			}
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	case wmEndSession:
		if wParam != 0 && t.opts.OnEndSession != nil {
			t.opts.OnEndSession()
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	if t.taskbarCreated != 0 && message == t.taskbarCreated {
		t.notify(nimAdd, nil)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

func (t *Tray) showMenu() {
	if t.opts.Menu == nil {
		return
	}
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	for _, item := range t.opts.Menu() {
		if item.Separator {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if item.Disabled {
			flags |= mfGrayed
		}
		if item.Checked {
			flags |= mfChecked
		}
		text, _ := windows.UTF16PtrFromString(item.Text)
		procAppendMenuW.Call(menu, flags, uintptr(item.ID), uintptr(unsafe.Pointer(text)))
		if item.Default {
			procSetMenuDefaultItem.Call(menu, uintptr(item.ID), 0)
		}
	}
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without the foreground the menu would not close when the user clicks elsewhere.
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton|tpmNoNotify, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(t.hwnd), 0)
	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)
	if cmd != 0 && t.opts.OnSelect != nil {
		t.opts.OnSelect(int(cmd))
	}
}

// notify sends the icon, tooltip and, when given, a balloon to the shell.
func (t *Tray) notify(action uintptr, balloon *[2]string) error {
	t.mu.Lock()
	data := notifyIconData{
		Wnd:             t.hwnd,
		ID:              1,
		Flags:           nifMessage | nifTip | nifShowTip,
		CallbackMessage: wmTrayCallback,
		Icon:            t.icon,
	}
	data.CbSize = uint32(unsafe.Sizeof(data))
	if t.icon != 0 {
		data.Flags |= nifIcon
	}
	copyUTF16(data.Tip[:], t.tooltip)
	t.mu.Unlock()
	if balloon != nil {
		data.Flags |= nifInfo
		copyUTF16(data.InfoTitle[:], balloon[0])
		copyUTF16(data.Info[:], balloon[1])
	}
	r, _, err := procShellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&data)))
	if r == 0 {
		return err
	}
	return nil
}

func (t *Tray) SetIcon(img image.Image) error {
	icon, err := IconFromImage(img)
	if err != nil {
		return err
	}
	t.mu.Lock()
	old := t.icon
	t.icon = icon
	t.mu.Unlock()
	err = t.notify(nimModify, nil)
	if old != 0 {
		procDestroyIcon.Call(uintptr(old))
	}
	return err
}

func (t *Tray) SetTooltip(text string) error {
	t.mu.Lock()
	t.tooltip = text
	t.mu.Unlock()
	return t.notify(nimModify, nil)
}

// Notify shows a balloon, which Windows 10 and 11 turn into a toast notification.
func (t *Tray) Notify(title, text string) error {
	return t.notify(nimModify, &[2]string{title, text})
}

// Close removes the icon and stops its thread.
func (t *Tray) Close() {
	t.notify(nimDelete, nil)
	procPostMessageW.Call(uintptr(t.hwnd), wmClose, 0, 0)
}

// IconSize is the side of a small icon on the primary monitor, in pixels.
func IconSize() int {
	r, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	if r == 0 {
		return 16
	}
	return int(r)
}

// IconFromImage builds an alpha-blended HICON. The caller owns it unless it goes to SetIcon.
func IconFromImage(img image.Image) (windows.Handle, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return 0, errors.New("tray: empty image")
	}
	header := struct {
		Size          uint32
		Width, Height int32
		Planes        uint16
		BitCount      uint16
		Compression   uint32
		SizeImage     uint32
		XPels, YPels  int32
		ClrUsed       uint32
		ClrImportant  uint32
	}{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	header.Size = uint32(unsafe.Sizeof(header))

	dc, _, _ := procGetDC.Call(0)
	var bits unsafe.Pointer
	colorBitmap, _, err := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	procReleaseDC.Call(0, dc)
	if colorBitmap == 0 {
		return 0, err
	}
	defer procDeleteObject.Call(colorBitmap)
	pixels := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			i := (y*w + x) * 4
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = c.B, c.G, c.R, c.A
		}
	}
	// The mask is ignored for 32-bit icons with alpha, but CreateIconIndirect insists on one.
	maskBits := make([]byte, ((w+15)/16*2)*h)
	mask, _, err := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	if mask == 0 {
		return 0, err
	}
	defer procDeleteObject.Call(mask)
	info := struct {
		Icon     int32
		XHotspot uint32
		YHotspot uint32
		Mask     uintptr
		Color    uintptr
	}{Icon: 1, Mask: mask, Color: colorBitmap}
	icon, _, err := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		return 0, err
	}
	return windows.Handle(icon), nil
}

func syscallN(proc uintptr, args ...uintptr) {
	syscall.SyscallN(proc, args...)
}

func copyUTF16(dst []uint16, s string) {
	src, _ := windows.UTF16FromString(s)
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}

func moduleHandle() (windows.Handle, error) {
	var h windows.Handle
	err := windows.GetModuleHandleEx(0, nil, &h)
	return h, err
}

// darkMenus asks uxtheme to draw this process's menus dark, as the app is dark-only. The call is
// undocumented (ordinal 135, SetPreferredAppMode since Windows 10 1903), but Explorer, Notepad++
// and others use it; on older builds the menu simply stays light.
func darkMenus() {
	if windows.RtlGetVersion().BuildNumber < 18362 {
		return
	}
	module, err := windows.LoadLibraryEx("uxtheme.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return
	}
	setPreferredAppMode, err := windows.GetProcAddressByOrdinal(module, 135)
	if err != nil {
		return
	}
	const forceDark = 2
	syscallN(setPreferredAppMode, forceDark)
	if flush, err := windows.GetProcAddressByOrdinal(module, 136); err == nil {
		syscallN(flush)
	}
}
