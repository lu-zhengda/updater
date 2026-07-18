package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// generateIcon renders the menu bar template icon at 22 px: the same
// double-chevron "upgrade" mark as the app icon (scripts/genicon), drawn in
// black with 4×4 supersampling so macOS can tint it for light/dark menu
// bars. The faded bottom chevron uses partial alpha, which template images
// preserve.
func generateIcon() []byte {
	const size = 22
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	const step = 1.0 / (4 * size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var cov float64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					u := (float64(px) + (float64(sx)+0.5)/4) / size
					v := (float64(py) + (float64(sy)+0.5)/4) / size
					cov += updateMarkCoverage(u, v, step)
				}
			}
			cov /= 16
			if cov > 0 {
				img.SetNRGBA(px, py, color.NRGBA{0, 0, 0, uint8(255 * cov)})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// updateMarkCoverage draws the double-chevron "upgrade" mark in unit space —
// kept in sync with scripts/genicon/main.go, with slightly heavier strokes
// and wider spacing so the mark stays crisp at 22 px.
func updateMarkCoverage(u, v, px float64) float64 {
	const halfW = 0.075

	aa := func(d float64) float64 {
		return math.Max(0, math.Min(1, d/px+0.5))
	}
	capsule := func(x1, y1, x2, y2 float64) float64 {
		dx, dy := x2-x1, y2-y1
		l2 := dx*dx + dy*dy
		t := ((u-x1)*dx + (v-y1)*dy) / l2
		t = math.Max(0, math.Min(1, t))
		return halfW - math.Hypot(u-(x1+t*dx), v-(y1+t*dy))
	}

	var cov float64
	// Top chevron, full strength.
	cov = math.Max(cov, aa(capsule(0.22, 0.46, 0.50, 0.24)))
	cov = math.Max(cov, aa(capsule(0.50, 0.24, 0.78, 0.46)))
	// Bottom chevron, faded.
	cov = math.Max(cov, 0.5*aa(capsule(0.22, 0.76, 0.50, 0.54)))
	cov = math.Max(cov, 0.5*aa(capsule(0.50, 0.54, 0.78, 0.76)))
	return cov
}
