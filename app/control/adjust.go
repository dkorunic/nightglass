// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"image"
	"image/draw"
	"math"
)

// Edit is the app's image adjustments (ImageAdjustmentsFragment) in its own
// units: Brightness -0.5..0.5 (0 none), Contrast and Saturation 0.5..1.5 (1
// none), Red, Green and Blue -1..1 (0 none). Rotate is clockwise quarter
// turns, applied before Crop, a part of the rotated image (0..1 from its
// left and top); a zero Crop keeps all of it.
type Edit struct {
	Brightness, Contrast, Saturation float64
	Red, Green, Blue                 float64
	Rotate                           int
	Crop                             struct{ X, Y, W, H float64 }
}

// applyEdit rotates and crops img, then applies the tone controls and the
// colour curves, as the app's crop view and ImageFilterUtils.applyAllAdjustments.
func applyEdit(img image.Image, e Edit) *image.NRGBA {
	src := rotate(nrgba(img), e.Rotate)
	b := src.Bounds()
	if c := e.Crop; c.W > 0 && c.H > 0 {
		r := image.Rect(
			b.Min.X+int(math.Round(c.X*float64(b.Dx()))), b.Min.Y+int(math.Round(c.Y*float64(b.Dy()))),
			b.Min.X+int(math.Round((c.X+c.W)*float64(b.Dx()))), b.Min.Y+int(math.Round((c.Y+c.H)*float64(b.Dy()))),
		).Intersect(b)
		if !r.Empty() {
			b = r
		}
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	m := toneMatrix(e.Brightness, e.Contrast, e.Saturation)
	lr, lg, lb := rgbCurve(e.Red), rgbCurve(e.Green), rgbCurve(e.Blue)
	p := out.Pix
	for i := 0; i < len(p); i += 4 {
		r, g, bl := float64(p[i]), float64(p[i+1]), float64(p[i+2])
		p[i] = lr[clamp8(m[0]*r+m[1]*g+m[2]*bl+m[3])]
		p[i+1] = lg[clamp8(m[4]*r+m[5]*g+m[6]*bl+m[7])]
		p[i+2] = lb[clamp8(m[8]*r+m[9]*g+m[10]*bl+m[11])]
	}

	return out
}

// toneMatrix is applyToneControls' colour matrix, rows of r, g, b and an
// offset (0..255): Android's ColorMatrix.setSaturation, then contrast
// about mid-grey plus brightness. Each value is clamped as the app's.
func toneMatrix(brightness, contrast, saturation float64) [12]float64 {
	bri := max(-0.5, min(0.5, brightness))
	con := max(0.5, min(1.5, contrast))
	sat := max(0.5, min(1.5, saturation))
	inv := 1 - sat
	r, g, b := 0.213*inv, 0.715*inv, 0.072*inv // ColorMatrix.setSaturation's weights
	off := ((1-con)*0.5 + bri) * 255

	return [12]float64{
		con * (r + sat), con * g, con * b, off,
		con * r, con * (g + sat), con * b, off,
		con * r, con * g, con * (b + sat), off,
	}
}

// rgbCurve is buildRgbAdjustmentLut: a cubic through 0 and 1 whose slope at
// 0 is 2^f, brightening (f > 0) or darkening one channel.
func rgbCurve(f float64) [256]uint8 {
	k := math.Pow(2, max(-1, min(1, f)))
	a, c := 2-2*k, k-1
	var lut [256]uint8
	for i := range lut {
		x := float64(i) / 255
		lut[i] = uint8(math.Round(max(0, min(1, k*x+a*x*x+c*x*x*x)) * 255))
	}

	return lut
}

// clamp8 rounds a colour matrix result into 0..255, as a
// ColorMatrixColorFilter keeps its output in range.
func clamp8(v float64) uint8 { return uint8(math.Round(max(0, min(255, v)))) }

// nrgba is img as an NRGBA image, converted once when it is not one. The
// image/draw conversion matches color.NRGBAModel for every valid
// (premultiplied) colour, which is all a decoded file holds.
func nrgba(img image.Image) *image.NRGBA {
	if m, ok := img.(*image.NRGBA); ok {
		return m
	}
	b := img.Bounds()
	m := image.NewNRGBA(b)
	draw.Draw(m, b, img, b.Min, draw.Src)

	return m
}

// rotate turns img clockwise by n quarter turns, copying pixels: going
// through At and Set cost two allocations per pixel.
func rotate(img *image.NRGBA, n int) *image.NRGBA {
	n = ((n % 4) + 4) % 4
	if n == 0 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if n%2 == 1 {
		dw, dh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		row := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := range w {
			var d int
			switch n {
			case 1:
				d = out.PixOffset(h-1-y, x)
			case 2:
				d = out.PixOffset(w-1-x, h-1-y)
			default:
				d = out.PixOffset(y, w-1-x)
			}
			copy(out.Pix[d:d+4], row[4*x:4*x+4])
		}
	}

	return out
}
