package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// generateIcon renders the menu bar template icon at 22 px, matching the
// app icon (packaging/icon_1024.png): a scalloped seal badge with a
// checkmark knocked out of it. Drawn in black with 4×4 supersampling so
// macOS can tint it for light/dark menu bars.
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
					cov += sealMarkCoverage(u, v, step)
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

// sealMarkCoverage draws the seal-with-check mark in unit space: a filled
// 12-point scalloped badge with the checkmark cut out. px is one sample
// step in unit space (for edge antialiasing).
func sealMarkCoverage(u, v, px float64) float64 {
	const (
		cx, cy = 0.5, 0.5
		baseR  = 0.40 // seal mid-radius
		amp    = 0.05 // scallop depth
		points = 12
	)

	aa := func(d float64) float64 {
		return math.Max(0, math.Min(1, d/px+0.5))
	}

	dx, dy := u-cx, v-cy
	dist := math.Hypot(dx, dy)
	theta := math.Atan2(dy, dx)
	sealR := baseR + amp*math.Cos(points*theta)
	seal := aa(sealR - dist)
	if seal <= 0 {
		return 0
	}

	// Checkmark knockout: two capsule strokes.
	capsule := func(x1, y1, x2, y2, halfW float64) float64 {
		ddx, ddy := x2-x1, y2-y1
		l2 := ddx*ddx + ddy*ddy
		t := math.Max(0, math.Min(1, ((u-x1)*ddx+(v-y1)*ddy)/l2))
		return halfW - math.Hypot(u-(x1+t*ddx), v-(y1+t*ddy))
	}
	check := math.Max(
		aa(capsule(0.35, 0.52, 0.46, 0.63, 0.065)),
		aa(capsule(0.46, 0.63, 0.67, 0.39, 0.065)),
	)

	return seal * (1 - check)
}
