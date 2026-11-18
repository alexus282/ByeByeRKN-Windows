// Package ui is the Windows window in the Android app's Amnezia-like design: a home screen with
// the glowing connect ring and the server card, settings in rounded cards, and a log. It is drawn
// with Gio, so it looks the same on every Windows version and needs nothing besides the exe.
package ui

import (
	"fmt"
	"image"
	"strings"
	"sync"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/alexus282/ByeByeRKN/windows/engine"
	"github.com/alexus282/ByeByeRKN/windows/store"
)

// Version is set by build.ps1 from the Android app's versionName.
var Version = "dev"

const (
	tabHome = iota
	tabSettings
	tabLog
)

const maxLogLines = 1000

// phase is what the home screen shows; it folds the controller's state together with the
// window's own knowledge that a disconnect is in progress.
type phase int

const (
	phaseIdle phase = iota
	phaseWorking
	phaseRetrying // failed, and the controller will try again by itself
	phaseFailed
	phaseConnected
	phaseStopping
)

type logLine struct {
	at   time.Time
	text string
}

// host is what the screens need from the native window around them.
type host struct {
	invalidate func()
	saveFile   func(name, content string) (string, error)
	openFolder func(path string)
}

type UI struct {
	th   *material.Theme
	be   Backend
	host host

	mu       sync.Mutex
	status   engine.Status
	traffic  engine.Traffic
	logs     []logLine
	pending  []func()
	stopping bool

	tab      int
	nav      [3]widget.Clickable
	home     homeScreen
	settings settingsScreen
	log      logScreen
	modal    *modal

	toastText string
	toastAt   time.Time
}

func newUI(be Backend) *UI {
	u := &UI{th: newTheme(), be: be}
	u.status = be.Status()
	u.traffic = be.Traffic()
	u.logs = be.InitialLogs()
	u.settings.init(be.Settings())
	return u
}

func (u *UI) invalidate() {
	if u.host.invalidate != nil {
		u.host.invalidate()
	}
}

// post runs f on the window's goroutine before the next frame; background work reports back
// through it.
func (u *UI) post(f func()) {
	u.mu.Lock()
	u.pending = append(u.pending, f)
	u.mu.Unlock()
	u.invalidate()
}

func (u *UI) setStatus(status engine.Status) {
	u.mu.Lock()
	u.status = status
	if status.State == engine.Idle {
		u.stopping = false
	}
	u.mu.Unlock()
	u.invalidate()
}

func (u *UI) setTraffic(traffic engine.Traffic) {
	u.mu.Lock()
	u.traffic = traffic
	u.mu.Unlock()
	u.invalidate()
}

func (u *UI) appendLog(at time.Time, line string) {
	u.mu.Lock()
	u.logs = append(u.logs, logLine{at: at, text: line})
	if len(u.logs) > maxLogLines {
		u.logs = append([]logLine(nil), u.logs[len(u.logs)-maxLogLines/2:]...)
	}
	u.mu.Unlock()
	u.invalidate()
}

// snapshot is the state one frame is drawn from.
type snapshot struct {
	status  engine.Status
	traffic engine.Traffic
	phase   phase
	logs    []logLine
}

func (u *UI) snapshot() snapshot {
	running := u.be.Running()
	u.mu.Lock()
	defer u.mu.Unlock()
	s := snapshot{status: u.status, traffic: u.traffic, logs: u.logs}
	switch {
	case u.stopping:
		s.phase = phaseStopping
	case u.status.State == engine.Connected:
		s.phase = phaseConnected
	case u.status.State == engine.Failed && running:
		s.phase = phaseRetrying
	case u.status.State == engine.Failed:
		s.phase = phaseFailed
	case u.status.State == engine.Working || running:
		s.phase = phaseWorking
	}
	return s
}

// toggle is the ring button and the tray item: connect when idle, otherwise stop whatever runs.
func (u *UI) toggle() {
	if !u.be.Running() {
		u.be.Connect()
		u.invalidate()
		return
	}
	u.mu.Lock()
	if u.stopping {
		u.mu.Unlock()
		return
	}
	u.stopping = true
	u.mu.Unlock()
	u.invalidate()
	// Disconnect waits for netsh to remove routes and DNS: never on the window's goroutine.
	go func() {
		u.be.DisconnectWait(30 * time.Second)
		u.mu.Lock()
		u.stopping = false
		u.mu.Unlock()
		u.invalidate()
	}()
}

func (u *UI) toast(text string) {
	u.toastText = text
	u.toastAt = time.Now()
	u.invalidate()
}

func (u *UI) updateSettings(change func(*store.Settings)) {
	settings := u.be.Settings()
	change(&settings)
	if err := u.be.SetSettings(settings); err != nil {
		u.toast("Не удалось сохранить настройки: " + err.Error())
	}
}

// Layout draws one frame.
func (u *UI) Layout(gtx C) D {
	u.mu.Lock()
	pending := u.pending
	u.pending = nil
	u.mu.Unlock()
	for _, f := range pending {
		f()
	}
	s := u.snapshot()

	paint.Fill(gtx.Ops, bgBase)
	u.handleKeys(gtx)
	dims := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min = gtx.Constraints.Max
			switch u.tab {
			case tabSettings:
				return u.settings.layout(gtx, u)
			case tabLog:
				return u.log.layout(gtx, u, s)
			default:
				return u.home.layout(gtx, u, s)
			}
		}),
		layout.Rigid(u.layoutNav),
	)
	u.layoutToast(gtx)
	if u.modal != nil {
		u.modal.layout(gtx, u)
	}
	return dims
}

func (u *UI) handleKeys(gtx C) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press && u.modal != nil {
			u.modal = nil
		}
	}
}

func (u *UI) layoutNav(gtx C) D {
	items := []struct {
		icon  *widget.Icon
		label string
	}{
		{icHome, "Главная"},
		{icSettings, "Настройки"},
		{icLog, "Журнал"},
	}
	for i := range u.nav {
		if u.nav[i].Clicked(gtx) {
			u.tab = i
		}
	}
	return layout.Inset{Left: 8, Right: 8, Top: 4, Bottom: 8}.Layout(gtx, func(gtx C) D {
		return constrainWidth(gtx, 520, func(gtx C) D {
			children := make([]layout.FlexChild, len(items))
			for i, item := range items {
				children[i] = layout.Flexed(1, func(gtx C) D {
					selected := u.tab == i
					c := textQuaternary
					if selected {
						c = brand
					}
					return layout.Inset{Left: 6, Right: 6}.Layout(gtx, func(gtx C) D {
						return pressable(gtx, &u.nav[i], bgSurfaceAlt, 14, func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Inset{Top: 8, Bottom: 6}.Layout(gtx, func(gtx C) D {
								return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx C) D { return drawIcon(gtx, item.icon, 24, c) }),
									layout.Rigid(layout.Spacer{Height: 3}.Layout),
									layout.Rigid(func(gtx C) D {
										gtx.Constraints.Min.X = gtx.Constraints.Max.X
										l := centered(label(u.th, 12, c, item.label))
										if selected {
											l = semibold(l)
										}
										return l.Layout(gtx)
									}),
								)
							})
						})
					})
				})
			}
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{}.Layout(gtx, children...)
		})
	})
}

const (
	toastFadeIn  = 150 * time.Millisecond
	toastShow    = 3200 * time.Millisecond
	toastFadeOut = 300 * time.Millisecond
)

// layoutToast shows the last message above the navigation bar, like an Android toast.
func (u *UI) layoutToast(gtx C) {
	if u.toastText == "" {
		return
	}
	age := gtx.Now.Sub(u.toastAt)
	if age >= toastShow+toastFadeOut {
		u.toastText = ""
		return
	}
	alpha := float32(1)
	switch {
	case age < toastFadeIn:
		alpha = float32(age) / float32(toastFadeIn)
		gtx.Execute(op.InvalidateCmd{})
	case age > toastShow:
		alpha = 1 - float32(age-toastShow)/float32(toastFadeOut)
		gtx.Execute(op.InvalidateCmd{})
	default:
		gtx.Execute(op.InvalidateCmd{At: u.toastAt.Add(toastShow)})
	}

	m := op.Record(gtx.Ops)
	inner := gtx
	inner.Constraints.Min = image.Point{}
	inner.Constraints.Max.X = min(gtx.Constraints.Max.X-gtx.Dp(48), gtx.Dp(460))
	dims := surface(inner, bgSheet, 14, func(gtx C) D {
		return layout.Inset{Left: 18, Right: 18, Top: 12, Bottom: 12}.Layout(gtx, label(u.th, 14, textPrimary, u.toastText).Layout)
	})
	call := m.Stop()
	x := (gtx.Constraints.Max.X - dims.Size.X) / 2
	y := gtx.Constraints.Max.Y - dims.Size.Y - gtx.Dp(92)
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	defer paint.PushOpacity(gtx.Ops, alpha).Pop()
	call.Add(gtx.Ops)
}

type option struct {
	title, summary string
}

// modal is a centred dialog in the style of Miuix's SuperDialog: a question, a list to pick
// from, or a text field.
type modal struct {
	title, text string
	confirm     string
	destructive bool

	options  []option
	selected int
	onPick   func(int)

	editor    *widget.Editor
	hint      string
	mono      bool
	validate  func(string) bool
	onConfirm func(string)

	scrim, card, cancel, ok widget.Clickable
	picks                   []widget.Clickable
	focused                 bool
	opened                  time.Time
}

func (u *UI) open(m *modal) {
	m.opened = time.Now()
	if len(m.options) > 0 {
		m.picks = make([]widget.Clickable, len(m.options))
	}
	u.modal = m
	u.invalidate()
}

func (m *modal) submit(u *UI) {
	value := ""
	if m.editor != nil {
		value = strings.TrimSpace(m.editor.Text())
		if m.validate != nil && !m.validate(value) {
			return
		}
	}
	u.modal = nil
	if m.onConfirm != nil {
		m.onConfirm(value)
	}
}

func (m *modal) layout(gtx C, u *UI) {
	if m.scrim.Clicked(gtx) {
		u.modal = nil
		return
	}
	if m.cancel.Clicked(gtx) {
		u.modal = nil
		return
	}
	if m.ok.Clicked(gtx) {
		m.submit(u)
		return
	}
	for i := range m.picks {
		if m.picks[i].Clicked(gtx) {
			u.modal = nil
			if m.onPick != nil {
				m.onPick(i)
			}
			return
		}
	}
	if m.editor != nil {
		for {
			ev, ok := m.editor.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				m.submit(u)
				return
			}
		}
		if !m.focused {
			gtx.Execute(key.FocusCmd{Tag: m.editor})
			m.editor.SetCaret(m.editor.Len(), 0)
			m.focused = true
		}
	}

	appear := clamp01(float32(gtx.Now.Sub(m.opened)) / float32(160*time.Millisecond))
	if appear < 1 {
		gtx.Execute(op.InvalidateCmd{})
	}
	size := gtx.Constraints.Max
	m.scrim.Layout(gtx, func(gtx C) D {
		paint.FillShape(gtx.Ops, fade(rgb(0x000000), 0.78*appear), clip.Rect{Max: size}.Op())
		return D{Size: size}
	})

	rec := op.Record(gtx.Ops)
	inner := gtx
	inner.Constraints.Min = image.Point{}
	inner.Constraints.Max.X = min(size.X-inner.Dp(32), inner.Dp(420))
	inner.Constraints.Max.Y = size.Y - inner.Dp(48)
	dims := m.card.Layout(inner, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return surface(gtx, bgSurfaceAlt, 28, func(gtx C) D {
			return layout.UniformInset(24).Layout(gtx, func(gtx C) D {
				return m.content(gtx, u)
			})
		})
	})
	call := rec.Stop()
	x := (size.X - dims.Size.X) / 2
	y := (size.Y-dims.Size.Y)/2 + int(float32(gtx.Dp(16))*(1-appear))
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	defer paint.PushOpacity(gtx.Ops, appear).Pop()
	call.Add(gtx.Ops)
}

func (m *modal) content(gtx C, u *UI) D {
	th := u.th
	var children []layout.FlexChild
	children = append(children, layout.Rigid(func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return centered(bold(label(th, 20, textPrimary, m.title))).Layout(gtx)
	}))
	if m.text != "" {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: 10}.Layout),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return centered(label(th, 14.5, textTertiary, m.text)).Layout(gtx)
			}))
	}
	if len(m.options) > 0 {
		children = append(children, layout.Rigid(layout.Spacer{Height: 14}.Layout))
		for i, opt := range m.options {
			children = append(children, layout.Rigid(func(gtx C) D {
				selected := i == m.selected
				return pressable(gtx, &m.picks[i], fade(bgSheet, 0.8), 14, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					bg := bgSurfaceAlt
					if selected {
						bg = brandTint
					}
					return surface(gtx, fade(bg, 0.9), 14, func(gtx C) D {
						return layout.Inset{Left: 16, Right: 12, Top: 11, Bottom: 11}.Layout(gtx, func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, func(gtx C) D {
									c := textPrimary
									if selected {
										c = brand
									}
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(label(th, 15.5, c, opt.title).Layout),
										layout.Rigid(func(gtx C) D {
											if opt.summary == "" {
												return D{}
											}
											return layout.Inset{Top: 2}.Layout(gtx, label(th, 13, textTertiary, opt.summary).Layout)
										}),
									)
								}),
								layout.Rigid(func(gtx C) D {
									if !selected {
										return D{Size: image.Pt(gtx.Dp(22), 0)}
									}
									return drawIcon(gtx, icCheck, 22, brand)
								}),
							)
						})
					})
				})
			}))
		}
	}
	if m.editor != nil {
		invalid := m.validate != nil && m.editor.Len() > 0 && !m.validate(strings.TrimSpace(m.editor.Text()))
		children = append(children,
			layout.Rigid(layout.Spacer{Height: 18}.Layout),
			layout.Rigid(func(gtx C) D { return field(gtx, th, m.editor, m.hint, invalid, m.mono) }))
	}
	if m.onConfirm != nil || len(m.options) == 0 {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: 20}.Layout),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				if m.onConfirm == nil {
					return button(gtx, th, &m.cancel, "Закрыть", true, false)
				}
				return layout.Flex{Spacing: layout.SpaceBetween}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return button(gtx, th, &m.cancel, "Отмена", false, false)
					}),
					layout.Rigid(layout.Spacer{Width: 12}.Layout),
					layout.Flexed(1, func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return button(gtx, th, &m.ok, m.confirm, true, m.destructive)
					}),
				)
			}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f КБ", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d Б", n)
	}
}

func formatDuration(d time.Duration) string {
	s := int64(d / time.Second)
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
