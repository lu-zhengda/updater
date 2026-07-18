// Command genicon renders the Updater app icon (packaging/icon_1024.png):
// a rounded-square blue gradient with a white "update cycle" mark — a
// refresh ring, arrowhead on its tip, and a rising arrow in the center.
// The menu bar template glyph (cmd/updater/menubar_icon.go) draws the same
// mark at 22 px.
//
// Usage: go run ./scripts/genicon <output.png>
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 1024

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	const inset = 100.0
	const radius = 185.0
	top := [3]float64{46, 139, 247} // #2E8BF7
	bot := [3]float64{81, 69, 205}  // #5145CD

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var bg, fg float64
			for _, o := range [][2]float64{{0.25, 0.25}, {0.75, 0.25}, {0.25, 0.75}, {0.75, 0.75}} {
				x, y := float64(px)+o[0], float64(py)+o[1]
				bg += roundedRectCoverage(x, y, inset, size-inset, radius)
				fg += markCoverage(x/size, y/size)
			}
			bg /= 4
			fg /= 4
			if bg <= 0 {
				continue
			}
			t := (float64(py) - inset) / (size - 2*inset)
			r := top[0] + (bot[0]-top[0])*t
			g := top[1] + (bot[1]-top[1])*t
			b := top[2] + (bot[2]-top[2])*t
			if fg > 0 {
				r += (255 - r) * fg
				g += (255 - g) * fg
				b += (255 - b) * fg
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r), uint8(g), uint8(b), uint8(255 * bg)})
		}
	}

	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

// roundedRectCoverage returns antialiased coverage of the rounded square
// spanning [lo,hi] with the given corner radius.
func roundedRectCoverage(x, y, lo, hi, radius float64) float64 {
	if x < lo || x > hi || y < lo || y > hi {
		return 0
	}
	inCornerX := x < lo+radius || x > hi-radius
	inCornerY := y < lo+radius || y > hi-radius
	if !inCornerX || !inCornerY {
		return 1
	}
	cx := math.Min(math.Max(x, lo+radius), hi-radius)
	cy := math.Min(math.Max(y, lo+radius), hi-radius)
	d := math.Hypot(x-cx, y-cy)
	return math.Max(0, math.Min(1, radius-d+0.5))
}

// markCoverage returns coverage of the update mark for a point in unit
// coordinates (0..1 across the full icon). Shared shape definition with the
// menu bar glyph — see updateMarkCoverage.
func markCoverage(u, v float64) float64 {
	return updateMarkCoverage(u, v, 1.0/size)
}

// updateMarkCoverage draws the "update cycle" mark in unit space:
// center (0.5, 0.5), ring radius .265, a gap at the upper right with an
// arrowhead on the ring tip, and a rising arrow through the center.
// px is the size of one sample step in unit space (for edge antialiasing).
func updateMarkCoverage(u, v, px float64) float64 {
	const (
		cx, cy   = 0.5, 0.5
		ringR    = 0.265
		ringHalf = 0.037
		// Gap in the ring, degrees in image coords (y down), centered
		// on the upper-right diagonal.
		gapFrom = -80.0
		gapTo   = -20.0
	)

	dx, dy := u-cx, v-cy
	dist := math.Hypot(dx, dy)
	angle := math.Atan2(dy, dx) * 180 / math.Pi

	aa := func(d float64) float64 { // signed distance (>=0 inside) -> coverage
		return math.Max(0, math.Min(1, d/px+0.5))
	}

	var cov float64

	// Ring body (excluding the gap).
	inGap := angle > gapFrom && angle < gapTo
	if !inGap {
		cov = math.Max(cov, aa(ringHalf-math.Abs(dist-ringR)))
	}

	// Round cap at the lower gap edge only — the upper edge is covered by
	// the arrowhead.
	{
		rad := gapTo * math.Pi / 180
		ex, ey := cx+ringR*math.Cos(rad), cy+ringR*math.Sin(rad)
		cov = math.Max(cov, aa(ringHalf-math.Hypot(u-ex, v-ey)))
	}

	// Arrowhead on the ring tip at the top gap edge, pointing clockwise
	// into the gap — the classic refresh direction.
	{
		rad := gapFrom * math.Pi / 180
		ex, ey := cx+ringR*math.Cos(rad), cy+ringR*math.Sin(rad)
		// Clockwise tangent at the tip (image coords, y down).
		tx, ty := -math.Sin(rad), math.Cos(rad)
		// Local frame: p along travel direction, q across it.
		p := (u-ex)*tx + (v-ey)*ty
		q := (u-ex)*-ty + (v-ey)*tx
		const headLen, headHalfW = 0.095, 0.052
		if p > -0.04 && p < headLen {
			t := math.Max(0, p) / headLen
			cov = math.Max(cov, aa(headHalfW*(1-t)-math.Abs(q)))
		}
	}

	// Rising arrow in the center.
	{
		const (
			tipY      = 0.335
			headBaseY = 0.55
			headHalfW = 0.112
			shaftHalf = 0.042
			bottomY   = 0.705
		)
		// Head triangle.
		if v >= tipY && v <= headBaseY {
			t := (v - tipY) / (headBaseY - tipY)
			cov = math.Max(cov, aa(t*headHalfW-math.Abs(u-cx)))
		}
		// Shaft.
		if v >= headBaseY-0.02 && v <= bottomY {
			cov = math.Max(cov, aa(shaftHalf-math.Abs(u-cx)))
		}
	}

	return cov
}
