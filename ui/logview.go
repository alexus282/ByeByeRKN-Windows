package ui

import (
	"io"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// logScreen explains what the search is doing: the same lines a support request needs.
type logScreen struct {
	list        widget.List
	copy, clear widget.Clickable
	seen        int
}

func (l *logScreen) layout(gtx C, u *UI, s snapshot) D {
	th := u.th
	if l.copy.Clicked(gtx) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(logText(s.logs)))})
		u.toast("Журнал скопирован")
	}
	if l.clear.Clicked(gtx) {
		u.mu.Lock()
		u.logs = nil
		u.mu.Unlock()
		s.logs = nil
	}
	l.list.Axis = layout.Vertical
	// New lines stay in view unless the user scrolled up to read. ScrollToEnd would do it too,
	// but it also pushes a short log down to the bottom of the card.
	if len(s.logs) != l.seen {
		if !l.list.Position.BeforeEnd && len(s.logs) > 0 {
			l.list.ScrollTo(len(s.logs) - 1)
		}
		l.seen = len(s.logs)
	}

	return layout.Inset{Left: 12, Right: 12, Bottom: 8}.Layout(gtx, func(gtx C) D {
		return constrainWidth(gtx, 860, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.End}.Layout(gtx,
						layout.Flexed(1, pageTitle(th, "Журнал")),
						layout.Rigid(func(gtx C) D { return chip(gtx, th, &l.copy, icCopy, "Скопировать") }),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx C) D { return chip(gtx, th, &l.clear, icDelete, "Очистить") }),
					)
				}),
				layout.Rigid(layout.Spacer{Height: 16}.Layout),
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min = gtx.Constraints.Max
					return surface(gtx, bgSurfaceAlt, 20, func(gtx C) D {
						gtx.Constraints.Min = gtx.Constraints.Max
						if len(s.logs) == 0 {
							return layout.Center.Layout(gtx, func(gtx C) D {
								return layout.UniformInset(24).Layout(gtx,
									centered(label(th, 14, textQuaternary, "Здесь появятся сообщения о поиске сервера и подключении")).Layout)
							})
						}
						return layout.Inset{Top: 10, Bottom: 10, Left: 4}.Layout(gtx, func(gtx C) D {
							return material.List(th, &l.list).Layout(gtx, len(s.logs), func(gtx C, i int) D {
								line := s.logs[i]
								return layout.Inset{Left: 12, Right: 12, Top: 3, Bottom: 3}.Layout(gtx, func(gtx C) D {
									return layout.Flex{}.Layout(gtx,
										layout.Rigid(func(gtx C) D {
											t := label(th, 12.5, textQuaternary, line.at.Format("15:04:05"))
											t.Font = font.Font{Typeface: monoFace}
											return layout.Inset{Top: 1, Right: 12}.Layout(gtx, t.Layout)
										}),
										layout.Flexed(1, label(th, 13.5, textSecondary, line.text).Layout),
									)
								})
							})
						})
					})
				}),
			)
		})
	})
}

func logText(lines []logLine) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.at.Format("15:04:05"))
		b.WriteString("  ")
		b.WriteString(line.text)
		b.WriteString("\r\n")
	}
	return b.String()
}
