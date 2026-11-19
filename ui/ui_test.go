package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
	"github.com/alexus282/ByeByeRKN-Windows/engine"
	"github.com/alexus282/ByeByeRKN-Windows/store"
)

const testScale = 1.25 // 120 DPI, a common laptop setting

func newTestUI(t *testing.T) *UI {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return newUI(NewLocal(engine.New(st), st))
}

// render draws frames of u the way the window would and returns the last one. Several frames let
// tweens settle and dialogs finish appearing.
func render(t *testing.T, u *UI, widthDp, heightDp int) *image.RGBA {
	t.Helper()
	size := image.Pt(int(float32(widthDp)*testScale), int(float32(heightDp)*testScale))
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skipf("no GPU for headless rendering: %v", err)
	}
	defer win.Release()
	now := time.Now()
	ops := new(op.Ops)
	for i := range 3 {
		ops.Reset()
		gtx := layout.Context{
			Ops:         ops,
			Metric:      unit.Metric{PxPerDp: testScale, PxPerSp: testScale},
			Now:         now.Add(time.Duration(i) * time.Second),
			Constraints: layout.Exact(size),
		}
		u.Layout(gtx)
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	return img
}

// save writes the screenshot when BBR_SCREENSHOTS names a folder, for looking at the design.
func save(t *testing.T, img image.Image, name string) {
	dir := os.Getenv("BBR_SCREENSHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func connectedStatus() engine.Status {
	return engine.Status{
		State: engine.Connected, Text: "Подключено", Endpoint: "162.159.192.7:2408", Mask: awg.QUIC,
		RTT: 48 * time.Millisecond, Since: time.Now().Add(-83 * time.Second), Verified: true,
		Exit: engine.Exit{Colo: "FRA", Country: "DE", Warp: true},
	}
}

func TestScreens(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		prepare       func(u *UI)
	}{
		{"home-idle", 420, 780, func(u *UI) {}},
		{"home-working", 420, 780, func(u *UI) {
			u.status = engine.Status{State: engine.Working, Text: "Сканирую QUIC: 12/32, ответили 3"}
		}},
		{"home-connected", 420, 780, func(u *UI) {
			u.status = connectedStatus()
			u.traffic = engine.Traffic{Rx: 18 << 20, Tx: 2<<20 + 300<<10}
		}},
		// The exit is approximate information; the card names the real server and there is no
		// warning to get wrong.
		{"home-connected-russian", 420, 780, func(u *UI) {
			u.status = connectedStatus()
			u.status.Exit = engine.Exit{Colo: "DME", Country: "RU", Warp: true}
		}},
		{"home-manual", 420, 780, func(u *UI) {
			u.status = connectedStatus()
			u.updateSettings(func(s *store.Settings) { s.Obfuscation = "STUN"; s.ManualEndpoint = "162.159.193.10:500" })
		}},
		{"home-failed", 420, 780, func(u *UI) {
			u.status = engine.Status{State: engine.Failed, Text: "не нашлось ни одного рабочего сервера WARP"}
		}},
		{"home-wide", 1100, 680, func(u *UI) {
			u.status = connectedStatus()
			u.traffic = engine.Traffic{Rx: 512 << 20, Tx: 40 << 20}
		}},
		{"settings", 420, 780, func(u *UI) { u.tab = tabSettings }},
		{"settings-advanced", 420, 1500, func(u *UI) {
			u.tab = tabSettings
			u.settings.showAdvanced = true
			u.settings.manualServer = true
			u.settings.endpoint.SetText("162.159.192.")
			u.updateSettings(func(s *store.Settings) { s.Obfuscation = "QUIC"; s.WarpPlusKey = "1a2b3c4d-5e6f7a8b-9c0d1e2f" })
		}},
		{"log", 420, 780, func(u *UI) {
			u.tab = tabLog
			for _, line := range []string{
				"последний сервер 162.159.192.1:2408 больше не отвечает",
				"подключено: 162.159.192.7:2408 · QUIC, выход FRA (DE)",
				"соединение подозрительно: рукопожатие не обновлялось 190 с",
			} {
				u.appendLog(time.Now(), line)
			}
		}},
		{"log-empty", 420, 780, func(u *UI) { u.tab = tabLog }},
		{"log-long", 420, 780, func(u *UI) {
			u.tab = tabLog
			for i := range 60 {
				u.appendLog(time.Now(), fmt.Sprintf("строка %d: Сканирую QUIC, ответили %d", i, i%7))
			}
		}},
		{"dialog-mask", 420, 780, func(u *UI) {
			u.tab = tabSettings
			options := []option{{"Авто", "Подбирается под вашу сеть"}}
			for _, mask := range awg.AutoOrder {
				options = append(options, maskNames[mask])
			}
			u.open(&modal{title: "Маскировка", options: options, selected: 1, onPick: func(int) {}})
			u.modal.opened = time.Now().Add(-time.Second)
		}},
		{"dialog-warp", 420, 780, func(u *UI) {
			u.tab = tabSettings
			u.settings.editWarpPlus(u, "1a2b3c4d")
			u.modal.opened = time.Now().Add(-time.Second)
		}},
		{"toast", 420, 780, func(u *UI) {
			u.toast("Конфиг скопирован")
			u.toastAt = time.Now().Add(-time.Second)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newTestUI(t)
			c.prepare(u)
			save(t, render(t, u, c.width, c.height), c.name)
		})
	}
}

func TestSettingsPersistImmediately(t *testing.T) {
	u := newTestUI(t)
	u.settings.manualServer = true
	u.settings.endpoint.SetText("162.159.192.1:24")
	u.settings.applyEndpoint(u)
	if got := u.be.Settings().ManualEndpoint; got != "162.159.192.1:24" {
		t.Fatalf("endpoint = %q", got)
	}
	// A half-typed address keeps the last valid one.
	u.settings.endpoint.SetText("162.159.")
	u.settings.applyEndpoint(u)
	if got := u.be.Settings().ManualEndpoint; got != "162.159.192.1:24" {
		t.Fatalf("endpoint after invalid text = %q", got)
	}
	u.settings.manualServer = false
	u.settings.applyEndpoint(u)
	if got := u.be.Settings().ManualEndpoint; got != "" {
		t.Fatalf("auto server kept %q", got)
	}
}

func TestModalValidation(t *testing.T) {
	u := newTestUI(t)
	confirmed := ""
	ed := &widget.Editor{SingleLine: true}
	ed.SetText("not-a-key")
	m := &modal{editor: ed, validate: func(v string) bool { return v == "ok" }, onConfirm: func(v string) { confirmed = v }}
	u.open(m)
	m.submit(u)
	if u.modal == nil || confirmed != "" {
		t.Fatal("invalid input closed the dialog")
	}
	ed.SetText(" ok ")
	m.submit(u)
	if u.modal != nil || confirmed != "ok" {
		t.Fatalf("valid input: modal=%v confirmed=%q", u.modal, confirmed)
	}
}

func TestPhase(t *testing.T) {
	u := newTestUI(t)
	if u.snapshot().phase != phaseIdle {
		t.Fatal("fresh UI is not idle")
	}
	u.setStatus(connectedStatus())
	if u.snapshot().phase != phaseConnected {
		t.Fatal("connected status")
	}
	u.stopping = true
	if u.snapshot().phase != phaseStopping {
		t.Fatal("stopping wins over the controller state")
	}
	u.setStatus(engine.Status{State: engine.Idle})
	if u.snapshot().phase != phaseIdle {
		t.Fatal("idle status clears stopping")
	}
}

func TestTrayGlyphs(t *testing.T) {
	phases := []phase{phaseIdle, phaseWorking, phaseConnected, phaseRetrying, phaseFailed, phaseStopping}
	for _, size := range []int{16, 20, 24, 32, 48} {
		var previous *image.RGBA
		for _, p := range phases {
			img := trayGlyph(size, p).(*image.RGBA)
			if img.Bounds().Dx() != size {
				t.Fatalf("size %d phase %d: got %v", size, p, img.Bounds())
			}
			// The badge is the point: the same artwork must look different in each state.
			if previous != nil && bytes.Equal(img.Pix, previous.Pix) {
				t.Fatalf("size %d: phase %d looks like the one before", size, p)
			}
			previous = img
		}
	}
	const cell = 64
	sheet := image.NewRGBA(image.Rect(0, 0, cell*len(phases), cell))
	for i, p := range phases {
		glyph := trayGlyph(cell, p)
		for y := range cell {
			for x := range cell {
				sheet.Set(i*cell+x, y, glyph.At(x, y))
			}
		}
	}
	save(t, sheet, "tray-glyphs")
}

func TestFormat(t *testing.T) {
	if got := formatDuration(83 * time.Second); got != "1:23" {
		t.Fatal(got)
	}
	if got := formatDuration(3725 * time.Second); got != "1:02:05" {
		t.Fatal(got)
	}
	if got := formatBytes(2048); got != "2.0 КБ" {
		t.Fatal(got)
	}
	if got := exitPlace("FRA"); got != "Франкфурт" {
		t.Fatal(got)
	}
	if got := exitPlace("XYZ"); got != "XYZ" {
		t.Fatal(got)
	}
	if got := overridesText(store.Settings{Obfuscation: "HEAVY"}); got != "Вручную: маскировка Усиленная" {
		t.Fatal(got)
	}
}
