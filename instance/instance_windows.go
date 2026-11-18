// Package instance keeps one copy of the app running. A second launch, say from the Start menu
// while the window sits in the tray, only wakes the first copy, which then shows its window. The
// uninstaller uses the quit event to close the window before it deletes the files.
package instance

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	showEvent = `Local\ByeByeRKN.Show`
	quitEvent = `Local\ByeByeRKN.Quit`
)

// The running copy is elevated in portable mode, while a second launch usually is not. Without
// this descriptor the events would carry a high integrity label and the plain process could not
// signal them.
const eventSDDL = "D:(A;;GA;;;WD)S:(ML;;NW;;;LW)"

// ErrRunning means another copy already owns the events; it has just been signalled to show.
var ErrRunning = errors.New("instance: already running")

// Signal wakes a running copy. It reports false when there is none.
func Signal() bool { return signal(showEvent) }

// SignalQuit asks the running window to disconnect and exit; the uninstaller waits for that.
func SignalQuit() bool { return signal(quitEvent) }

func signal(name string) bool {
	ptr, _ := windows.UTF16PtrFromString(name)
	handle, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, ptr)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	return windows.SetEvent(handle) == nil
}

// Listen claims both events for this process. The channels receive a value each time another
// launch asks to see the window or an uninstaller asks to quit.
func Listen() (show, quit <-chan struct{}, err error) {
	showHandle, created, err := create(showEvent)
	if err != nil {
		return nil, nil, err
	}
	if !created {
		windows.SetEvent(showHandle)
		windows.CloseHandle(showHandle)
		return nil, nil, ErrRunning
	}
	quitHandle, created, err := create(quitEvent)
	if err != nil {
		windows.CloseHandle(showHandle)
		return nil, nil, err
	}
	if !created {
		windows.SetEvent(showHandle)
		windows.CloseHandle(showHandle)
		windows.CloseHandle(quitHandle)
		return nil, nil, ErrRunning
	}
	return watch(showHandle), watch(quitHandle), nil
}

func create(name string) (windows.Handle, bool, error) {
	sd, err := windows.SecurityDescriptorFromString(eventSDDL)
	if err != nil {
		return 0, false, err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	ptr, _ := windows.UTF16PtrFromString(name)
	handle, err := windows.CreateEvent(&attributes, 0, 0, ptr)
	if handle == 0 {
		return 0, false, err
	}
	return handle, !errors.Is(err, windows.ERROR_ALREADY_EXISTS), nil
}

func watch(handle windows.Handle) <-chan struct{} {
	events := make(chan struct{}, 1)
	go func() {
		for {
			if result, _ := windows.WaitForSingleObject(handle, windows.INFINITE); result != windows.WAIT_OBJECT_0 {
				return
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()
	return events
}
