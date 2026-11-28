// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

// A dark 16-bit sky is stretched so its median lands near the background
// level, the shadows go black and the brightest pixel stays white.
func TestAutoStretch(t *testing.T) {
	img := image.NewGray16(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			v := uint16(1000 + (x*7+y*13)%200) // sky about 1.6% with noise
			img.SetGray16(x, y, color.Gray16{v})
		}
	}
	img.SetGray16(100, 100, color.Gray16{0xffff}) // a star
	img.SetGray16(0, 0, color.Gray16{0})          // a dead pixel
	out := AutoStretch(img, -1, -1)
	med := func() float64 {
		var v []float64
		for i := 0; i < len(out.Pix); i += 4 {
			v = append(v, float64(out.Pix[i])/255)
		}
		return median(v)
	}()
	if math.Abs(med-0.035) > 0.02 {
		t.Errorf("stretched median %.3f, want about the 0.035 background", med)
	}
	if out.NRGBAAt(100, 100).R != 255 || out.NRGBAAt(0, 0).R != 0 {
		t.Errorf("star %v, dead pixel %v", out.NRGBAAt(100, 100), out.NRGBAAt(0, 0))
	}
	if mtf(0.5, 0.5) != 0.5 || mtf(0, 0.2) != 0 || mtf(1, 0.2) != 1 {
		t.Error("midtones transfer function")
	}
}

// The auto-crop cuts 32 pixels off each edge; a tiny frame is left alone.
func TestCropBorder(t *testing.T) {
	enc := func(w, h int) Frame {
		var b bytes.Buffer
		_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)))
		return Frame{Display: b.Bytes(), DisplayType: "image/png"}
	}
	f, err := CropBorder(enc(200, 100), 32)
	if err != nil {
		t.Fatal(err)
	}
	if c, _, _ := image.DecodeConfig(bytes.NewReader(f.Display)); c.Width != 136 || c.Height != 36 {
		t.Errorf("cropped to %dx%d, want 136x36", c.Width, c.Height)
	}
	if f, _ := CropBorder(enc(50, 50), 32); f.Display == nil {
		t.Error("tiny frame lost")
	} else if c, _, _ := image.DecodeConfig(bytes.NewReader(f.Display)); c.Width != 50 {
		t.Errorf("tiny frame cropped to %d", c.Width)
	}
}
