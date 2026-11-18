// Package winres carries the app's icon, generated from art/icon-source.webp by
// windows/make_icon.py.
package winres

import _ "embed"

//go:embed icon.png
var IconPNG []byte
