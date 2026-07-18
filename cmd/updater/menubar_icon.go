package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// generateIcon renders the menu bar template icon at 22 px: the same
// "update cycle" mark as the app icon (scripts/genicon) — a clockwise
// refresh ring with an arrowhead and a rising arrow in the center — drawn
// in black with 4×4 supersampling so macOS can tint it for light/dark
// menu bars.
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

// updateMarkCoverage draws the "update cycle" mark in unit space — kept in
// sync with scripts/genicon/main.go, with slightly heavier strokes so the
// mark stays crisp at 22 px.
func updateMarkCoverage(u, v, px float64) float64 {
	const (
		cx, cy   = 0.5, 0.5
		ringR    = 0.36
		ringHalf = 0.062
		gapFrom  = -80.0
		gapTo    = -20.0
	)

	dx, dy := u-cx, v-cy
	dist := math.Hypot(dx, dy)
	angle := math.Atan2(dy, dx) * 180 / math.Pi

	aa := func(d float64) float64 {
		return math.Max(0, math.Min(1, d/px+0.5))
	}

	var cov float64

	inGap := angle > gapFrom && angle < gapTo
	if !inGap {
		cov = math.Max(cov, aa(ringHalf-math.Abs(dist-ringR)))
	}

	// Round cap at the lower gap edge; the upper edge is the arrowhead.
	{
		rad := gapTo * math.Pi / 180
		ex, ey := cx+ringR*math.Cos(rad), cy+ringR*math.Sin(rad)
		cov = math.Max(cov, aa(ringHalf-math.Hypot(u-ex, v-ey)))
	}

	// Arrowhead pointing clockwise into the gap.
	{
		rad := gapFrom * math.Pi / 180
		ex, ey := cx+ringR*math.Cos(rad), cy+ringR*math.Sin(rad)
		tx, ty := -math.Sin(rad), math.Cos(rad)
		p := (u-ex)*tx + (v-ey)*ty
		q := (u-ex)*-ty + (v-ey)*tx
		const headLen, headHalfW = 0.14, 0.085
		if p > -0.05 && p < headLen {
			t := math.Max(0, p) / headLen
			cov = math.Max(cov, aa(headHalfW*(1-t)-math.Abs(q)))
		}
	}

	// Rising arrow in the center.
	{
		const (
			tipY      = 0.30
			headBaseY = 0.56
			headHalfW = 0.155
			shaftHalf = 0.062
			bottomY   = 0.76
		)
		if v >= tipY && v <= headBaseY {
			t := (v - tipY) / (headBaseY - tipY)
			cov = math.Max(cov, aa(t*headHalfW-math.Abs(u-cx)))
		}
		if v >= headBaseY-0.02 && v <= bottomY {
			cov = math.Max(cov, aa(shaftHalf-math.Abs(u-cx)))
		}
	}

	return cov
}
