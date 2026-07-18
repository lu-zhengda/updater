package main

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestGenerateIconIsValid22pxPNG(t *testing.T) {
	data := generateIcon()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("generateIcon returned invalid PNG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 22 || b.Dy() != 22 {
		t.Errorf("icon size = %dx%d, want 22x22", b.Dx(), b.Dy())
	}
	// Something must actually be drawn.
	opaque := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				opaque++
			}
		}
	}
	if opaque < 40 {
		t.Errorf("icon has only %d non-transparent pixels", opaque)
	}
	if out := os.Getenv("ICON_DUMP"); out != "" {
		_ = os.WriteFile(out, data, 0o644)
	}
}
