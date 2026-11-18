package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Exit is where traffic leaves Cloudflare, parsed from a cdn-cgi/trace response.
type Exit struct {
	Colo    string
	Country string
	IP      string
	Warp    bool
}

// RussianColos are exits where traffic crosses Russian networks and DPI again. They are judged by
// data centre, not country: Cloudflare reports loc=RU even when the exit is in Helsinki.
var RussianColos = map[string]bool{
	"DME": true, "SVO": true, "VKO": true, "LED": true, "KJA": true, "SVX": true,
	"OVB": true, "KZN": true, "ROV": true, "AER": true, "KHV": true, "VVO": true,
}

func (e Exit) InRussia() bool { return RussianColos[e.Colo] }

func ParseTrace(body string) (Exit, bool) {
	var exit Exit
	seen := false
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "colo":
			exit.Colo, seen = value, true
		case "loc":
			exit.Country = value
		case "ip":
			exit.IP = value
		case "warp":
			exit.Warp = value == "on" || value == "plus"
		}
	}
	return exit, seen
}

const traceURL = "https://1.1.1.1/cdn-cgi/trace"

// FetchTrace asks Cloudflare where our traffic leaves. The address is a literal, so the request
// cannot stall on DNS behind a half-dead tunnel; a fresh transport per call avoids reusing a
// connection that belonged to the previous route.
func FetchTrace(ctx context.Context) (Exit, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return Exit{}, err
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Do(req)
	if err != nil {
		return Exit{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return Exit{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Exit{}, fmt.Errorf("trace: HTTP %d", response.StatusCode)
	}
	exit, ok := ParseTrace(string(body))
	if !ok {
		return Exit{}, fmt.Errorf("trace: no colo in %q", body)
	}
	return exit, nil
}
