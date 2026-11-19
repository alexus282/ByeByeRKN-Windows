package ui

import (
	"image"
	"image/color"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

var (
	icHome     = mustIcon(icons.ActionHome)
	icSettings = mustIcon(icons.ActionSettings)
	icLog      = mustIcon(icons.ActionReceipt)
	icChevron  = mustIcon(icons.NavigationChevronRight)
	icUnfold   = mustIcon(icons.NavigationUnfoldMore)
	icCheck    = mustIcon(icons.NavigationCheck)
	icTune     = mustIcon(icons.ImageTune)
	icCopy     = mustIcon(icons.ContentContentCopy)
	icDelete   = mustIcon(icons.ActionDelete)
)

func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return ic
}

func drawIcon(gtx C, ic *widget.Icon, size unit.Dp, c color.NRGBA) D {
	s := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(s, s))
	return ic.Layout(gtx, c)
}

// surface draws w on a rounded background and clips it to the same shape, so hover highlights of
// the first and last rows follow the card's corners.
func surface(gtx C, bg color.NRGBA, radius unit.Dp, w layout.Widget) D {
	m := op.Record(gtx.Ops)
	dims := w(gtx)
	call := m.Stop()
	rr := clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(radius))
	paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
	defer rr.Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return dims
}

// sheet is a surface with only the top corners rounded, like Amnezia's server drawer.
func sheet(gtx C, bg color.NRGBA, radius unit.Dp, w layout.Widget) D {
	m := op.Record(gtx.Ops)
	dims := w(gtx)
	call := m.Stop()
	r := gtx.Dp(radius)
	rr := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, NW: r, NE: r}
	paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
	call.Add(gtx.Ops)
	return dims
}

// pressable lays w out as a button: hand cursor and a highlight under the pointer.
func pressable(gtx C, click *widget.Clickable, hover color.NRGBA, radius unit.Dp, w layout.Widget) D {
	return click.Layout(gtx, func(gtx C) D {
		m := op.Record(gtx.Ops)
		dims := w(gtx)
		call := m.Stop()
		rect := image.Rectangle{Max: dims.Size}
		if click.Hovered() || gtx.Focused(click) {
			c := hover
			if click.Pressed() {
				c = fade(c, 1.6)
			}
			paint.FillShape(gtx.Ops, c, clip.UniformRRect(rect, gtx.Dp(radius)).Op(gtx.Ops))
		}
		call.Add(gtx.Ops)
		defer clip.Rect(rect).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return dims
	})
}

// constrainWidth centres content no wider than max, so a maximised window doesn't stretch rows
// across the whole screen.
func constrainWidth(gtx C, max unit.Dp, w layout.Widget) D {
	limit := gtx.Dp(max)
	if gtx.Constraints.Max.X <= limit {
		return w(gtx)
	}
	full := gtx.Constraints.Max.X
	off := (full - limit) / 2
	gtx.Constraints.Max.X = limit
	if gtx.Constraints.Min.X > limit {
		gtx.Constraints.Min.X = limit
	}
	defer op.Offset(image.Pt(off, 0)).Push(gtx.Ops).Pop()
	dims := w(gtx)
	dims.Size.X = full
	return dims
}

func sectionTitle(th *material.Theme, txt string) layout.Widget {
	return func(gtx C) D {
		return layout.Inset{Left: 8, Top: 28, Bottom: 10}.Layout(gtx, semibold(label(th, 14, brand, txt)).Layout)
	}
}

func pageTitle(th *material.Theme, txt string) layout.Widget {
	return func(gtx C) D {
		return layout.Inset{Left: 8, Top: 22, Bottom: 2}.Layout(gtx, bold(label(th, 28, textPrimary, txt)).Layout)
	}
}

func divider(gtx C) D {
	return layout.Inset{Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		size := image.Pt(gtx.Constraints.Max.X, max(1, gtx.Dp(0.75)))
		paint.FillShape(gtx.Ops, bgSheet, clip.Rect{Max: size}.Op())
		return D{Size: size}
	})
}

type trailing int

const (
	trailNone trailing = iota
	trailArrow
	trailValue
	trailSwitch
)

// row is one settings line in Miuix style: title, optional summary, and an arrow, a value or a
// switch on the right.
type row struct {
	title, summary string
	titleColor     color.NRGBA
	trail          trailing
	value          string
	on             bool
}

func (r row) layout(gtx C, th *material.Theme, click *widget.Clickable, anim *tween) D {
	content := func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: 20, Right: 16, Top: 16, Bottom: 16}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					titleColor := r.titleColor
					if titleColor == (color.NRGBA{}) {
						titleColor = textPrimary
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(label(th, 16, titleColor, r.title).Layout),
						layout.Rigid(func(gtx C) D {
							if r.summary == "" {
								return D{}
							}
							return layout.Inset{Top: 3}.Layout(gtx, label(th, 13.5, textTertiary, r.summary).Layout)
						}),
					)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Left: 12}.Layout(gtx, func(gtx C) D {
						switch r.trail {
						case trailArrow:
							return drawIcon(gtx, icChevron, 22, textQuaternary)
						case trailValue:
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(label(th, 14, textTertiary, r.value).Layout),
								layout.Rigid(layout.Spacer{Width: 4}.Layout),
								layout.Rigid(func(gtx C) D { return drawIcon(gtx, icUnfold, 20, textQuaternary) }),
							)
						case trailSwitch:
							return toggleSwitch(gtx, r.on, anim)
						}
						return D{}
					})
				}),
			)
		})
	}
	if click == nil {
		return content(gtx)
	}
	return pressable(gtx, click, fade(bgSheet, 0.55), 0, content)
}

// toggleSwitch draws a Miuix-like switch whose thumb slides with the animation in anim.
func toggleSwitch(gtx C, on bool, anim *tween) D {
	target := float32(0)
	if on {
		target = 1
	}
	anim.target(target, gtx.Now, 180*time.Millisecond)
	t := anim.value(gtx.Now)
	if anim.active(gtx.Now) {
		gtx.Execute(op.InvalidateCmd{})
	}
	w, h := gtx.Dp(48), gtx.Dp(28)
	track := image.Rectangle{Max: image.Pt(w, h)}
	paint.FillShape(gtx.Ops, mix(bgPanel, brand, t), clip.UniformRRect(track, h/2).Op(gtx.Ops))
	pad := gtx.Dp(4)
	d := h - 2*pad
	x := pad + int(float32(w-2*pad-d)*t+0.5)
	thumb := image.Rect(x, pad, x+d, pad+d)
	paint.FillShape(gtx.Ops, mix(textSecondary, rgb(0xFFFFFF), t), clip.Ellipse(thumb).Op(gtx.Ops))
	return D{Size: track.Max}
}

// chip is a small rounded button, as on the Android home screen.
func chip(gtx C, th *material.Theme, click *widget.Clickable, ic *widget.Icon, txt string) D {
	return pressable(gtx, click, fade(bgSheet, 0.7), 12, func(gtx C) D {
		return surface(gtx, bgSurface, 12, func(gtx C) D {
			return layout.Inset{Left: 14, Right: 14, Top: 9, Bottom: 9}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return drawIcon(gtx, ic, 18, textTertiary) }),
					layout.Rigid(layout.Spacer{Width: 8}.Layout),
					layout.Rigid(func(gtx C) D {
						l := label(th, 14, textSecondary, txt)
						l.MaxLines = 1
						return l.Layout(gtx)
					}),
				)
			})
		})
	})
}

// button is a dialog button: the confirming one in the brand colour, the other one neutral.
func button(gtx C, th *material.Theme, click *widget.Clickable, txt string, primary, destructive bool) D {
	bg, fg := bgPanel, textPrimary
	switch {
	case destructive:
		bg, fg = fade(danger, 0.18), danger
	case primary:
		bg, fg = brand, onBrand
	}
	if click.Hovered() {
		bg = mix(bg, rgb(0xFFFFFF), 0.08)
	}
	if click.Pressed() {
		bg = mix(bg, rgb(0x000000), 0.12)
	}
	return click.Layout(gtx, func(gtx C) D {
		size := image.Pt(gtx.Constraints.Min.X, gtx.Dp(46))
		if size.X == 0 {
			size.X = gtx.Dp(120)
		}
		rect := image.Rectangle{Max: size}
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, gtx.Dp(14)).Op(gtx.Ops))
		if gtx.Focused(click) {
			widget.Border{Color: fade(textPrimary, 0.6), CornerRadius: 14, Width: 1.5}.Layout(gtx, func(gtx C) D { return D{Size: size} })
		}
		gtx.Constraints = layout.Exact(size)
		layout.Center.Layout(gtx, semibold(label(th, 15, fg, txt)).Layout)
		defer clip.Rect(rect).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return D{Size: size}
	})
}

// field is a text input on a darker plate; the border lights up in the brand colour or in red
// when the text is not valid.
func field(gtx C, th *material.Theme, ed *widget.Editor, hint string, invalid bool, mono bool) D {
	border := color.NRGBA{}
	switch {
	case invalid:
		border = danger
	case gtx.Focused(ed):
		border = brand
	}
	return surface(gtx, bgSheet, 14, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return widget.Border{Color: border, CornerRadius: 14, Width: 1.5}.Layout(gtx, func(gtx C) D {
			return layout.Inset{Left: 16, Right: 16, Top: 13, Bottom: 13}.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				e := material.Editor(th, ed, hint)
				e.Color = textPrimary
				e.HintColor = textQuaternary
				e.SelectionColor = fade(brand, 0.35)
				e.TextSize = 15
				if mono {
					e.Font = font.Font{Typeface: monoFace}
				}
				return e.Layout(gtx)
			})
		})
	})
}

// arc strokes part of a circle. Angles are in degrees, clockwise from three o'clock as on Android.
// Built from short segments so the end points are exact and can get round caps.
//
// The band is filled as an outline rather than stroked: Gio's stroker left faint vertical streaks
// inside the arc.
func arc(ops *op.Ops, center f32.Point, radius, startDeg, sweepDeg, width float32, c color.NRGBA) {
	full := sweepDeg >= 359.5
	if full {
		sweepDeg = 360
	}
	steps := int(sweepDeg/2) + 2
	outer, inner := radius+width/2, radius-width/2
	point := func(i int, r float32) f32.Point {
		a := rad(startDeg + sweepDeg*float32(i)/float32(steps))
		return center.Add(f32.Pt(cos(a)*r, sin(a)*r))
	}
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(point(0, outer))
	for i := 1; i <= steps; i++ {
		p.LineTo(point(i, outer))
	}
	if full {
		// A separate inner contour in the opposite direction leaves the hole.
		p.Close()
		p.MoveTo(point(steps, inner))
	}
	for i := steps; i >= 0; i-- {
		p.LineTo(point(i, inner))
	}
	p.Close()
	paint.FillShape(ops, c, clip.Outline{Path: p.End()}.Op())
	if full {
		return
	}
	for _, deg := range []float32{startDeg, startDeg + sweepDeg} {
		a := rad(deg)
		pt := center.Add(f32.Pt(cos(a)*radius, sin(a)*radius))
		disc(ops, pt, width/2, c)
	}
}

func disc(ops *op.Ops, center f32.Point, radius float32, c color.NRGBA) {
	r := image.Rect(int(center.X-radius+0.5), int(center.Y-radius+0.5), int(center.X+radius+0.5), int(center.Y+radius+0.5))
	paint.FillShape(ops, c, clip.Ellipse(r).Op(ops))
}

type glowKey struct {
	size          int
	radius, width float32
	c             color.NRGBA
	strength      uint8
}

// glowImages caches halos: colour and strength only change during short animations.
var glowImages = map[glowKey]paint.ImageOp{}

// glow paints a soft halo on both sides of a ring of the given radius, fading out over width.
// Gio has no radial gradient, so the halo is a precomputed image. Strength is baked into its
// alpha: an opacity layer left faint vertical streaks on the dark background.
func glow(ops *op.Ops, center f32.Point, radius, width float32, c color.NRGBA, strength float32) {
	size := int(2*(radius+width)) + 6
	key := glowKey{size, radius, width, c, uint8(clamp01(strength/1.3)*48 + 0.5)}
	img, ok := glowImages[key]
	if !ok {
		if len(glowImages) > 96 {
			clear(glowImages)
		}
		peak := 0.22 * float32(key.strength) / 48 * 1.3
		img = paint.NewImageOp(glowImage(size, radius, width, c, peak))
		glowImages[key] = img
	}
	half := float32(size) / 2
	defer op.Offset(image.Pt(int(center.X-half+0.5), int(center.Y-half+0.5))).Push(ops).Pop()
	// PaintOp fills the whole clip, and the image's edge pixels would smear outside it.
	defer clip.Rect{Max: image.Pt(size, size)}.Push(ops).Pop()
	img.Add(ops)
	paint.PaintOp{}.Add(ops)
}

func glowImage(size int, radius, width float32, c color.NRGBA, peak float32) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	mid := float32(size) / 2
	for y := range size {
		for x := range size {
			dx, dy := float32(x)+0.5-mid, float32(y)+0.5-mid
			d := sqrt(dx*dx+dy*dy) - radius
			if d < 0 {
				d = -d
			}
			if d >= width {
				continue
			}
			f := 1 - d/width
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = c.R, c.G, c.B
			img.Pix[i+3] = uint8(peak*f*f*255 + 0.5)
		}
	}
	return img
}
