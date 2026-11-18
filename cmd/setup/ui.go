package main

import (
	"image"
	"image/color"
	"strings"
	"time"

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

var checkIcon = mustIcon(icons.NavigationCheck)

func mustIcon(data []byte) *widget.Icon {
	icon, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return icon
}

func (in *installer) layout(gtx C) D {
	paint.Fill(gtx.Ops, bgBase)
	installing, done, errText, resultDir, launched := in.state()
	if done {
		return in.doneLayout(gtx, resultDir, launched)
	}
	return in.formLayout(gtx, installing, errText)
}

func (in *installer) formLayout(gtx C, installing bool, errText string) D {
	th := in.th
	in.handleForm(gtx, installing)
	content := func(gtx C) D {
		return layout.Inset{Left: 24, Right: 24, Top: 22, Bottom: 20}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(bold(label(th, 26, textPrimary, "Установка ByeByeRKN")).Layout),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 4}.Layout(gtx,
						label(th, 14, textTertiary, "Версия "+version+" · Cloudflare WARP на AmneziaWG").Layout)
				}),
				layout.Rigid(layout.Spacer{Height: 22}.Layout),
				layout.Rigid(func(gtx C) D { return in.pathCard(gtx, th, installing) }),
				layout.Rigid(layout.Spacer{Height: 14}.Layout),
				layout.Rigid(func(gtx C) D { return in.optionsCard(gtx, th, installing) }),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 16, Left: 4, Right: 4}.Layout(gtx,
						label(th, 13, textMuted, "Установщик создаст службу Windows, которая будет управлять VPN. "+
							"Ярлыки и папка установки выбираются выше; настройки и аккаунт WARP переносятся в защищённую папку службы.").Layout)
				}),
				layout.Rigid(func(gtx C) D {
					if errText == "" {
						return D{}
					}
					return layout.Inset{Top: 14}.Layout(gtx, label(th, 13.5, danger, errText).Layout)
				}),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D {
							if !installing {
								return D{}
							}
							dots := strings.Repeat(".", int(gtx.Now.UnixMilli()/400)%4)
							gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(400 * time.Millisecond)})
							return label(th, 14, textTertiary, "Устанавливаю"+dots).Layout(gtx)
						}),
						layout.Rigid(func(gtx C) D {
							return button(gtx, th, &in.cancelClick, "Отмена", false, installing)
						}),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
						layout.Rigid(func(gtx C) D {
							text := "Установить"
							if installing {
								text = "Установка…"
							}
							return button(gtx, th, &in.installClick, text, true, installing)
						}),
					)
				}),
			)
		})
	}
	gtx.Constraints.Min = gtx.Constraints.Max
	return constrainWidth(gtx, 600, content)
}

func (in *installer) handleForm(gtx C, installing bool) {
	if installing {
		return
	}
	if in.browseClick.Clicked(gtx) {
		in.browseFolder()
	}
	if in.startClick.Clicked(gtx) {
		in.startMenu = !in.startMenu
	}
	if in.desktopClick.Clicked(gtx) {
		in.desktop = !in.desktop
	}
	if in.autoClick.Clicked(gtx) {
		in.autostart = !in.autostart
	}
	if in.launchClick.Clicked(gtx) {
		in.launch = !in.launch
	}
	if in.installClick.Clicked(gtx) {
		in.install()
	}
	if in.cancelClick.Clicked(gtx) {
		in.close()
	}
}

func (in *installer) pathCard(gtx C, th *material.Theme, disabled bool) D {
	return surface(gtx, bgSurfaceAlt, 16, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(16).Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(label(th, 13, textTertiary, "Папка установки").Layout),
				layout.Rigid(layout.Spacer{Height: 8}.Layout),
				layout.Rigid(func(gtx C) D {
					row := gtx
					if disabled {
						row = gtx.Disabled()
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(row,
						layout.Flexed(1, func(gtx C) D {
							return field(gtx, th, in.dir, `C:\Program Files\ByeByeRKN`)
						}),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
						layout.Rigid(func(gtx C) D {
							return button(gtx, th, &in.browseClick, "Обзор…", false, disabled)
						}),
					)
				}),
			)
		})
	})
}

func (in *installer) optionsCard(gtx C, th *material.Theme, disabled bool) D {
	check := func(click *widget.Clickable, on *bool, title, summary string) layout.Widget {
		return func(gtx C) D {
			if !disabled && click.Clicked(gtx) {
				*on = !*on
			}
			return checkRow(gtx, th, click, *on, title, summary)
		}
	}
	return surface(gtx, bgSurfaceAlt, 16, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(check(&in.startClick, &in.startMenu, "Ярлык в меню «Пуск»", "Для всех пользователей компьютера")),
			layout.Rigid(divider),
			layout.Rigid(check(&in.desktopClick, &in.desktop, "Ярлык на рабочем столе", "")),
			layout.Rigid(divider),
			layout.Rigid(check(&in.autoClick, &in.autostart, "Запускать при входе в Windows", "VPN включится, если в настройках включено автоподключение")),
			layout.Rigid(divider),
			layout.Rigid(check(&in.launchClick, &in.launch, "Запустить после установки", "")),
		)
	})
}

func (in *installer) doneLayout(gtx C, dir string, launched bool) D {
	th := in.th
	if in.closeClick.Clicked(gtx) {
		in.close()
	}
	if in.runClick.Clicked(gtx) {
		in.runApp()
	}
	content := func(gtx C) D {
		return layout.Inset{Left: 28, Right: 28, Top: 28, Bottom: 22}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(bold(label(th, 26, brand, "Готово")).Layout),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 10}.Layout(gtx,
						label(th, 15, textSecondary, "ByeByeRKN установлен в\n"+dir).Layout)
				}),
				layout.Rigid(func(gtx C) D {
					if !launched {
						return D{}
					}
					return layout.Inset{Top: 8}.Layout(gtx, label(th, 14, textTertiary, "Приложение запущено.").Layout)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 14}.Layout(gtx, label(th, 13, textMuted,
						"Дальше запускайте ByeByeRKN с ярлыка: права администратора больше не нужны. "+
							"Удалить приложение можно в «Программы и компоненты».").Layout)
				}),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Spacing: layout.SpaceEnd}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return button(gtx, th, &in.closeClick, "Закрыть", false, false) }),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
						layout.Rigid(func(gtx C) D {
							text := "Запустить ByeByeRKN"
							if launched {
								text = "Открыть ещё раз"
							}
							return button(gtx, th, &in.runClick, text, true, false)
						}),
					)
				}),
			)
		})
	}
	gtx.Constraints.Min = gtx.Constraints.Max
	return constrainWidth(gtx, 600, content)
}

func checkRow(gtx C, th *material.Theme, click *widget.Clickable, checked bool, title, summary string) D {
	return pressable(gtx, click, fade(bgSheet, 0.5), 12, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: 14, Right: 14, Top: 12, Bottom: 12}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return checkBox(gtx, checked) }),
				layout.Rigid(layout.Spacer{Width: 12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(label(th, 15, textPrimary, title).Layout),
						layout.Rigid(func(gtx C) D {
							if summary == "" {
								return D{}
							}
							return layout.Inset{Top: 2}.Layout(gtx, label(th, 13, textTertiary, summary).Layout)
						}),
					)
				}),
			)
		})
	})
}

func checkBox(gtx C, checked bool) D {
	size := gtx.Dp(22)
	rect := image.Rectangle{Max: image.Pt(size, size)}
	rr := clip.UniformRRect(rect, gtx.Dp(6))
	if checked {
		paint.FillShape(gtx.Ops, brand, rr.Op(gtx.Ops))
		inner := gtx
		inner.Constraints = layout.Exact(image.Pt(gtx.Dp(16), gtx.Dp(16)))
		off := image.Pt((size-inner.Constraints.Max.X)/2, (size-inner.Constraints.Max.Y)/2)
		defer op.Offset(off).Push(gtx.Ops).Pop()
		checkIcon.Layout(inner, onBrand)
	} else {
		paint.FillShape(gtx.Ops, bgSheet, rr.Op(gtx.Ops))
		inner := gtx
		inner.Constraints = layout.Exact(rect.Max)
		widget.Border{Color: bgPanel, CornerRadius: 6, Width: 1.5}.Layout(inner, func(gtx C) D {
			return D{Size: rect.Max}
		})
	}
	return D{Size: rect.Max}
}

func divider(gtx C) D {
	return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx C) D {
		size := image.Pt(gtx.Constraints.Max.X, max(1, gtx.Dp(0.75)))
		paint.FillShape(gtx.Ops, bgSheet, clip.Rect{Max: size}.Op())
		return D{Size: size}
	})
}

func surface(gtx C, bg color.NRGBA, radius unit.Dp, w layout.Widget) D {
	rec := op.Record(gtx.Ops)
	dims := w(gtx)
	call := rec.Stop()
	rr := clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(radius))
	paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
	defer rr.Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return dims
}

func pressable(gtx C, click *widget.Clickable, hover color.NRGBA, radius unit.Dp, w layout.Widget) D {
	return click.Layout(gtx, func(gtx C) D {
		rec := op.Record(gtx.Ops)
		dims := w(gtx)
		call := rec.Stop()
		rect := image.Rectangle{Max: dims.Size}
		if click.Hovered() {
			paint.FillShape(gtx.Ops, hover, clip.UniformRRect(rect, gtx.Dp(radius)).Op(gtx.Ops))
		}
		call.Add(gtx.Ops)
		defer clip.Rect(rect).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return dims
	})
}

func button(gtx C, th *material.Theme, click *widget.Clickable, text string, primary, disabled bool) D {
	bg, fg := bgPanel, textPrimary
	if primary {
		bg, fg = brand, onBrand
	}
	if disabled {
		bg, fg = fade(bg, 0.4), fade(fg, 0.5)
	}
	if !disabled && click.Hovered() {
		bg = mix(bg, rgb(0xFFFFFF), 0.08)
	}
	return click.Layout(gtx, func(gtx C) D {
		height := gtx.Dp(44)
		width := gtx.Constraints.Min.X
		if width == 0 {
			// The primary action ("Запустить ByeByeRKN") is wider than "Обзор…".
			width = max(gtx.Dp(130), gtx.Dp(unit.Dp(len([]rune(text))*8+36)))
		}
		size := image.Pt(width, height)
		rect := image.Rectangle{Max: size}
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, gtx.Dp(12)).Op(gtx.Ops))
		gtx.Constraints = layout.Exact(size)
		layout.Center.Layout(gtx, semibold(label(th, 15, fg, text)).Layout)
		defer clip.Rect(rect).Push(gtx.Ops).Pop()
		if !disabled {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		return D{Size: size}
	})
}

func field(gtx C, th *material.Theme, editor *widget.Editor, hint string) D {
	border := color.NRGBA{}
	if gtx.Focused(editor) {
		border = brand
	}
	return surface(gtx, bgSheet, 12, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return widget.Border{Color: border, CornerRadius: 12, Width: 1.5}.Layout(gtx, func(gtx C) D {
			return layout.Inset{Left: 14, Right: 14, Top: 12, Bottom: 12}.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				e := material.Editor(th, editor, hint)
				e.Color = textPrimary
				e.HintColor = textMuted
				e.SelectionColor = fade(brand, 0.35)
				e.TextSize = 14.5
				return e.Layout(gtx)
			})
		})
	})
}

func constrainWidth(gtx C, maxWidth unit.Dp, w layout.Widget) D {
	limit := gtx.Dp(maxWidth)
	if gtx.Constraints.Max.X <= limit {
		return w(gtx)
	}
	full := gtx.Constraints.Max.X
	offset := (full - limit) / 2
	gtx.Constraints.Max.X = limit
	if gtx.Constraints.Min.X > limit {
		gtx.Constraints.Min.X = limit
	}
	defer op.Offset(image.Pt(offset, 0)).Push(gtx.Ops).Pop()
	dims := w(gtx)
	dims.Size.X = full
	return dims
}

func mix(a, b color.NRGBA, t float32) color.NRGBA {
	lerp := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B), A: lerp(a.A, b.A)}
}
