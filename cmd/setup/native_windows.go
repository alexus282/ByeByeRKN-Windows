package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	dwmapi  = windows.NewLazySystemDLL("dwmapi.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")

	procLoadImageW            = user32.NewProc("LoadImageW")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procSHBrowseForFolderW    = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW  = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree         = windows.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree")
)

const (
	wmSetIcon    = 0x0080
	iconSmall    = 0
	iconBig      = 1
	imageIcon    = 1
	smCxIcon     = 11
	smCxSmIcon   = 49
	iconResource = 1 // icon-only syso; an old manifest-bearing one put it at 2

	dwmUseImmersiveDarkMode = 20
	dwmBorderColor          = 34
	dwmCaptionColor         = 35
	dwmTextColor            = 36

	bifReturnOnlyFSDirs = 0x0001
	bifEditBox          = 0x0010
	bifNewDialogStyle   = 0x0040
)

// styleWindow gives the title bar the installer's colours and the app icon.
func styleWindow(hwnd uintptr) {
	on := int32(1)
	procDwmSetWindowAttribute.Call(hwnd, dwmUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), 4)
	caption := uint32(bgBase.R) | uint32(bgBase.G)<<8 | uint32(bgBase.B)<<16
	procDwmSetWindowAttribute.Call(hwnd, dwmCaptionColor, uintptr(unsafe.Pointer(&caption)), 4)
	procDwmSetWindowAttribute.Call(hwnd, dwmBorderColor, uintptr(unsafe.Pointer(&caption)), 4)
	text := uint32(textSecondary.R) | uint32(textSecondary.G)<<8 | uint32(textSecondary.B)<<16
	procDwmSetWindowAttribute.Call(hwnd, dwmTextColor, uintptr(unsafe.Pointer(&text)), 4)

	var module windows.Handle
	if windows.GetModuleHandleEx(0, nil, &module) != nil {
		return
	}
	for _, kind := range []struct{ which, metric uintptr }{{iconBig, smCxIcon}, {iconSmall, smCxSmIcon}} {
		size, _, _ := procGetSystemMetrics.Call(kind.metric)
		icon, _, _ := procLoadImageW.Call(uintptr(module), iconResource, imageIcon, size, size, 0)
		if icon == 0 {
			icon, _, _ = procLoadImageW.Call(uintptr(module), 2, imageIcon, size, size, 0)
		}
		if icon != 0 {
			procPostMessageW.Call(hwnd, wmSetIcon, kind.which, icon)
		}
	}
}

type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	Param       uintptr
	Image       int32
}

// browseForFolder asks for the installation directory with the standard Windows picker.
func browseForFolder(owner uintptr, initial string) string {
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}
	title, _ := windows.UTF16PtrFromString("Куда установить ByeByeRKN")
	var display [windows.MAX_LONG_PATH]uint16
	if initial != "" {
		if name, err := windows.UTF16FromString(initial); err == nil && len(name) < len(display) {
			copy(display[:], name)
		}
	}
	info := browseInfo{
		Owner:       owner,
		DisplayName: &display[0],
		Title:       title,
		Flags:       bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return ""
	}
	defer procCoTaskMemFree.Call(pidl)
	var path [windows.MAX_LONG_PATH]uint16
	if ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0]))); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(path[:])
}
