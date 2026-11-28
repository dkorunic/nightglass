// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"image"
	"slices"
)

// AutoStretch is the standard screen transfer function stretch, which the
// app applies to masters deeper than 8 bits before showing or saving them:
// per channel, the shadows are clipped below the median plus a multiple of
// the (normalised) median absolute deviation, and a midtones transfer
// function puts the median at the background level. strength (0..1) sets
// the clipping, background (0..1) the target level; negative means the
// defaults of Celestron's web client, 0.90 and 0.035 (the Origin sends its
// own with each master). Statistics come from about 1% of the pixels.
func AutoStretch(img image.Image, strength, background float64) *image.NRGBA {
	if strength < 0 {
		strength = 0.90
	}
	if background < 0 {
		background = 0.035
	}
	b := img.Bounds()
	n := b.Dx() * b.Dy()
	step := 100 // about 1% of the pixels
	if n < 10000 {
		step = 1
	}
	at := rgbAt(img)
	var sample [3][]float64
	for i := 0; i < n; i += step {
		r, g, bl := at(b.Min.X+i%b.Dx(), b.Min.Y+i/b.Dx())
		sample[0] = append(sample[0], float64(r)/0xffff)
		sample[1] = append(sample[1], float64(g)/0xffff)
		sample[2] = append(sample[2], float64(bl)/0xffff)
	}
	var med, mad [3]float64
	for c := range 3 {
		med[c] = median(sample[c])
		dev := make([]float64, len(sample[c]))
		for i, v := range sample[c] {
			dev[i] = abs(v - med[c])
		}
		mad[c] = 1.4826 * median(dev)
	}
	for c := range 3 { // an empty channel borrows the other two
		if med[c] == 0 {
			med[c] = (med[(c+1)%3] + med[(c+2)%3]) / 2
			mad[c] = (mad[(c+1)%3] + mad[(c+2)%3]) / 2
		}
	}
	clip := min(-(1-strength)*3.8, -0.15)
	var c0, m [3]float64
	for c := range 3 {
		c0[c] = med[c] + mad[c]*clip
		m[c] = mtf(med[c]-c0[c], background)
	}
	// A channel's output depends only on its 16-bit value, so each
	// channel's curve is computed once into a table instead of per pixel.
	var lut [3][]uint8
	for c := range 3 {
		lut[c] = make([]uint8, 1<<16)
		for v := range lut[c] {
			f := float64(v) / 0xffff
			if f > c0[c] {
				f = mtf((f-c0[c])/(1-c0[c]), m[c])
			} else {
				f = 0
			}
			lut[c][v] = uint8(f*255 + 0.5)
		}
	}
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		p := out.Pix[out.PixOffset(b.Min.X, y):]
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl := at(x, y)
			p[0], p[1], p[2], p[3] = lut[0][r], lut[1][g], lut[2][bl], 0xff
			p = p[4:]
		}
	}

	return out
}

// rgbAt reads a pixel's red, green and blue as At(x, y).RGBA() does, without
// boxing a colour per call where the image has RGBA64At (every image type
// of the standard library): At cost 6.3 million allocations per master.
func rgbAt(img image.Image) func(x, y int) (r, g, b uint32) {
	if m, ok := img.(image.RGBA64Image); ok {
		return func(x, y int) (uint32, uint32, uint32) {
			c := m.RGBA64At(x, y)

			return uint32(c.R), uint32(c.G), uint32(c.B)
		}
	}

	return func(x, y int) (uint32, uint32, uint32) {
		r, g, b, _ := img.At(x, y).RGBA()

		return r, g, b
	}
}

// mtf is the midtones transfer function with balance m.
func mtf(x, m float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}

	return (m - 1) * x / ((2*m-1)*x - m)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)

	return s[len(s)/2]
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}

	return x
}
