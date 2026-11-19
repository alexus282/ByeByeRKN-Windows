package main

import (
	"image/color"
	"os"
	"path/filepath"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// Same palette as the app, so the installer does not look like a foreign program.
var (
	bgBase        = rgb(0x101012)
	bgSurfaceAlt  = rgb(0x1F1F23)
	bgSheet       = rgb(0x313136)
	bgPanel       = rgb(0x3F3F46)
	textPrimary   = rgb(0xFAFAFA)
	textSecondary = rgb(0xE4E4E7)
	textTertiary  = rgb(0xA1A1AA)
	textMuted     = rgb(0x86868F)
	brand         = rgb(0x00D5FE)
	onBrand       = rgb(0x00191F)
	danger        = rgb(0xF87171)
)

const uiFace font.Typeface = "Segoe UI"

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

func fade(c color.NRGBA, alpha float32) color.NRGBA {
	c.A = uint8(float32(c.A)*alpha + 0.5)
	return c
}

func newTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(loadFonts()))
	th.Face = uiFace
	th.Palette = material.Palette{Bg: bgBase, Fg: textPrimary, ContrastBg: brand, ContrastFg: onBrand}
	th.TextSize = 15
	return th
}

func loadFonts() []font.FontFace {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	files := []struct {
		name   string
		weight font.Weight
	}{{"segoeui.ttf", font.Normal}, {"seguisb.ttf", font.SemiBold}, {"segoeuib.ttf", font.Bold}}
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
			face.Font.Typeface = uiFace
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
