// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// applyEditRef and rotateRef are the plain At-based versions applyEdit
// replaced, kept as the oracle: the fast code must match them exactly.
func applyEditRef(img image.Image, e Edit) *image.NRGBA {
	src := rotateRef(img, e.Rotate)
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

func rotateRef(img image.Image, n int) image.Image {
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
		for x := range w {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y))
			switch n {
			case 1:
				out.Set(h-1-y, x, c)
			case 2:
				out.Set(w-1-x, h-1-y, c)
			case 3:
				out.Set(y, w-1-x, c)
			}
		}
	}

	return out
}

// The editor's images are decoded files: PNG (RGBA, NRGBA, Gray, Gray16,
// RGBA64, NRGBA64, Paletted) and JPEG (YCbCr, Gray), so their colours are
// valid premultiplied ones; some are SubImages with a non-zero origin.
func TestApplyEditMatchesReference(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // reproducible noise
	var imgs []image.Image
	for _, sz := range [][2]int{{1, 1}, {2, 3}, {17, 9}, {64, 41}} {
		w, h := sz[0], sz[1]
		rect := image.Rect(0, 0, w, h)
		nrgba, g16, nrgba64 := image.NewNRGBA(rect), image.NewGray16(rect), image.NewNRGBA64(rect)
		pal := image.NewPaletted(rect, color.Palette{color.NRGBA{0, 0, 0, 0}, color.White, color.NRGBA{200, 10, 30, 128}})
		for y := range h {
			for x := range w {
				a := uint8(255)
				if x%3 == 0 {
					a = uint8(r.IntN(256))
				}
				nrgba.SetNRGBA(x, y, color.NRGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), a})
				g16.SetGray16(x, y, color.Gray16{uint16(r.IntN(65536))})
				nrgba64.SetNRGBA64(x, y, color.NRGBA64{uint16(r.IntN(65536)), uint16(r.IntN(65536)), uint16(r.IntN(65536)), uint16(a) * 0x101})
				pal.SetColorIndex(x, y, uint8(r.IntN(3)))
			}
		}
		var pb, jb bytes.Buffer
		for _, src := range []image.Image{nrgba, g16, nrgba64, pal} {
			pb.Reset()
			if err := png.Encode(&pb, src); err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(&pb)
			if err != nil {
				t.Fatal(err)
			}
			imgs = append(imgs, img)
		}
		opaque := image.NewRGBA(rect)
		draw.Draw(opaque, rect, nrgba, image.Point{}, draw.Src)
		for i := 3; i < len(opaque.Pix); i += 4 {
			opaque.Pix[i] = 0xff
		}
		pb.Reset()
		if err := png.Encode(&pb, opaque); err != nil {
			t.Fatal(err)
		}
		if img, err := png.Decode(&pb); err == nil {
			imgs = append(imgs, img) // *image.RGBA
		}
		if err := jpeg.Encode(&jb, opaque, nil); err != nil {
			t.Fatal(err)
		}
		if img, err := jpeg.Decode(&jb); err == nil {
			imgs = append(imgs, img) // *image.YCbCr
		}
	}
	for _, img := range slices.Clone(imgs) {
		if b := img.Bounds(); b.Dx() > 2 && b.Dy() > 2 {
			imgs = append(imgs, img.(interface {
				SubImage(r image.Rectangle) image.Image
			}).SubImage(image.Rect(1, 1, b.Max.X, b.Max.Y-1)))
		}
	}
	type crop = struct{ X, Y, W, H float64 }
	crops := []crop{{}, {0.1, 0.2, 0.5, 0.6}, {0.5, 0.5, 0.9, 0.9}, {2, 2, 1, 1}, {0, 0, 0.01, 0.01}}
	for i, img := range imgs {
		for rot := -1; rot <= 5; rot++ {
			for _, c := range crops {
				e := Edit{Brightness: 0.1, Contrast: 1.2, Saturation: 0.8, Red: 0.3, Green: -0.2, Blue: 0, Rotate: rot, Crop: c}
				what := fmt.Sprintf("%T %v rot %d crop %v", img, img.Bounds(), rot, c)
				got, want := applyEdit(img, e), applyEditRef(img, e)
				if got.Rect != want.Rect || !slices.Equal(got.Pix, want.Pix) {
					t.Errorf("image %d %s: applyEdit differs from the reference", i, what)
				}
			}
		}
	}
}
