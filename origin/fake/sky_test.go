// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"bytes"
	"image"
	"math"
	"testing"

	"golang.org/x/image/tiff"
)

// diff is the mean absolute difference of the red channel where a's
// pixel x+dx matches b's pixel x.
func diff(a, b *image.RGBA64, dx int) float64 {
	var sum float64
	n := 0
	for y := range a.Rect.Dy() {
		for x := max(0, -dx); x+dx < a.Rect.Dx() && x < b.Rect.Dx(); x++ {
			sum += math.Abs(float64(a.RGBA64At(x+dx, y).R) - float64(b.RGBA64At(x, y).R))
			n++
		}
	}

	return sum / float64(n) / 65535
}

func TestSky(t *testing.T) {
	base := Sky{RA: 83.8, Dec: -5.4, Width: 1.2, W: 240, H: 160, Depth: 1, NoiseSeed: 1}
	if !bytes.Equal(base.JPEG(), base.JPEG()) {
		t.Error("the same field renders differently")
	}
	// The noise falls with the stack depth (1/√16 = 1/4).
	other := base
	other.NoiseSeed = 2
	single := diff(base.Image(), other.Image(), 0)
	base.Depth, other.Depth = 16, 16
	stacked := diff(base.Image(), other.Image(), 0)
	if single == 0 || stacked > single/3 {
		t.Errorf("frame-to-frame noise %.5f single, %.5f stacked", single, stacked)
	}
	// Stars stay on the sky: 0.24° east at 200 px/° moves them 48 px right.
	quiet := Sky{RA: 10, Dec: 0, Width: 1.2, W: 240, H: 160, Depth: 1 << 30}
	east := quiet
	east.RA += 0.24
	a, b := quiet.Image(), east.Image()
	if d := diff(a, b, -48); d > 1e-4 {
		t.Errorf("shifted field differs by %.6f", d)
	}
	if d := diff(a, b, 0); d < 1e-3 {
		t.Errorf("unshifted comparison too close (%.6f): no stars?", d)
	}
	// Final masters decode as 16-bit RGB.
	img, err := tiff.Decode(bytes.NewReader(quiet.TIFF()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := img.(*image.RGBA64); !ok || img.Bounds().Dx() != 240 {
		t.Errorf("TIFF decodes as %T %v", img, img.Bounds())
	}
}

// A field turned 180° is the same field upside down, and At undoes Pixel.
func TestSkyRotation(t *testing.T) {
	up := Sky{RA: 200, Dec: 40, Width: OriginField, W: 120, H: 80, Depth: 1 << 30}
	turned := up
	turned.Rotation = 180
	a, b := up.Image(), turned.Image()
	var worst float64
	for y := range 80 {
		for x := range 120 {
			worst = max(worst, math.Abs(float64(a.RGBA64At(x, y).R)-float64(b.RGBA64At(119-x, 79-y).R))/65535)
		}
	}
	if worst > 1e-3 {
		t.Errorf("turned field differs by %.5f", worst)
	}
	turned.Rotation = OriginOrientation
	for _, f := range [][2]float64{{0.5, 0.5}, {0.8, 0.3}, {0, 1}} {
		ra, dec := turned.At(f[0], f[1])
		if x, y := turned.Pixel(ra, dec); math.Abs(x-f[0]*120) > 1e-6 || math.Abs(y-f[1]*80) > 1e-6 {
			t.Errorf("At(%v) = %v/%v, back at %v/%v", f, ra, dec, x, y)
		}
	}
	if math.Abs(turned.Height()-OriginField*80/120) > 1e-12 {
		t.Errorf("height %v", turned.Height())
	}
}
