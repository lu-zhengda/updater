package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// generateIcon creates a 22x22 monochrome template icon for the menu bar.
// It draws an upward arrow (↑) representing updates, suitable as a macOS template image.
func generateIcon() []byte {
	const size = 22
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Draw an upward arrow in black (template images use black; macOS tints automatically).
	black := color.NRGBA{R: 0, G: 0, B: 0, A: 255}

	// Arrow shaft: vertical line in the center, rows 7-18.
	for y := 7; y <= 18; y++ {
		img.SetNRGBA(10, y, black)
		img.SetNRGBA(11, y, black)
	}

	// Arrow head: chevron pointing up, rows 3-9.
	for i := 0; i <= 6; i++ {
		// Left side of chevron.
		x := 10 - i
		y := 7 + i
		if x >= 0 && y < size {
			img.SetNRGBA(x, y, black)
			img.SetNRGBA(x+1, y, black)
		}
		// Right side of chevron.
		x2 := 11 + i
		if x2 < size && y < size {
			img.SetNRGBA(x2, y, black)
			img.SetNRGBA(x2-1, y, black)
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
