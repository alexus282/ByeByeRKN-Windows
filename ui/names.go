package ui

import "github.com/alexus282/ByeByeRKN/windows/awg"

// maskNames are the Android app's titles and explanations of each disguise.
var maskNames = map[awg.Mask]option{
	awg.QUIC:  {"QUIC", "Первый пакет — HTTP/3 к обычному сайту"},
	awg.STUN:  {"STUN", "Первый пакет — как в видеозвонках WebRTC"},
	awg.Light: {"Лёгкая", "Несколько мусорных пакетов перед рукопожатием"},
	awg.DNS:   {"DNS", "Первый пакет — DNS-запрос"},
	awg.SIP:   {"SIP", "Первый пакет — как в IP-телефонии"},
	awg.Heavy: {"Усиленная", "Много мусорных пакетов, для жёстких сетей"},
	awg.None:  {"Без маскировки", "Обычный WireGuard"},
}

func maskTitle(m awg.Mask) string {
	if name, ok := maskNames[m]; ok {
		return name.title
	}
	return m.String()
}

// dnsChoices are the presets in display order; the keys are what the store persists.
var dnsChoices = []struct{ key, title string }{
	{"CLOUDFLARE", "Cloudflare"},
	{"GOOGLE", "Google"},
	{"ADGUARD", "AdGuard (без рекламы)"},
	{"QUAD9", "Quad9"},
}

// cities are Cloudflare data centres reachable from Russia and nearby, by IATA code, as on Android.
var cities = map[string]string{
	"WAW": "Варшава", "FRA": "Франкфурт", "HEL": "Хельсинки", "ARN": "Стокгольм",
	"AMS": "Амстердам", "VIE": "Вена", "PRG": "Прага", "BUD": "Будапешт",
	"RIX": "Рига", "TLL": "Таллин", "VNO": "Вильнюс", "DME": "Москва", "SVO": "Москва", "VKO": "Москва",
	"LED": "Санкт-Петербург", "KJA": "Красноярск", "SVX": "Екатеринбург", "OVB": "Новосибирск",
	"OTP": "Бухарест", "SOF": "София", "BEG": "Белград", "KIV": "Кишинёв",
	"DUS": "Дюссельдорф", "HAM": "Гамбург", "MUC": "Мюнхен", "BER": "Берлин", "TXL": "Берлин",
	"CPH": "Копенгаген", "OSL": "Осло", "LHR": "Лондон", "MAN": "Манчестер",
	"CDG": "Париж", "MRS": "Марсель", "MXP": "Милан", "FCO": "Рим",
	"ZRH": "Цюрих", "GVA": "Женева", "IST": "Стамбул", "EVN": "Ереван",
	"TBS": "Тбилиси", "GYD": "Баку", "ALA": "Алматы", "TAS": "Ташкент",
	"KBP": "Киев", "MSQ": "Минск", "ATH": "Афины", "LIS": "Лиссабон",
	"MAD": "Мадрид", "BCN": "Барселона", "BRU": "Брюссель", "DUB": "Дублин",
}

// exitPlace is where traffic really leaves, which says more to a person than a server address.
func exitPlace(colo string) string {
	if city, ok := cities[colo]; ok {
		return city
	}
	return colo
}
