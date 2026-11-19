package catalog

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Repo is the repository that carries the signed catalog. It is shared with the Android app, so
// new endpoints reach both clients without a release.
const Repo = "alexus282/ByeByeRKN"

// RefreshEvery limits the background check to once a day.
const RefreshEvery = 24 * time.Hour

// CachePath is the signed catalog copy inside the app's data directory.
func CachePath(dir string) string { return filepath.Join(dir, "catalog.json") }

// LoadCache makes a newer signed copy the current catalog. A malformed or unsigned file is
// ignored: the built-in list is always a safe fallback.
func LoadCache(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	signature, err := os.ReadFile(path + ".sig")
	if err != nil || !Verify(data, string(signature)) {
		return
	}
	cached, err := Parse(data)
	if err != nil || cached.Version <= Current().Version {
		return
	}
	SetCurrent(cached)
}

// Refresh downloads and verifies the repository catalog when the cached copy is older than a day.
// Failures are deliberately silent: the app must work offline and on networks where GitHub is
// blocked, so the embedded or previously cached list stays in use.
func Refresh(ctx context.Context, path string) {
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < RefreshEvery {
		if _, err := os.Stat(path + ".sig"); err == nil {
			return
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	catalog, data, signature, err := Fetch(ctx, client, Repo)
	if err != nil {
		return
	}
	if catalog.Version <= Current().Version {
		// Still a successful check: remember it so the next start does not ask again.
		touch(path)
		return
	}
	// Signature first, data second: a crash between the two leaves the old (or missing) data with
	// a newer signature, which LoadCache rejects and the next Refresh retries. The opposite order
	// could leave fresh-looking data with an old signature and postpone the retry by a day.
	if err := writeAtomic(path+".sig", []byte(signature)); err != nil {
		return
	}
	if err := writeAtomic(path, data); err != nil {
		return
	}
	SetCurrent(catalog)
}

func touch(path string) {
	now := time.Now()
	os.Chtimes(path, now, now)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return errors.New("catalog: " + err.Error())
	}
	return nil
}
