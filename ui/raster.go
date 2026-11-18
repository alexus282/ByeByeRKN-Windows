package ui

import (
	"image"

	"golang.org/x/image/vector"
)

// rasterizer is the vector rasterizer the tray fallback and the icon test draw with.
type rasterizer = vector.Rasterizer

func newRasterizer(img *image.RGBA) *vector.Rasterizer {
	return vector.NewRasterizer(img.Bounds().Dx(), img.Bounds().Dy())
}
