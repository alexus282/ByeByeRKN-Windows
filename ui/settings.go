package ui

import (
	"io"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
	"github.com/alexus282/ByeByeRKN-Windows/catalog"
	"github.com/alexus282/ByeByeRKN-Windows/store"
	"github.com/alexus282/ByeByeRKN-Windows/warp"
)

const defaultEndpoint = "162.159.192.1:2408"

var (
	endpointPattern = regexp.MustCompile(`^(\[[0-9a-fA-F:]+]|[\w.\-]+):\d{1,5}$`)
	hostPattern     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)
)

type settingsScreen struct {
	list widget.List

	autoConnect, closeToTray         widget.Clickable
	copyConfig, saveConfig, advanced widget.Clickable
	mask, server, dns, sni, repick   widget.Clickable
	warpPlus, reregister, folder     widget.Clickable
	autoAnim, trayAnim               tween
	showAdvanced, manualServer       bool
	endpoint                         widget.Editor
	// clipboardText is set by a background export and written on the next frame, where the
	// clipboard command can use the frame's operations.
	clipboardText string
}

func (s *settingsScreen) init(settings store.Settings) {
	s.list.Axis = layout.Vertical
	s.endpoint.SingleLine = true
	s.manualServer = settings.ManualEndpoint != ""
	s.endpoint.SetText(orDefault(settings.ManualEndpoint, defaultEndpoint))
	// Someone who already chose things by hand should see them without hunting for a toggle.
	s.showAdvanced = settings.ManualEndpoint != "" || settings.Obfuscation != "" || settings.QuicSNI != ""
}

func (s *settingsScreen) handle(gtx C, u *UI) {
	settings := u.be.Settings()
	if s.autoConnect.Clicked(gtx) {
		u.updateSettings(func(st *store.Settings) { st.AutoConnect = !st.AutoConnect })
	}
	if s.closeToTray.Clicked(gtx) {
		u.updateSettings(func(st *store.Settings) { st.ExitOnClose = !st.ExitOnClose })
	}
	if s.clipboardText != "" {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(s.clipboardText))})
		s.clipboardText = ""
		u.toast("Конфиг скопирован")
	}
	if s.copyConfig.Clicked(gtx) {
		go func() {
			text, err := u.be.ExportConfig()
			u.post(func() {
				if err != nil {
					u.toast("Не удалось получить конфиг: " + err.Error())
					return
				}
				s.clipboardText = text
			})
		}()
	}
	if s.saveConfig.Clicked(gtx) {
		s.save(u)
	}
	if s.advanced.Clicked(gtx) {
		s.showAdvanced = !s.showAdvanced
	}
	if s.mask.Clicked(gtx) {
		options := []option{{"Авто", "Подбирается под вашу сеть"}}
		selected := 0
		for i, mask := range awg.AutoOrder {
			options = append(options, maskNames[mask])
			if mask.String() == settings.Obfuscation {
				selected = i + 1
			}
		}
		u.open(&modal{title: "Маскировка", options: options, selected: selected, onPick: func(i int) {
			u.updateSettings(func(st *store.Settings) {
				st.Obfuscation = ""
				if i > 0 {
					st.Obfuscation = awg.AutoOrder[i-1].String()
				}
			})
		}})
	}
	if s.server.Clicked(gtx) {
		options := []option{{"Авто", "Подбирается автоматически"}, {"Вручную", "Свой адрес WARP"}}
		u.open(&modal{title: "Сервер", options: options, selected: int(boolf(s.manualServer)), onPick: func(i int) {
			s.manualServer = i == 1
			s.applyEndpoint(u)
		}})
	}
	for {
		ev, ok := s.endpoint.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.ChangeEvent); ok {
			s.applyEndpoint(u)
		}
	}
	if s.dns.Clicked(gtx) {
		options := make([]option, len(dnsChoices))
		selected := 0
		for i, choice := range dnsChoices {
			options[i] = option{choice.title, strings.Join(store.DNSServerLists[choice.key][:2], ", ")}
			if choice.key == settings.DNS {
				selected = i
			}
		}
		u.open(&modal{title: "DNS", options: options, selected: selected, onPick: func(i int) {
			u.updateSettings(func(st *store.Settings) { st.DNS = dnsChoices[i].key })
		}})
	}
	if s.sni.Clicked(gtx) {
		ed := &widget.Editor{SingleLine: true, Submit: true}
		ed.SetText(settings.QuicSNI)
		examples := catalog.Current().QuicSNI
		if len(examples) > 3 {
			examples = examples[:3]
		}
		u.open(&modal{
			title:    "Сайт для QUIC-маски",
			text:     "Первый пакет будет похож на HTTP/3-запрос к этому сайту. Пусто — случайный из " + strings.Join(examples, ", ") + " и других.",
			editor:   ed,
			hint:     "ya.ru",
			confirm:  "Сохранить",
			validate: func(v string) bool { return v == "" || (len(v) <= 253 && hostPattern.MatchString(v)) },
			onConfirm: func(v string) {
				u.updateSettings(func(st *store.Settings) { st.QuicSNI = v })
			},
		})
	}
	if s.repick.Clicked(gtx) {
		go func() {
			err := u.be.ForgetServer()
			u.post(func() {
				if err != nil {
					u.toast(err.Error())
					return
				}
				u.toast("При следующем подключении сервер подберётся заново")
			})
		}()
	}
	if s.warpPlus.Clicked(gtx) {
		s.editWarpPlus(u, settings.WarpPlusKey)
	}
	if s.reregister.Clicked(gtx) {
		u.open(&modal{
			title:       "Новая регистрация",
			text:        "Будет создан новый ключ и адрес WARP. Старые экспортированные конфиги продолжат работать, пока их не отключит Cloudflare.",
			confirm:     "Сбросить",
			destructive: true,
			onConfirm: func(string) {
				go func() {
					err := u.be.ResetAccount()
					u.post(func() {
						if err != nil {
							u.toast(err.Error())
							return
						}
						u.toast("Новая регистрация будет при следующем подключении")
					})
				}()
			},
		})
	}
	if s.folder.Clicked(gtx) && u.host.openFolder != nil {
		u.host.openFolder(filepath.Dir(u.be.Path()))
	}
}

// applyEndpoint stores the typed address only when it is valid, as on Android: a half-typed one
// must not break the next connection. Writing only real changes keeps a keystroke from touching
// the disk.
func (s *settingsScreen) applyEndpoint(u *UI) {
	text := strings.TrimSpace(s.endpoint.Text())
	current := u.be.Settings().ManualEndpoint
	next := current
	switch {
	case !s.manualServer:
		next = ""
	case endpointPattern.MatchString(text):
		next = text
	}
	if next == current {
		return
	}
	u.updateSettings(func(st *store.Settings) { st.ManualEndpoint = next })
}

func (s *settingsScreen) save(u *UI) {
	if u.host.saveFile == nil {
		return
	}
	go func() {
		text, err := u.be.ExportConfig()
		if err != nil {
			u.post(func() { u.toast("Не удалось получить конфиг: " + err.Error()) })
			return
		}
		path, err := u.host.saveFile("byebyerkn.conf", text)
		u.post(func() {
			switch {
			case err != nil:
				u.toast("Не удалось сохранить: " + err.Error())
			case path != "":
				u.toast("Сохранено: " + path)
			}
		})
	}()
}

func (s *settingsScreen) editWarpPlus(u *UI, current string) {
	ed := &widget.Editor{SingleLine: true, Submit: true}
	ed.SetText(current)
	u.open(&modal{
		title:    "Ключ WARP+",
		text:     "Ключ даёт ускоренные маршруты WARP+ и привязывается к устройствам приложения. Без ключа всё работает так же, на бесплатном WARP. Пустое поле — забыть ключ.",
		editor:   ed,
		hint:     "xxxxxxxx-xxxxxxxx-xxxxxxxx",
		mono:     true,
		confirm:  "Сохранить",
		validate: func(v string) bool { return v == "" || warp.IsLicense(v) },
		onConfirm: func(key string) {
			if key == "" {
				u.updateSettings(func(st *store.Settings) { st.WarpPlusKey = "" })
				u.toast("Ключ забыт. Чтобы вернуться к бесплатному WARP, зарегистрируйтесь заново")
				return
			}
			u.toast("Проверяю ключ…")
			go func() {
				message, err := u.be.ApplyWarpPlus(key)
				u.post(func() {
					if err != nil {
						u.toast("Ключ не подошёл: " + err.Error())
						return
					}
					u.toast(message)
				})
			}()
		},
	})
}

func (s *settingsScreen) layout(gtx C, u *UI) D {
	s.handle(gtx, u)
	th := u.th
	settings := u.be.Settings()

	items := []layout.Widget{
		pageTitle(th, "Настройки"),
		sectionTitle(th, "Запуск и трей"),
		group(
			func(gtx C) D {
				return row{title: "Подключаться при запуске", summary: "Сразу искать сервер, когда открывается приложение", trail: trailSwitch, on: settings.AutoConnect}.
					layout(gtx, th, &s.autoConnect, &s.autoAnim)
			},
			func(gtx C) D {
				summary := "Крестик прячет окно в трей, VPN продолжает работать"
				if settings.ExitOnClose {
					summary = "Крестик отключает VPN и закрывает приложение"
				}
				return row{title: "Сворачивать в трей", summary: summary, trail: trailSwitch, on: !settings.ExitOnClose}.
					layout(gtx, th, &s.closeToTray, &s.trayAnim)
			},
		),
		sectionTitle(th, "Конфиг для AmneziaWG"),
		group(
			func(gtx C) D {
				return row{title: "Скопировать конфиг", summary: "Для другого клиента AmneziaWG", trail: trailArrow}.layout(gtx, th, &s.copyConfig, nil)
			},
			func(gtx C) D {
				return row{title: "Сохранить в файл", summary: "byebyerkn.conf", trail: trailArrow}.layout(gtx, th, &s.saveConfig, nil)
			},
		),
		sectionTitle(th, "Для опытных"),
		group(func(gtx C) D {
			title := "Показать ручные настройки"
			if s.showAdvanced {
				title = "Скрыть ручные настройки"
			}
			return row{title: title, summary: "Обычно не нужны: приложение подбирает всё само", trail: trailArrow}.layout(gtx, th, &s.advanced, nil)
		}),
	}
	if s.showAdvanced {
		items = append(items, s.advancedItems(th, u, settings)...)
	}
	items = append(items,
		sectionTitle(th, "О приложении"),
		group(
			func(gtx C) D {
				return row{title: "ByeByeRKN " + Version, summary: "AmneziaWG " + amneziaVersion()}.layout(gtx, th, nil, nil)
			},
			func(gtx C) D {
				if u.be.ServiceMode() {
					return row{title: "Папка службы", summary: filepath.Dir(u.be.Path()) + " — доступна только службе и администраторам"}.layout(gtx, th, nil, nil)
				}
				return row{title: "Папка настроек", summary: filepath.Dir(u.be.Path()), trail: trailArrow}.layout(gtx, th, &s.folder, nil)
			},
			func(gtx C) D {
				return row{title: "Лицензии", summary: "amneziawg-go, Wintun, Gio и go-winio (MIT, Apache-2.0, Unlicense), на основе WireGuard®. Не связано с Cloudflare; WARP — сервис Cloudflare, Inc."}.layout(gtx, th, nil, nil)
			},
		),
		layout.Spacer{Height: 28}.Layout,
	)

	return material.List(th, &s.list).Layout(gtx, len(items), func(gtx C, i int) D {
		return layout.Inset{Left: 12, Right: 12}.Layout(gtx, func(gtx C) D {
			return constrainWidth(gtx, 720, items[i])
		})
	})
}

func (s *settingsScreen) advancedItems(th *material.Theme, u *UI, settings store.Settings) []layout.Widget {
	maskValue, maskSummary := "Авто", "Подбирается под вашу сеть"
	if mask, ok := awg.ParseMask(settings.Obfuscation); ok && settings.Obfuscation != "" {
		maskValue, maskSummary = maskNames[mask].title, maskNames[mask].summary
	}
	dnsValue := dnsChoices[0].title
	for _, choice := range dnsChoices {
		if choice.key == settings.DNS {
			dnsValue = choice.title
		}
	}
	warpSummary := "Не указан: обычный бесплатный WARP"
	if key := settings.WarpPlusKey; len(key) > 8 {
		warpSummary = key[:4] + "…" + key[len(key)-4:]
	}
	regSummary := "Регистрация будет при первом подключении"
	if u.be.HasAccount() {
		regSummary = "Новый ключ и адрес WARP"
	}

	connection := []layout.Widget{
		func(gtx C) D {
			return row{title: "Маскировка", summary: maskSummary, trail: trailValue, value: maskValue}.layout(gtx, th, &s.mask, nil)
		},
		func(gtx C) D {
			value, summary := "Авто", "Подбирается автоматически"
			if s.manualServer {
				value, summary = "Вручную", "Свой адрес WARP"
			}
			dims := row{title: "Сервер", summary: summary, trail: trailValue, value: value}.layout(gtx, th, &s.server, nil)
			return dims
		},
	}
	if s.manualServer {
		connection = append(connection, func(gtx C) D {
			invalid := !endpointPattern.MatchString(strings.TrimSpace(s.endpoint.Text()))
			return layout.Inset{Left: 20, Right: 20, Bottom: 16}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return field(gtx, th, &s.endpoint, "Адрес:порт", invalid, true) }),
					layout.Rigid(func(gtx C) D {
						if !invalid {
							return D{}
						}
						return layout.Inset{Top: 6, Left: 4}.Layout(gtx, label(th, 13, danger, "Формат IP:порт, например "+defaultEndpoint).Layout)
					}),
				)
			})
		})
	}
	connection = append(connection,
		func(gtx C) D {
			servers := store.DNSServers(settings.DNS)
			return row{title: "DNS", summary: strings.Join(servers[:2], ", "), trail: trailValue, value: dnsValue}.layout(gtx, th, &s.dns, nil)
		},
		func(gtx C) D {
			return row{title: "Сайт для QUIC-маски", summary: orDefault(settings.QuicSNI, "Случайный популярный сайт"), trail: trailArrow}.layout(gtx, th, &s.sni, nil)
		},
		func(gtx C) D {
			return row{title: "Подобрать сервер заново", summary: "При следующем подключении", trail: trailArrow}.layout(gtx, th, &s.repick, nil)
		},
	)

	return []layout.Widget{
		sectionTitle(th, "Подключение"),
		groupMerged(connection, map[int]bool{2: s.manualServer}),
		func(gtx C) D {
			return layout.Inset{Left: 8, Top: 8}.Layout(gtx, label(th, 13, textQuaternary, "Изменения применятся при следующем подключении").Layout)
		},
		sectionTitle(th, "Аккаунт WARP"),
		group(
			func(gtx C) D {
				return row{title: "Ключ WARP+", summary: warpSummary, trail: trailArrow}.layout(gtx, th, &s.warpPlus, nil)
			},
			func(gtx C) D {
				return row{title: "Зарегистрироваться заново", summary: regSummary, trail: trailArrow}.layout(gtx, th, &s.reregister, nil)
			},
		),
	}
}

// group is a settings card with thin dividers between its rows.
func group(rows ...layout.Widget) layout.Widget {
	return groupMerged(rows, nil)
}

// groupMerged is a group where the rows marked in attached continue the row above them without a
// divider, as the endpoint field belongs to the server row.
func groupMerged(rows []layout.Widget, attached map[int]bool) layout.Widget {
	return func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return surface(gtx, bgSurfaceAlt, 20, func(gtx C) D {
			children := make([]layout.FlexChild, 0, 2*len(rows))
			for i, r := range rows {
				if i > 0 && !attached[i] {
					children = append(children, layout.Rigid(divider))
				}
				children = append(children, layout.Rigid(r))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
	}
}

func amneziaVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/amnezia-vpn/amneziawg-go" {
				return strings.TrimPrefix(dep.Version, "v")
			}
		}
	}
	return "—"
}
