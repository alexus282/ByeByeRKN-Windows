// Package instance keeps one copy of the app running. A second launch, say from the Start menu
// while the window sits in the tray, only wakes the first copy, which then shows its window.
package instance

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const eventName = `Local\ByeByeRKN.Show`

// The running copy is elevated, while a second launch usually is not. Without this descriptor the
// event would carry a high integrity label and the plain process could not signal it.
const eventSDDL = "D:(A;;GA;;;WD)S:(ML;;NW;;;LW)"

// Signal wakes a running copy. It reports false when there is none.
func Signal() bool {
	name, _ := windows.UTF16PtrFromString(eventName)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h) == nil
}

// ErrRunning means another copy already owns the event; it has just been signalled.
var ErrRunning = errors.New("instance: already running")

// Listen claims the event for this process. The channel receives a value each time another
// launch asks to see the window.
func Listen() (<-chan struct{}, error) {
	sd, err := windows.SecurityDescriptorFromString(eventSDDL)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	name, _ := windows.UTF16PtrFromString(eventName)
	h, err := windows.CreateEvent(&sa, 0, 0, name)
	if h == 0 {
		return nil, err
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.SetEvent(h)
		windows.CloseHandle(h)
		return nil, ErrRunning
	}
	show := make(chan struct{}, 1)
	go func() {
		for {
			if event, _ := windows.WaitForSingleObject(h, windows.INFINITE); event != windows.WAIT_OBJECT_0 {
				return
			}
			select {
			case show <- struct{}{}:
			default:
			}
		}
	}()
	return show, nil
}
