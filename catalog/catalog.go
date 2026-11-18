// Package catalog holds the WARP addresses, ports and disguise sites the app tries. A copy is
// built in; a newer one, signed with the project's key, comes from the repository
// (catalog/warp.json), so new addresses reach users without a new release. It is the same file
// and the same key as in the Android app.
package catalog

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
)

// A copy of the repository's catalog/warp.json; TestEmbeddedCopyMatchesRepository keeps them equal.
//
//go:embed warp.json
var builtInJSON []byte

type Catalog struct {
	Version int   `json:"version"`
	Ports   []int `json:"ports"`
	// IPv4 families such as "162.159.192.", completed with a host number.
	Prefixes []string `json:"prefixes"`
	// IPv6 families: the IPv4 endpoint embedded in the last bits, completed with two hex digits.
	V6Prefixes []string `json:"v6Prefixes"`
	// Always in the scan, before random picks.
	WellKnown []string `json:"wellKnown"`
	// Popular sites whose names the QUIC disguise shows.
	QuicSNI []string `json:"quicSni"`
}

// PublicKey is the project's catalog key (P-256, base64 X.509). The private half never leaves the
// maintainer's machine.
const PublicKey = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERscadfs5qqBgSpgJNZF550sHGZapwRt3OtY4gbio/jbrKRZ+Jiza6k+nW2IfAC+x/Y018ms78OW0TMlQSoigvw=="

var BuiltIn = mustParse(builtInJSON)

var current atomic.Pointer[Catalog]

func init() { current.Store(BuiltIn) }

// Current is what searches use: the built-in list until a newer signed one arrives.
func Current() *Catalog { return current.Load() }

func SetCurrent(c *Catalog) { current.Store(c) }

func mustParse(data []byte) *Catalog {
	c, err := Parse(data)
	if err != nil {
		panic("built-in catalog: " + err.Error())
	}
	return c
}

const maxItems = 1000

var (
	prefixRe   = regexp.MustCompile(`^(\d{1,3}\.){3}$`)
	v6PrefixRe = regexp.MustCompile(`^[0-9a-fA-F:]{6,37}$`)
	endpointRe = regexp.MustCompile(`^(\[[0-9a-fA-F:]+\]|[A-Za-z0-9.\-]+):\d{1,5}$`)
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)
)

// Parse reads and checks a catalog; anything malformed or empty is an error, never a partial list.
// Unknown fields are ignored, so later versions can add some.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if err := c.check(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) check() error {
	all := func(items []string, re *regexp.Regexp) bool {
		for _, s := range items {
			if !re.MatchString(s) {
				return false
			}
		}
		return len(items) <= maxItems
	}
	switch {
	case c.Version < 1:
		return errors.New("version must be positive")
	case len(c.Ports) == 0 || len(c.Ports) > maxItems:
		return errors.New("bad port list")
	case len(c.Prefixes) == 0 || !all(c.Prefixes, prefixRe):
		return errors.New("bad prefixes")
	case !all(c.V6Prefixes, v6PrefixRe):
		return errors.New("bad IPv6 prefixes")
	case !all(c.WellKnown, endpointRe):
		return errors.New("bad well-known endpoints")
	case len(c.QuicSNI) == 0 || !all(c.QuicSNI, hostRe):
		return errors.New("bad SNI list")
	}
	for _, p := range c.Ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("bad port %d", p)
		}
	}
	return nil
}

// Verify reports whether signature (base64 DER ECDSA) signs data with the project's key.
func Verify(data []byte, signature string) bool {
	return verifyWith(data, signature, PublicKey)
}

func verifyWith(data []byte, signature, publicKey string) bool {
	der, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return false
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return false
	}
	pub, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(signature), ""))
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return ecdsa.VerifyASN1(pub, sum[:], sig)
}

// Fetch downloads the repository's catalog and its signature. It returns the parsed catalog with
// the exact bytes, so they can be stored and checked again on the next start.
func Fetch(ctx context.Context, client *http.Client, repo string) (*Catalog, []byte, string, error) {
	base := "https://raw.githubusercontent.com/" + repo + "/main/catalog/"
	data, err := get(ctx, client, base+"warp.json")
	if err != nil {
		return nil, nil, "", err
	}
	sig, err := get(ctx, client, base+"warp.json.sig")
	if err != nil {
		return nil, nil, "", err
	}
	if !Verify(data, string(sig)) {
		return nil, nil, "", errors.New("signature does not match")
	}
	c, err := Parse(data)
	if err != nil {
		return nil, nil, "", err
	}
	return c, data, string(sig), nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256*1024))
}
