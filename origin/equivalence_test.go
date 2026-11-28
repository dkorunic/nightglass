// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"slices"
	"testing"

	"golang.org/x/image/tiff"
)

// The image pipeline reads pixels without image.Image.At and skips work
// the old one repeated. These references are the plain At-based versions
// it replaced, kept as the oracle: the fast code must match them exactly.

func autoStretchRef(img image.Image, strength, background float64) *image.NRGBA {
	if strength < 0 {
		strength = 0.90
	}
	if background < 0 {
		background = 0.035
	}
	b := img.Bounds()
	n := b.Dx() * b.Dy()
	step := 100
	if n < 10000 {
		step = 1
	}
	var sample [3][]float64
	for i := 0; i < n; i += step {
		r, g, bl, _ := img.At(b.Min.X+i%b.Dx(), b.Min.Y+i/b.Dx()).RGBA()
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
	for c := range 3 {
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
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			var px [3]uint8
			for c, v := range [3]uint32{r, g, bl} {
				f := float64(v) / 0xffff
				if f > c0[c] {
					f = mtf((f-c0[c])/(1-c0[c]), m[c])
				} else {
					f = 0
				}
				px[c] = uint8(f*255 + 0.5)
			}
			out.SetNRGBA(x, y, color.NRGBA{px[0], px[1], px[2], 0xff})
		}
	}

	return out
}

// decodeRef is the old decode followed, when crop > 0, by the old CropBorder.
func decodeRef(info ImageReady, b []byte, stretch bool, crop int) (Frame, error) {
	f := Frame{Info: info, Original: b, Display: b}
	switch {
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		f.DisplayType = "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		f.DisplayType = pngType
		// A 16-bit PNG master (SetFsmFormat "png") is stretched as a TIFF one.
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || !stretch || (info.ImageType != "STACKED_MASTER" && info.ImageType != "FINAL_STACKED_MASTER") {
			break
		}
		switch img.(type) {
		case *image.Gray16, *image.RGBA64, *image.NRGBA64:
			var buf bytes.Buffer
			if err := png.Encode(&buf, autoStretchRef(img, orNeg(info.StretchStrength), orNeg(info.StretchBackground))); err != nil {
				return Frame{}, err
			}
			f.Display = buf.Bytes()
		}
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		img, err := tiff.Decode(bytes.NewReader(b))
		if err != nil {
			return Frame{}, err
		}
		switch img.(type) {
		case *image.Gray16, *image.RGBA64, *image.NRGBA64:
			if stretch && (info.ImageType == "STACKED_MASTER" || info.ImageType == "FINAL_STACKED_MASTER") {
				img = autoStretchRef(img, orNeg(info.StretchStrength), orNeg(info.StretchBackground))
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return Frame{}, err
		}
		f.Display, f.DisplayType = buf.Bytes(), pngType
	default:
		return Frame{}, ErrNotImage
	}
	if crop <= 0 {
		return f, nil
	}
	img, _, err := image.Decode(bytes.NewReader(f.Display))
	if err != nil {
		return f, err
	}
	bb := img.Bounds()
	if bb.Dx() <= 2*crop || bb.Dy() <= 2*crop {
		return f, nil
	}
	r := image.Rect(bb.Min.X+crop, bb.Min.Y+crop, bb.Max.X-crop, bb.Max.Y-crop)
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return f, err
	}
	f.Display, f.DisplayType = buf.Bytes(), pngType

	return f, nil
}

// testImages are the pixel types the pipeline meets, filled with noise,
// in sizes on both sides of AutoStretch's 1% sampling and of the 32 px
// crop, some with a non-zero origin (a SubImage).
func testImages(t *testing.T) map[string]image.Image {
	t.Helper()
	r := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // reproducible noise
	u16 := func(n int) uint16 { return uint16(r.IntN(n)) }
	out := map[string]image.Image{}
	for _, sz := range [][2]int{{1, 1}, {37, 23}, {64, 70}, {65, 66}, {150, 120}} {
		w, h := sz[0], sz[1]
		rect := image.Rect(0, 0, w, h)
		rgba64, nrgba64, nrgba64a := image.NewRGBA64(rect), image.NewNRGBA64(rect), image.NewNRGBA64(rect)
		rgba64a, g16, rgba, nrgba := image.NewRGBA64(rect), image.NewGray16(rect), image.NewRGBA(rect), image.NewNRGBA(rect)
		g8, cmyk := image.NewGray(rect), image.NewCMYK(rect)
		pal := image.NewPaletted(rect, color.Palette{color.Black, color.White, color.NRGBA{200, 10, 30, 255}, color.NRGBA{9, 80, 160, 255}})
		for y := range h {
			for x := range w {
				c := color.RGBA64{u16(9000), u16(14000), u16(65536), 0xffff}
				rgba64.SetRGBA64(x, y, c)
				nrgba64.SetNRGBA64(x, y, color.NRGBA64{c.R, c.G, c.B, 0xffff})
				a := uint16(0xffff)
				if x%5 == 0 {
					a = u16(65536)
				}
				nrgba64a.SetNRGBA64(x, y, color.NRGBA64{c.R, c.G, c.B, a})
				rgba64a.SetRGBA64(x, y, color.RGBA64{uint16(uint32(c.R) * uint32(a) / 0xffff), uint16(uint32(c.G) * uint32(a) / 0xffff), uint16(uint32(c.B) * uint32(a) / 0xffff), a})
				g16.SetGray16(x, y, color.Gray16{c.G})
				rgba.SetRGBA(x, y, color.RGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), 0xff})
				nrgba.SetNRGBA(x, y, color.NRGBA{uint8(c.B >> 8), uint8(c.G >> 8), uint8(c.R >> 8), uint8(a >> 8)})
				g8.SetGray(x, y, color.Gray{uint8(c.B >> 8)})
				cmyk.SetCMYK(x, y, color.CMYK{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), uint8(x)})
				pal.SetColorIndex(x, y, uint8(r.IntN(4)))
			}
		}
		var jb bytes.Buffer
		if err := jpeg.Encode(&jb, rgba, nil); err != nil {
			t.Fatal(err)
		}
		ycc, err := jpeg.Decode(&jb)
		if err != nil {
			t.Fatal(err)
		}
		for name, img := range map[string]image.Image{
			"RGBA64": rgba64, "NRGBA64": nrgba64, "NRGBA64alpha": nrgba64a, "RGBA64alpha": rgba64a, "Gray16": g16,
			"RGBA": rgba, "NRGBAalpha": nrgba, "Gray": g8, "CMYK": cmyk, "Paletted": pal, "YCbCr": ycc,
		} {
			out[fmt.Sprintf("%s-%dx%d", name, w, h)] = img
			if w > 2 && h > 2 {
				sub := img.(interface {
					SubImage(r image.Rectangle) image.Image
				}).SubImage(image.Rect(1, 2, w, h))
				out[fmt.Sprintf("%s-%dx%d-sub", name, w, h)] = sub
			}
		}
	}

	return out
}

func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	bb := a.Bounds()
	for y := bb.Min.Y; y < bb.Max.Y; y++ {
		for x := bb.Min.X; x < bb.Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				return false
			}
		}
	}

	return true
}

func TestAutoStretchMatchesReference(t *testing.T) {
	for name, img := range testImages(t) {
		for _, p := range [][2]float64{{-1, -1}, {0, 0}, {0.3, 0.2}, {1, 0.5}} {
			got, want := AutoStretch(img, p[0], p[1]), autoStretchRef(img, p[0], p[1])
			if got.Rect != want.Rect || !slices.Equal(got.Pix, want.Pix) {
				t.Errorf("%s %v: AutoStretch differs from the reference", name, p)
			}
		}
	}
}

// encodePNG writes the same bytes as png.Encode at the same level.
func TestEncodePNGMatchesPNGEncode(t *testing.T) {
	for name, img := range testImages(t) {
		for _, level := range []png.CompressionLevel{png.DefaultCompression, png.NoCompression} {
			var want bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: level}).Encode(&want, img); err != nil {
				t.Fatal(err)
			}
			got, err := encodePNG(img, level)
			if err != nil || !bytes.Equal(got, want.Bytes()) {
				t.Errorf("%s level %d: encodePNG differs from png.Encode (%v)", name, level, err)
			}
		}
	}
}

// Decode, DecodePlain and DecodeCrop show what the old decode and
// CropBorder showed, and CompressPNG makes the very file the old code
// saved from it.
func TestDecodeMatchesReference(t *testing.T) {
	half, fifth := 0.3, 0.2
	for name, img := range testImages(t) {
		if img.Bounds().Min != (image.Point{}) {
			continue // a file has no origin
		}
		var tb, jb, pb bytes.Buffer
		if err := tiff.Encode(&tb, img, nil); err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{"tiff": tb.Bytes()}
		if err := jpeg.Encode(&jb, img, nil); err == nil {
			files["jpeg"] = jb.Bytes()
		}
		if err := png.Encode(&pb, img); err == nil {
			files["png"] = pb.Bytes()
		}
		for kind, b := range files {
			// STACKED_MASTER takes FINAL_STACKED_MASTER's path, and the
			// Origin's stretch settings matter only to masters.
			for _, it := range []string{"FINAL_STACKED_MASTER", "LIVE"} {
				customs := []bool{false, true}
				if it == "LIVE" {
					customs = customs[:1]
				}
				for _, custom := range customs {
					info := ImageReady{ImageType: it, FileLocation: name}
					if custom {
						info.StretchStrength, info.StretchBackground = &half, &fifth
					}
					for _, mode := range []struct {
						fn      func() (Frame, error)
						stretch bool
						crop    int
					}{
						{func() (Frame, error) { return Decode(info, b) }, true, 0},
						{func() (Frame, error) { return DecodePlain(info, b) }, false, 0},
						{func() (Frame, error) { return DecodeCrop(info, b, 32) }, true, 32},
					} {
						if mode.crop > 0 && it == "LIVE" {
							// Only masters are cropped, and a 16-bit one is
							// stretched (opaque) first. Unstretched
							// semi-transparent 16-bit pixels may differ by
							// 1/255: the old crop went through a 16-bit
							// unpremultiplied PNG and rounded twice.
							continue
						}
						what := fmt.Sprintf("%s %s %s custom=%v stretch=%v crop=%d", name, kind, it, custom, mode.stretch, mode.crop)
						got, err := mode.fn()
						want, werr := decodeRef(info, b, mode.stretch, mode.crop)
						if (err != nil) != (werr != nil) {
							t.Fatalf("%s: error %v, reference %v", what, err, werr)
						}
						if err != nil {
							continue
						}
						if got.DisplayType != want.DisplayType || !bytes.Equal(got.Original, want.Original) {
							t.Fatalf("%s: type %s, reference %s", what, got.DisplayType, want.DisplayType)
						}
						if !got.Uncompressed {
							if !bytes.Equal(got.Display, want.Display) {
								t.Errorf("%s: display differs from the reference", what)
							}

							continue
						}
						saved, err := CompressPNG(got.Display)
						if err != nil || !bytes.Equal(saved, want.Display) {
							t.Errorf("%s: compressed display differs from the reference (%v)", what, err)
						}
						gi, _ := png.Decode(bytes.NewReader(got.Display))
						wi, _ := png.Decode(bytes.NewReader(want.Display))
						if gi == nil || wi == nil || !samePixels(gi, wi) {
							t.Errorf("%s: shown pixels differ from the reference", what)
						}
					}
				}
			}
		}
	}
}
