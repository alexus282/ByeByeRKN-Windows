package ui

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"

	"github.com/alexus282/ByeByeRKN/windows/engine"
	"github.com/alexus282/ByeByeRKN/windows/store"
	"github.com/alexus282/ByeByeRKN/windows/tray"
)

const (
	menuStatus = iota + 1
	menuToggle
	menuOpen
	menuQuit
)

// shell owns the native side: the Gio window, its hiding into the tray, and the tray icon.
type shell struct {
	ui    *UI
	ctrl  *engine.Controller
	store *store.Store
	win   *app.Window
	tray  *tray.Tray

	hwnd      atomic.Uintptr
	hidden    atomic.Bool
	minimized atomic.Bool
	quitting  atomic.Bool
	quitOnce  sync.Once

	mu        sync.Mutex
	trayPhase phase
	trayTip   string
	trayReady bool
}

// Run shows the window and the tray icon and serves them until the user quits; quitting
// disconnects and ends the process, so Run never returns. show wakes the window when another
// launch of the app asks for it.
func Run(ctrl *engine.Controller, st *store.Store, show <-chan struct{}) {
	u := newUI(ctrl, st)
	s := &shell{ui: u, ctrl: ctrl, store: st, win: new(app.Window)}
	s.win.Option(app.Title("ByeByeRKN"), app.Size(420, 780), app.MinSize(360, 560))
	u.host = host{
		invalidate: s.changed,
		saveFile: func(name, content string) (string, error) {
			return saveFileDialog(s.hwnd.Load(), name, content)
		},
		openFolder: openFolder,
	}
	ctrl.OnStatus = u.setStatus
	ctrl.OnTraffic = u.setTraffic
	ctrl.OnLog = u.appendLog

	if t, err := tray.New(tray.Options{
		Tooltip:      "ByeByeRKN",
		Icon:         trayGlyph(tray.IconSize(), phaseIdle),
		OnClick:      s.show,
		Menu:         s.menu,
		OnSelect:     s.selectMenu,
		OnEndSession: ctrl.Disconnect,
	}); err == nil {
		s.tray = t
	} else {
		u.appendLog("значок в трее недоступен: " + err.Error())
	}
	s.mu.Lock()
	s.trayReady = true
	s.mu.Unlock()
	s.changed()

	go s.loop()
	go func() {
		for range show {
			s.show()
		}
	}()
	if st.Settings().AutoConnect {
		ctrl.Connect()
	}
	app.Main()
}

func (s *shell) loop() {
	var ops op.Ops
	for {
		switch e := s.win.Event().(type) {
		case app.Win32ViewEvent:
			if e.Valid() {
				s.hwnd.Store(e.HWND)
				go styleWindow(e.HWND)
			}
		case app.ConfigEvent:
			s.minimized.Store(e.Config.Mode == app.Minimized)
		case *app.ClosingEvent:
			if s.quitting.Load() {
				break
			}
			e.Abort()
			if s.store.Settings().ExitOnClose || s.tray == nil {
				go s.quit()
			} else {
				s.hide()
			}
		case app.DestroyEvent:
			// Only a quit or the system ends the window; either way routes must go first.
			s.quit()
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			if s.hidden.Load() || s.minimized.Load() {
				// Nobody sees the window: draw nothing and ask for no animation frames.
				e.Frame(gtx.Ops)
				continue
			}
			s.ui.Layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (s *shell) hide() {
	hwnd := s.hwnd.Load()
	if hwnd == 0 {
		return
	}
	hideWindow(hwnd)
	s.hidden.Store(true)
	if settings := s.store.Settings(); !settings.TrayHintShown && s.tray != nil {
		s.tray.Notify("ByeByeRKN работает в трее", "VPN продолжает работать. Щёлкните по значку, чтобы открыть окно; выйти можно из его меню.")
		settings.TrayHintShown = true
		s.store.SetSettings(settings)
	}
}

func (s *shell) show() {
	if s.quitting.Load() {
		return
	}
	s.hidden.Store(false)
	// Raise restores a minimised window and shows a hidden one.
	s.win.Perform(system.ActionRaise)
	s.win.Invalidate()
}

// quit removes routes and DNS before the process ends, otherwise the machine is left without
// internet.
func (s *shell) quit() {
	s.quitOnce.Do(func() {
		s.quitting.Store(true)
		if hwnd := s.hwnd.Load(); hwnd != 0 {
			hideWindow(hwnd)
		}
		if s.tray != nil {
			s.tray.SetTooltip("ByeByeRKN\nОтключение…")
		}
		s.ctrl.Disconnect()
		if s.tray != nil {
			s.tray.Close()
		}
		os.Exit(0)
	})
}

// changed redraws the window and brings the tray icon in line with the state.
func (s *shell) changed() {
	s.win.Invalidate()
	s.mu.Lock()
	if !s.trayReady || s.tray == nil {
		s.mu.Unlock()
		return
	}
	snap := s.ui.snapshot()
	tip := "ByeByeRKN\n" + phaseLabel(snap)
	previous := s.trayPhase
	phaseChanged := previous != snap.phase
	tipChanged := tip != s.trayTip
	s.trayPhase, s.trayTip = snap.phase, tip
	s.mu.Unlock()

	if phaseChanged {
		s.tray.SetIcon(trayGlyph(tray.IconSize(), snap.phase))
	}
	if tipChanged {
		s.tray.SetTooltip(tip)
	}
	if !phaseChanged || !s.hidden.Load() {
		return
	}
	// Nobody looks at a hidden window: tell about the moments that matter.
	switch {
	case snap.phase == phaseConnected:
		s.tray.Notify("Подключено", serverLine(snap))
	case previous == phaseConnected && (snap.phase == phaseRetrying || snap.phase == phaseWorking || snap.phase == phaseFailed):
		s.tray.Notify("Связь пропала", "Ищу другой сервер…")
	}
}

func (s *shell) menu() []tray.MenuItem {
	snap := s.ui.snapshot()
	items := []tray.MenuItem{
		{ID: menuStatus, Text: phaseLabel(snap), Disabled: true},
		{Separator: true},
	}
	switch snap.phase {
	case phaseIdle, phaseFailed:
		items = append(items, tray.MenuItem{ID: menuToggle, Text: "Подключиться"})
	case phaseStopping:
		items = append(items, tray.MenuItem{ID: menuToggle, Text: "Отключение…", Disabled: true})
	default:
		items = append(items, tray.MenuItem{ID: menuToggle, Text: "Отключиться"})
	}
	return append(items,
		tray.MenuItem{ID: menuOpen, Text: "Открыть ByeByeRKN", Default: true},
		tray.MenuItem{Separator: true},
		tray.MenuItem{ID: menuQuit, Text: "Выход"},
	)
}

func (s *shell) selectMenu(id int) {
	switch id {
	case menuToggle:
		s.ui.toggle()
	case menuOpen:
		s.show()
	case menuQuit:
		go s.quit()
	}
}

// phaseLabel is the one-line state for the tray tooltip and menu.
func phaseLabel(snap snapshot) string {
	switch snap.phase {
	case phaseConnected:
		text := "Подключено · " + serverLine(snap)
		if snap.status.Degraded {
			text += " · с ограничениями"
		}
		return text
	case phaseWorking:
		return "Подключение…"
	case phaseRetrying:
		return "Нет связи, пробую снова…"
	case phaseFailed:
		return "Не подключено"
	case phaseStopping:
		return "Отключение…"
	}
	return "Отключено"
}

// serverLine names the server the tunnel really uses, then the approximate exit: the address is
// what a support request needs, the city only says where traffic leaves Cloudflare.
func serverLine(snap snapshot) string {
	parts := []string{}
	if snap.status.Endpoint != "" {
		parts = append(parts, snap.status.Endpoint)
	}
	parts = append(parts, maskTitle(snap.status.Mask))
	if snap.status.Exit.Colo != "" {
		parts = append(parts, "выход "+exitPlace(snap.status.Exit.Colo))
	}
	return strings.Join(parts, " · ")
}
