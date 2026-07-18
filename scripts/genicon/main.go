// Command genicon renders the Updater app icon (packaging/icon_1024.png):
// a rounded-square blue gradient with a white double chevron rising — the
// "upgrade" mark. The menu bar template glyph
// (cmd/updater/menubar_icon.go) draws the same mark at 22 px.
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
				fg += updateMarkCoverage(x/size, y/size, 1.0/size)
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
			// Subtle top glow for depth.
			dxc := (float64(px)/size - 0.5) / 0.9
			dyc := (float64(py)/size - 0.18) / 1.1
			glow := math.Max(0, 1-math.Hypot(dxc, dyc)) * 18
			r += glow
			g += glow
			b += glow
			if fg > 0 {
				r += (255 - r) * fg
				g += (255 - g) * fg
				b += (255 - b) * fg
			}
			img.SetNRGBA(px, py, color.NRGBA{
				uint8(math.Min(r, 255)), uint8(math.Min(g, 255)), uint8(math.Min(b, 255)), uint8(255 * bg)})
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

// updateMarkCoverage draws the double-chevron "upgrade" mark in unit space:
// a bright chevron rising over a faded one. px is one sample step in unit
// space (for edge antialiasing). Kept in sync with the menu bar glyph in
// cmd/updater/menubar_icon.go.
func updateMarkCoverage(u, v, px float64) float64 {
	const halfW = 0.062

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
	cov = math.Max(cov, aa(capsule(0.26, 0.47, 0.50, 0.28)))
	cov = math.Max(cov, aa(capsule(0.50, 0.28, 0.74, 0.47)))
	// Bottom chevron, faded — the version being left behind.
	cov = math.Max(cov, 0.5*aa(capsule(0.26, 0.72, 0.50, 0.53)))
	cov = math.Max(cov, 0.5*aa(capsule(0.50, 0.53, 0.74, 0.72)))
	return cov
}
