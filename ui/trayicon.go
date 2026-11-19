package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	xdraw "golang.org/x/image/draw"

	"github.com/alexus282/ByeByeRKN-Windows/winres"
)

// trayGlyph is the notification-area icon: the app's own artwork, scaled to the tiny taskbar
// size, with a corner badge for the tunnel state. The art alone would say nothing about whether
// the VPN is up.
func trayGlyph(size int, p phase) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if art, err := png.Decode(bytes.NewReader(winres.IconPNG)); err == nil {
		xdraw.CatmullRom.Scale(img, img.Bounds(), art, art.Bounds(), draw.Src, nil)
	} else {
		fallbackGlyph(img, p)
	}
	badgeColor, ok := map[phase]color.NRGBA{
		phaseIdle:      textTertiary,
		phaseWorking:   amber,
		phaseRetrying:  amber,
		phaseStopping:  amber,
		phaseConnected: brand,
		phaseFailed:    danger,
	}[p]
	if !ok {
		return img
	}
	s := float32(size)
	// Badge at the bottom-right corner, ringed in the background colour so it reads on any art.
	cx, cy := s-0.22*s, 0.78*s
	r := max(s*0.13, 1.4)
	fillDisc(img, cx, cy, r+s*0.07, bgBase)
	fillDisc(img, cx, cy, r, badgeColor)
	return img
}

// fallbackGlyph draws the home screen's ring for a build without the artwork.
func fallbackGlyph(img *image.RGBA, p phase) {
	s := float32(img.Bounds().Dx())
	c := s / 2
	width := max(s*0.13, 1.6)
	radius := s*0.36 - width/2
	backdrop := bgSurface
	if p == phaseConnected {
		backdrop = brandTint
	}
	fillDisc(img, c, c, s/2, backdrop)
	switch p {
	case phaseConnected:
		fillRing(img, c, c, radius, width, brand)
		fillDisc(img, c, c, max(s*0.12, 1.5), brand)
	case phaseWorking, phaseRetrying, phaseStopping:
		fillArc(img, c, c, radius, width, -90, 270, brand)
	case phaseFailed:
		fillRing(img, c, c, radius, width, danger)
	default:
		fillRing(img, c, c, radius, width, textTertiary)
	}
}

const glyphSegments = 96

func circlePath(z *rasterizer, cx, cy, r float32, reverse bool) {
	for i := 0; i <= glyphSegments; i++ {
		k := i
		if reverse {
			k = glyphSegments - i
		}
		a := rad(360 * float32(k) / glyphSegments)
		x, y := cx+cos(a)*r, cy+sin(a)*r
		if i == 0 {
			z.MoveTo(x, y)
		} else {
			z.LineTo(x, y)
		}
	}
	z.ClosePath()
}

func paintPath(img *image.RGBA, z *rasterizer, c color.NRGBA) {
	z.DrawOp = draw.Over
	z.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
}

func fillDisc(img *image.RGBA, cx, cy, r float32, c color.NRGBA) {
	z := newRasterizer(img)
	circlePath(z, cx, cy, r, false)
	paintPath(img, z, c)
}

func fillRing(img *image.RGBA, cx, cy, r, width float32, c color.NRGBA) {
	z := newRasterizer(img)
	// Opposite windings leave the inner circle as a hole.
	circlePath(z, cx, cy, r+width/2, false)
	circlePath(z, cx, cy, r-width/2, true)
	paintPath(img, z, c)
}

func fillArc(img *image.RGBA, cx, cy, r, width, startDeg, sweepDeg float32, c color.NRGBA) {
	z := newRasterizer(img)
	steps := glyphSegments * int(sweepDeg) / 360
	outer, inner := r+width/2, r-width/2
	for i := 0; i <= steps; i++ {
		a := rad(startDeg + sweepDeg*float32(i)/float32(steps))
		x, y := cx+cos(a)*outer, cy+sin(a)*outer
		if i == 0 {
			z.MoveTo(x, y)
		} else {
			z.LineTo(x, y)
		}
	}
	for i := steps; i >= 0; i-- {
		a := rad(startDeg + sweepDeg*float32(i)/float32(steps))
		z.LineTo(cx+cos(a)*inner, cy+sin(a)*inner)
	}
	z.ClosePath()
	paintPath(img, z, c)
	for _, deg := range []float32{startDeg, startDeg + sweepDeg} {
		a := rad(deg)
		fillDisc(img, cx+cos(a)*r, cy+sin(a)*r, width/2, c)
	}
}
