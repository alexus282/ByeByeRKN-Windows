package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/alexus282/ByeByeRKN/windows/awg"
	"github.com/alexus282/ByeByeRKN/windows/engine"
	"github.com/alexus282/ByeByeRKN/windows/store"
)

type homeScreen struct {
	button widget.Clickable
	chip   widget.Clickable

	color, sweep, halo, press tween
	angle                     float32
	lastSpin                  time.Time
}

func (h *homeScreen) layout(gtx C, u *UI, s snapshot) D {
	if h.chip.Clicked(gtx) {
		u.tab = tabSettings
	}
	if h.button.Clicked(gtx) {
		u.toggle()
		s = u.snapshot()
	}
	max := gtx.Constraints.Max
	wide := max.X > max.Y && max.X >= gtx.Dp(640)
	diameter := unit.Dp(190)
	if max.Y < gtx.Dp(560) {
		diameter = 150
	}
	chips := func(gtx C) D { return h.chips(gtx, u) }
	button := func(gtx C) D { return h.ring(gtx, u, s, diameter) }
	status := func(gtx C) D { return statusLine(gtx, u, s) }

	if wide {
		return layout.Inset{Left: 32, Right: 32}.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min.Y = 0
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(chips),
						layout.Rigid(layout.Spacer{Height: 36}.Layout),
						layout.Rigid(button),
						layout.Rigid(layout.Spacer{Height: 36}.Layout),
						layout.Rigid(status),
					)
				}),
				layout.Rigid(layout.Spacer{Width: 32}.Layout),
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min.Y = 0
					return constrainWidth(gtx, 520, func(gtx C) D { return serverCard(gtx, u, s, false) })
				}),
			)
		})
	}
	gtx.Constraints.Min = gtx.Constraints.Max
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Height: 16}.Layout),
		layout.Rigid(chips),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(button),
		layout.Rigid(layout.Spacer{Height: 36}.Layout),
		layout.Rigid(status),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return serverCard(gtx, u, s, true)
		}),
	)
}

// chips reminds about settings that take the choice away from the automatic search.
func (h *homeScreen) chips(gtx C, u *UI) D {
	text := overridesText(u.store.Settings())
	if text == "" {
		return D{}
	}
	return chip(gtx, u.th, &h.chip, icTune, text)
}

func overridesText(settings store.Settings) string {
	var parts []string
	if mask, ok := awg.ParseMask(settings.Obfuscation); ok && settings.Obfuscation != "" {
		parts = append(parts, "маскировка "+maskTitle(mask))
	}
	if settings.ManualEndpoint != "" {
		parts = append(parts, "сервер "+settings.ManualEndpoint)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Вручную: " + strings.Join(parts, ", ")
}

// ring is the Amnezia-style connect button: a thin ring with a soft glow, and a spinning arc while
// the app is busy.
func (h *homeScreen) ring(gtx C, u *UI, s snapshot, diameter unit.Dp) D {
	now := gtx.Now
	connected := s.phase == phaseConnected
	busy := s.phase == phaseWorking || s.phase == phaseRetrying || s.phase == phaseStopping
	hovered := h.button.Hovered()
	focused := gtx.Focused(&h.button)

	h.color.target(boolf(connected), now, 400*time.Millisecond)
	h.sweep.target(pick[float32](busy, 160, 360), now, 500*time.Millisecond)
	h.halo.target(pick[float32](connected, 1, 0.35)+pick[float32](hovered, 0.25, 0), now, 600*time.Millisecond)
	h.press.target(pick[float32](h.button.Pressed(), 0.96, 1), now, 120*time.Millisecond)
	if busy {
		if !h.lastSpin.IsZero() {
			h.angle += float32(now.Sub(h.lastSpin).Seconds()) * 360
		}
		h.lastSpin = now
	} else {
		h.lastSpin = time.Time{}
	}
	if busy || h.color.active(now) || h.sweep.active(now) || h.halo.active(now) || h.press.active(now) {
		gtx.Execute(op.InvalidateCmd{})
	}

	d := gtx.Dp(diameter)
	size := image.Pt(d, d)
	center := f32.Pt(float32(d)/2, float32(d)/2)
	scale := h.press.value(now)
	press := op.Affine(f32.AffineId().Scale(center, f32.Pt(scale, scale))).Push(gtx.Ops)
	defer press.Pop()

	// Drawn outside the clickable, whose clip would cut the halo down to a square.
	stroke := float32(gtx.Dp(pick[unit.Dp](focused, 4, 2)))
	radius := (float32(d) - stroke) / 2
	c := mix(textSecondary, brand, h.color.value(now))
	if focused && !connected {
		c = brand
	}
	if s.phase == phaseFailed {
		c = mix(c, danger, 0.6)
	}
	glow(gtx.Ops, center, radius, float32(gtx.Dp(30)), c, h.halo.value(now))
	arc(gtx.Ops, center, radius, 100+h.angle, h.sweep.value(now), stroke, c)

	return h.button.Layout(gtx, func(gtx C) D {

		text := "Подключиться"
		switch s.phase {
		case phaseConnected:
			text = "Отключиться"
		case phaseWorking:
			text = "Подключение…"
		case phaseRetrying:
			text = "Отменить"
		case phaseStopping:
			text = "Отключение…"
		}
		textColor := textPrimary
		if connected {
			textColor = brand
		}
		l := centered(bold(label(u.th, unit.Sp(20*float32(diameter)/190), textColor, text)))
		l.MaxLines = 2
		inner := gtx
		inner.Constraints = layout.Exact(size)
		layout.Center.Layout(inner, func(gtx C) D {
			gtx.Constraints.Max.X = d - gtx.Dp(28)
			gtx.Constraints.Min.X = 0
			return l.Layout(gtx)
		})

		defer clip.Ellipse(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return D{Size: size}
	})
}

func statusLine(gtx C, u *UI, s snapshot) D {
	text, c, warning := "Сервер и маскировка подберутся сами", textTertiary, ""
	switch s.phase {
	case phaseWorking:
		text = "Подключение…"
		if s.status.State == engine.Working && s.status.Text != "" {
			text = s.status.Text
		}
	case phaseRetrying:
		text, c = s.status.Text+"\nПовторю автоматически.", danger
	case phaseFailed:
		text, c = s.status.Text, danger
	case phaseStopping:
		text = "Отключаюсь и возвращаю маршруты Windows…"
	case phaseConnected:
		since := s.status.Since
		if since.IsZero() {
			since = gtx.Now
		}
		text = "Подключено · " + formatDuration(gtx.Now.Sub(since))
		if s.status.Degraded {
			warning = "Лучшего маршрута нет: Telegram может не работать"
		}
		c = brand
		// The timer ticks once a second, not every frame.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Truncate(time.Second).Add(time.Second)})
	}
	return layout.Inset{Left: 32, Right: 32}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.Y = gtx.Dp(44)
		gtx.Constraints.Min.X = 0
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(centered(label(u.th, 15, c, text)).Layout),
			layout.Rigid(func(gtx C) D {
				if warning == "" {
					return D{}
				}
				return layout.Inset{Top: 4}.Layout(gtx, centered(label(u.th, 14, amber, warning)).Layout)
			}),
		)
	})
}

// serverCard is the summary of the server, like Amnezia's server drawer at the bottom.
func serverCard(gtx C, u *UI, s snapshot, asSheet bool) D {
	th := u.th
	connected := s.phase == phaseConnected
	body := func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		top := unit.Dp(22)
		if asSheet {
			top = 10
		}
		return layout.Inset{Left: 20, Right: 20, Top: top, Bottom: 20}.Layout(gtx, func(gtx C) D {
			return constrainWidth(gtx, 560, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				var children []layout.FlexChild
				if asSheet {
					children = append(children,
						layout.Rigid(func(gtx C) D {
							size := image.Pt(gtx.Dp(36), gtx.Dp(4))
							return layout.Center.Layout(gtx, func(gtx C) D {
								fillRound(gtx, size, bgPanel)
								return D{Size: size}
							})
						}),
						layout.Rigid(layout.Spacer{Height: 14}.Layout))
				}
				subtitle := "AmneziaWG"
				if connected {
					subtitle = "AmneziaWG · " + maskTitle(s.status.Mask)
				}
				children = append(children,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return centered(bold(label(th, 18, textPrimary, "Cloudflare WARP"))).Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: 4}.Layout),
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return centered(label(th, 14, textTertiary, subtitle)).Layout(gtx)
					}),
				)
				// The colo is where traffic leaves Cloudflare, not the server the tunnel talks to,
				// and it is only approximate. Both lines are shown separately so the address on
				// screen is the real one.
				if connected {
					children = append(children, layout.Rigid(layout.Spacer{Height: 10}.Layout))
					if s.status.Endpoint != "" {
						children = append(children, layout.Rigid(centered(label(th, 13.5, textSecondary,
							"Сервер · "+s.status.Endpoint)).Layout))
					}
					if exit := s.status.Exit; exit.Colo != "" {
						line := "Выход · " + exitPlace(exit.Colo) + " (" + exit.Colo
						if exit.Country != "" {
							line += ", " + exit.Country
						}
						line += ")"
						children = append(children, layout.Rigid(func(gtx C) D {
							return layout.Inset{Top: 3}.Layout(gtx, centered(label(th, 12.5, textQuaternary, line)).Layout)
						}))
					}
				} else {
					children = append(children, layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 6}.Layout(gtx, centered(label(th, 14, textTertiary, "Сервер выберется автоматически")).Layout)
					}))
				}
				if connected {
					ping := "—"
					if s.status.RTT > 0 {
						ping = fmt.Sprintf("%d мс", s.status.RTT.Milliseconds())
					}
					children = append(children,
						layout.Rigid(layout.Spacer{Height: 16}.Layout),
						layout.Rigid(func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Flex{Spacing: layout.SpaceEvenly}.Layout(gtx,
								layout.Rigid(stat(u, "Пинг", ping)),
								layout.Rigid(stat(u, "Получено", formatBytes(s.traffic.Rx))),
								layout.Rigid(stat(u, "Отправлено", formatBytes(s.traffic.Tx))),
							)
						}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		})
	}
	if asSheet {
		return sheet(gtx, bgSurface, 24, body)
	}
	return surface(gtx, bgSurface, 24, body)
}

func stat(u *UI, title, value string) layout.Widget {
	return func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(label(u.th, 12, textQuaternary, title).Layout),
			layout.Rigid(layout.Spacer{Height: 2}.Layout),
			layout.Rigid(semibold(label(u.th, 15, textSecondary, value)).Layout),
		)
	}
}

func fillRound(gtx C, size image.Point, c color.NRGBA) {
	paint.FillShape(gtx.Ops, c, clip.UniformRRect(image.Rectangle{Max: size}, size.Y/2).Op(gtx.Ops))
}

func boolf(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func pick[T any](cond bool, yes, no T) T {
	if cond {
		return yes
	}
	return no
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
