package ui

import (
	"image/color"
	"math"
	"os"
	"path/filepath"
	"time"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// The Android app's palette: AmneziaVPN-like near-black surfaces with ByeByeRKN's cyan accent.
var (
	bgBase         = rgb(0x101012)
	bgSurface      = rgb(0x18181B)
	bgSurfaceAlt   = rgb(0x1F1F23)
	bgSheet        = rgb(0x313136)
	bgPanel        = rgb(0x3F3F46)
	textPrimary    = rgb(0xFAFAFA)
	textSecondary  = rgb(0xE4E4E7)
	textTertiary   = rgb(0xA1A1AA)
	textQuaternary = rgb(0x86868F)
	brand          = rgb(0x00D5FE)
	brandTint      = rgb(0x0A2A31)
	onBrand        = rgb(0x00191F)
	danger         = rgb(0xF87171)
	amber          = rgb(0xFBBF24)
)

const (
	uiFace   font.Typeface = "Segoe UI"
	monoFace font.Typeface = "Consolas"
)

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

// fade scales the colour's opacity by alpha.
func fade(c color.NRGBA, alpha float32) color.NRGBA {
	c.A = uint8(float32(c.A)*clamp01(alpha) + 0.5)
	return c
}

func mix(a, b color.NRGBA, t float32) color.NRGBA {
	t = clamp01(t)
	lerp := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B), A: lerp(a.A, b.A)}
}

func clamp01(v float32) float32 {
	return float32(math.Max(0, math.Min(1, float64(v))))
}

func rad(deg float32) float32 { return deg * math.Pi / 180 }
func cos(a float32) float32   { return float32(math.Cos(float64(a))) }
func sin(a float32) float32   { return float32(math.Sin(float64(a))) }
func sqrt(v float32) float32  { return float32(math.Sqrt(float64(v))) }

func newTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(loadFonts()))
	th.Face = uiFace
	th.Palette = material.Palette{Bg: bgBase, Fg: textPrimary, ContrastBg: brand, ContrastFg: onBrand}
	th.TextSize = 15
	return th
}

// loadFonts takes Segoe UI and Consolas straight from the Windows font folder: scanning every
// system font would cost a second at start, and Go's own fonts look foreign on Windows. They stay
// as the fallback for a stripped-down system.
func loadFonts() []font.FontFace {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	files := []struct {
		name   string
		face   font.Typeface
		weight font.Weight
	}{
		{"segoeui.ttf", uiFace, font.Normal},
		{"seguisb.ttf", uiFace, font.SemiBold},
		{"segoeuib.ttf", uiFace, font.Bold},
		{"consola.ttf", monoFace, font.Normal},
	}
	var faces []font.FontFace
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.name))
		if err != nil {
			continue
		}
		parsed, err := opentype.ParseCollection(data)
		if err != nil {
			continue
		}
		for _, face := range parsed {
			// Each Segoe file names its own family ("Segoe UI Semibold"); one family with real
			// weights lets a label simply ask for SemiBold.
			face.Font.Typeface = file.face
			face.Font.Weight = file.weight
			face.Font.Style = font.Regular
			faces = append(faces, face)
		}
	}
	return append(faces, gofont.Collection()...)
}

func label(th *material.Theme, size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := material.Label(th, size, txt)
	l.Color = c
	return l
}

func bold(l material.LabelStyle) material.LabelStyle {
	l.Font.Weight = font.Bold
	return l
}

func semibold(l material.LabelStyle) material.LabelStyle {
	l.Font.Weight = font.SemiBold
	return l
}

func centered(l material.LabelStyle) material.LabelStyle {
	l.Alignment = text.Middle
	return l
}

// tween animates one value towards a target; changing the target midway starts from where the
// value is now, so nothing jumps.
type tween struct {
	from, to float32
	start    time.Time
	dur      time.Duration
	set      bool
}

func (t *tween) target(to float32, now time.Time, dur time.Duration) {
	if !t.set {
		*t = tween{from: to, to: to, start: now, dur: dur, set: true}
		return
	}
	if to == t.to {
		return
	}
	t.from = t.value(now)
	t.to = to
	t.start = now
	t.dur = dur
}

func (t *tween) value(now time.Time) float32 {
	if t.dur <= 0 {
		return t.to
	}
	p := float32(now.Sub(t.start)) / float32(t.dur)
	if p >= 1 {
		return t.to
	}
	if p < 0 {
		p = 0
	}
	// Ease-out cubic: fast start, soft landing, like Compose's default.
	q := 1 - p
	return t.from + (t.to-t.from)*(1-q*q*q)
}

func (t *tween) active(now time.Time) bool {
	return t.set && now.Sub(t.start) < t.dur
}
