// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"

	"golang.org/x/image/tiff"
)

// Sky is a procedural star field (no catalogue): stars are fixed on the
// sky, seeded per 0.5° cell, so the field follows where the telescope
// points; the noise falls with the stack depth as 1/√depth. With Survey set
// the field is instead cut from that HiPS survey by CDS hips2fits (see
// survey.go), the stars standing in until it arrives. RA grows to the left
// (east) and Dec upwards, as on the real sky, before Rotation turns the field
// counter-clockwise.
type Sky struct {
	Survey    string  // HiPS survey ID, e.g. CDS/P/DSS2/color; empty for procedural stars
	RA, Dec   float64 // field centre, J2000 degrees
	Width     float64 // field width in degrees (height follows the image)
	Rotation  float64 // degrees, as an Origin's Orientation
	Blur      float64 // extra star blur (defocus), pixels
	W, H      int     // image size in pixels
	Depth     int     // frames stacked, 1 for a single frame
	NoiseSeed uint64  // varies the noise between frames
}

// The Origin's field of view and orientation, as the OriginSimulator
// project records them from a real Origin's NewImageReady (FovX 0.021894,
// FovY 0.014672, Orientation 3.120207 radians).
const (
	OriginField       = 1.2544 // degrees across
	OriginFieldY      = 0.8406 // degrees high
	OriginOrientation = 178.78 // degrees
)

const skyCell = 0.5 // degrees

// Height is the field's height in degrees.
func (s Sky) Height() float64 { return s.Width * float64(max(s.H, 1)) / float64(max(s.W, 1)) }

// Pixel is where ra, dec (degrees) falls in the image, in pixels.
func (s Sky) Pixel(ra, dec float64) (float64, float64) {
	u := -math.Remainder(ra-s.RA, 360) * math.Cos(dec*math.Pi/180)
	v := dec - s.Dec
	sin, cos := math.Sincos(s.Rotation * math.Pi / 180)
	scale := float64(max(s.W, 1)) / s.Width

	return float64(s.W)/2 + (u*cos-v*sin)*scale, float64(s.H)/2 - (u*sin+v*cos)*scale
}

// At is the sky (degrees) at fx, fy, fractions of the image's width and
// height from its top left: where RunCenterTarget's OffsetX, OffsetY point.
func (s Sky) At(fx, fy float64) (float64, float64) {
	up, vp := (fx-0.5)*s.Width, (0.5-fy)*s.Height()
	sin, cos := math.Sincos(s.Rotation * math.Pi / 180)
	u, v := -(up*cos + vp*sin), -up*sin+vp*cos
	dec := max(-90, min(90, s.Dec+v))

	return math.Mod(s.RA+u/max(math.Cos(dec*math.Pi/180), 0.01)+360, 360), dec
}

// Image renders the field as 16-bit RGB.
func (s Sky) Image() *image.RGBA64 {
	w, h := max(s.W, 1), max(s.H, 1)
	s.W, s.H = w, h
	if s.Survey != "" {
		if img := s.survey(w, h); img != nil {
			if s.Blur > 0.3 {
				img = blur(img, w, h, s.Blur)
			}

			return s.finish(img, w, h, 0)
		}
	}
	img := make([]float64, w*h*3)
	sigma := math.Hypot(max(0.8, 1.3*float64(w)/1200), s.Blur)
	reach := math.Hypot(s.Width, s.Height()) / 2 // any rotation stays within
	cosDec := max(math.Cos(s.Dec*math.Pi/180), 0.05)
	for cd := math.Floor((s.Dec - reach - skyCell) / skyCell); cd*skyCell <= s.Dec+reach+skyCell; cd++ {
		// The RA span covered at this declination band.
		c := max(math.Cos(math.Min(math.Abs(cd*skyCell), math.Abs(cd*skyCell+skyCell))*math.Pi/180), 0.05)
		span := min(reach/math.Min(c, cosDec)+skyCell, 180)
		for cr := math.Floor((s.RA - span) / skyCell); cr*skyCell <= s.RA+span; cr++ {
			ra0 := math.Mod(cr*skyCell+3600, 360)
			// Fewer stars per cell towards the poles, where RA cells shrink.
			r := rand.New(rand.NewPCG(uint64(int64(ra0/skyCell)), uint64(int64(cd)+1000))) //nolint:gosec // seeded on purpose: the same stars every time
			for range int(60 * c) {
				ra, dec := ra0+r.Float64()*skyCell, cd*skyCell+r.Float64()*skyCell
				flux := 0.015 / math.Pow(1-r.Float64()*0.999, 1.2) // many faint, few bright
				tint := [3]float64{1 + 0.3*r.NormFloat64(), 1, 1 - 0.3*r.NormFloat64()}
				x, y := s.Pixel(ra, dec)
				star(img, w, h, x, y, sigma, flux, tint)
			}
		}
	}

	return s.finish(img, w, h, 0.04)
}

// finish adds the sky background and the frame's noise to img.
func (s Sky) finish(img []float64, w, h int, background float64) *image.RGBA64 {
	noise := rand.New(rand.NewPCG(s.NoiseSeed, 7)) //nolint:gosec // reproducible noise
	sd := 0.01 / math.Sqrt(float64(max(s.Depth, 1)))
	out := image.NewRGBA64(image.Rect(0, 0, w, h))
	for i := range w * h {
		var px [3]uint16
		for ch := range 3 {
			v := background + img[i*3+ch] + sd*noise.NormFloat64()
			px[ch] = uint16(max(0, min(1, v)) * 65535)
		}
		out.SetRGBA64(i%w, i/w, color.RGBA64{px[0], px[1], px[2], 65535})
	}

	return out
}

// star adds a Gaussian star of total brightness flux at x, y.
func star(img []float64, w, h int, x, y, sigma, flux float64, tint [3]float64) {
	r := int(math.Ceil(4 * sigma))
	if x < -float64(r) || y < -float64(r) || x > float64(w+r) || y > float64(h+r) {
		return
	}
	peak := flux * 10 / sigma
	for py := max(0, int(y)-r); py <= min(h-1, int(y)+r); py++ {
		for px := max(0, int(x)-r); px <= min(w-1, int(x)+r); px++ {
			dx, dy := float64(px)+0.5-x, float64(py)+0.5-y
			v := peak * math.Exp(-(dx*dx+dy*dy)/(2*sigma*sigma))
			for ch := range 3 {
				img[(py*w+px)*3+ch] += v * tint[ch]
			}
		}
	}
}

// JPEG encodes the field, as live, snapshot and stacked frames arrive.
func (s Sky) JPEG() []byte {
	// The encoder reads an RGBA64 frame through At, an allocation per
	// pixel; it takes the high byte of each value, as this 8-bit copy holds.
	img := s.Image()
	rgba := &image.RGBA{Pix: make([]byte, len(img.Pix)/2), Stride: img.Stride / 2, Rect: img.Rect}
	for i := range rgba.Pix {
		rgba.Pix[i] = img.Pix[2*i]
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, rgba, &jpeg.Options{Quality: 85})

	return b.Bytes()
}

// TIFF encodes the field as 16-bit RGB, as final masters arrive.
func (s Sky) TIFF() []byte {
	var b bytes.Buffer
	_ = tiff.Encode(&b, s.Image(), nil)

	return b.Bytes()
}
