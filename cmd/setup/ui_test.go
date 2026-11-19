package main

import (
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
)

const testScale = 1.25

func render(t *testing.T, in *installer, widthDp, heightDp int) *image.RGBA {
	t.Helper()
	size := image.Pt(int(float32(widthDp)*testScale), int(float32(heightDp)*testScale))
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skipf("no GPU for headless rendering: %v", err)
	}
	defer win.Release()
	ops := new(op.Ops)
	now := time.Now()
	for frame := range 2 {
		ops.Reset()
		gtx := layout.Context{
			Ops:         ops,
			Metric:      unit.Metric{PxPerDp: testScale, PxPerSp: testScale},
			Now:         now.Add(time.Duration(frame) * time.Second),
			Constraints: layout.Exact(size),
		}
		in.layout(gtx)
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

func save(t *testing.T, img image.Image, name string) {
	dir := os.Getenv("BBR_SCREENSHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func newTestInstaller() *installer {
	in := &installer{th: newTheme(), startMenu: true, launch: true}
	in.dir = &widget.Editor{SingleLine: true}
	in.dir.SetText(`C:\Program Files\ByeByeRKN`)
	return in
}

func TestInstallerScreens(t *testing.T) {
	in := newTestInstaller()
	save(t, render(t, in, 640, 600), "setup-form")

	in.mu.Lock()
	in.done = true
	in.resultDir = `C:\Program Files\ByeByeRKN`
	in.mu.Unlock()
	save(t, render(t, in, 640, 600), "setup-done")

	in = newTestInstaller()
	in.mu.Lock()
	in.installing = true
	in.mu.Unlock()
	save(t, render(t, in, 640, 600), "setup-installing")
}

func TestPathValidation(t *testing.T) {
	in := newTestInstaller()
	for _, dir := range []string{"relative\\folder", `C:\`, `\\server\share`} {
		if err := in.runInstall(dir, true, false, false); err == nil {
			t.Fatalf("%q must be rejected before the payload is touched", dir)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("BBR_SETUP_TEST", `D:\Apps`)
	if got := expandEnv(`%BBR_SETUP_TEST%\ByeByeRKN`); got != `D:\Apps\ByeByeRKN` {
		t.Fatalf("got %q", got)
	}
	if got := expandEnv(`C:\NoVariables`); got != `C:\NoVariables` {
		t.Fatalf("got %q", got)
	}
	if got := normalizeDir(` "%BBR_SETUP_TEST%\ByeByeRKN" `); got != `D:\Apps\ByeByeRKN` {
		t.Fatalf("normalizeDir got %q", got)
	}
}
