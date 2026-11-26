// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // registers JPEG with image.Decode, for CropBorder
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/image/tiff"
)

// ErrNotImage is a download that is not JPEG, PNG or TIFF (such as an
// HTML error page).
var ErrNotImage = errors.New("origin: not an image")

const maxImage = 256 << 20

// pngType is the PNG media type, for frames shown as PNG.
const pngType = "image/png"

// FileURL is where the telescope serves a NewImageReady FileLocation.
func FileURL(host, location string) string {
	segs := strings.Split(strings.TrimPrefix(location, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}

	return "http://" + host + FilePath + strings.Join(segs, "/")
}

// Frame is one downloaded image. Display is JPEG or PNG for the GUI;
// Original is the file as served, so a TIFF master is saved as a TIFF.
// Uncompressed marks a Display PNG made here without compression, which
// shows at once (deflating a master took ~0.5 s); CompressPNG gives the
// file to save.
type Frame struct {
	DisplayType  string
	Display      []byte
	Original     []byte
	Info         ImageReady
	Uncompressed bool
}

// FetchFile downloads a file the telescope serves, such as a FileLocation
// or the DownloadLink of System/LogsReady.
func FetchFile(ctx context.Context, hc *http.Client, host, location string) ([]byte, error) {
	return FetchFileProgress(ctx, hc, host, location, nil)
}

// progressReader reports how much of total has been read.
type progressReader struct {
	r           io.Reader
	report      func(done, total int64)
	done, total int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	p.report(p.done, p.total)

	return n, err
}

// FetchFileProgress is FetchFile, calling progress (when not nil) as bytes
// arrive with the size the telescope announced (0 when unknown).
func FetchFileProgress(ctx context.Context, hc *http.Client, host, location string, progress func(done, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FileURL(host, location), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("origin: GET %s: %s", location, resp.Status)
	}
	var body io.Reader = resp.Body
	if progress != nil {
		body = &progressReader{r: resp.Body, total: max(resp.ContentLength, 0), report: progress}
	}
	b, err := io.ReadAll(io.LimitReader(body, maxImage+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxImage {
		return nil, fmt.Errorf("origin: %s larger than %d bytes", location, maxImage)
	}

	return b, nil
}

// Fetch downloads info.FileLocation from host and converts TIFF to PNG.
func Fetch(ctx context.Context, hc *http.Client, host string, info ImageReady) (Frame, error) {
	return FetchProgress(ctx, hc, host, info, nil)
}

// FetchProgress is Fetch with FetchFileProgress's progress reports.
func FetchProgress(ctx context.Context, hc *http.Client, host string, info ImageReady, progress func(done, total int64)) (Frame, error) {
	b, err := FetchFileProgress(ctx, hc, host, info.FileLocation, progress)
	if err != nil {
		return Frame{}, err
	}

	return Decode(info, b)
}

// Decode makes a Frame of a downloaded image: JPEG and PNG as they are,
// TIFF converted to PNG for display, a master stretched.
func Decode(info ImageReady, b []byte) (Frame, error) { return decode(info, b, true, 0) }

// DecodePlain is Decode without the masters' stretch, as the app decodes
// them with its post-processing off (ScopeAutomaticPostProcessImages).
func DecodePlain(info ImageReady, b []byte) (Frame, error) { return decode(info, b, false, 0) }

// DecodeCrop is Decode followed by CropBorder(f, px), for masters: a TIFF
// is cropped before its only PNG encode instead of decoding the PNG again.
func DecodeCrop(info ImageReady, b []byte, px int) (Frame, error) { return decode(info, b, true, px) }

func decode(info ImageReady, b []byte, stretch bool, crop int) (Frame, error) {
	f := Frame{Info: info, Original: b, Display: b}
	switch {
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		f.DisplayType = "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		f.DisplayType = pngType
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		img, err := tiff.Decode(bytes.NewReader(b))
		if err != nil {
			return Frame{}, fmt.Errorf("%w: %s: %w", ErrNotImage, info.FileLocation, err)
		}
		switch img.(type) {
		case *image.Gray16, *image.RGBA64, *image.NRGBA64:
			// A master deeper than 8 bits is stretched, as the app's
			// processUIImage does (only masters go through it).
			if stretch && (info.ImageType == "STACKED_MASTER" || info.ImageType == "FINAL_STACKED_MASTER") {
				img = AutoStretch(img, orNeg(info.StretchStrength), orNeg(info.StretchBackground))
			}
		}

		return shown(f, cropped(img, crop))
	default:
		return Frame{}, fmt.Errorf("%w: %s", ErrNotImage, info.FileLocation)
	}
	if crop > 0 {
		return CropBorder(f, crop)
	}

	return f, nil
}

// shown is f displaying img as an uncompressed PNG.
func shown(f Frame, img image.Image) (Frame, error) {
	b, err := encodePNG(img, png.NoCompression)
	if err != nil {
		return Frame{}, err
	}
	f.Display, f.DisplayType, f.Uncompressed = b, pngType, true

	return f, nil
}

// CompressPNG is a PNG made with png.Encode's default compression, as a
// Frame's Display is saved: byte for byte what encoding the image at that
// level makes in the first place.
func CompressPNG(b []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	return encodePNG(img, png.DefaultCompression)
}

// encodePNG is png.Encode at level. An opaque 16-bit RGB image goes
// through opaque16, which png encodes without allocating per pixel (1.06 s
// and 6.3 million allocations for a 3072×2048 master); the bytes are the
// same.
func encodePNG(img image.Image, level png.CompressionLevel) ([]byte, error) {
	switch m := img.(type) {
	case *image.RGBA64:
		if m.Opaque() {
			img = opaque16{m, m.Pix, m.Stride, m.Rect.Min}
		}
	case *image.NRGBA64:
		if m.Opaque() {
			img = opaque16{m, m.Pix, m.Stride, m.Rect.Min}
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// opaque16 is an opaque RGBA64 or NRGBA64 image (alike when opaque) whose
// At points into Pix: an interface holds the pointer without the
// allocation a color.RGBA64 value costs. png reads only At(x, y).RGBA().
type opaque16 struct {
	image.Image

	pix    []byte
	stride int
	min    image.Point
}

func (o opaque16) At(x, y int) color.Color {
	i := (y-o.min.Y)*o.stride + (x-o.min.X)*8

	return (*be16RGBA)(o.pix[i : i+8])
}

// Opaque spares png its own per-pixel opacity check.
func (o opaque16) Opaque() bool { return true }

// be16RGBA is one big-endian 16-bit RGBA pixel.
type be16RGBA [8]byte

func (p *be16RGBA) RGBA() (r, g, b, a uint32) {
	return uint32(p[0])<<8 | uint32(p[1]), uint32(p[2])<<8 | uint32(p[3]),
		uint32(p[4])<<8 | uint32(p[5]), uint32(p[6])<<8 | uint32(p[7])
}

// Latest runs fn on the newest offered value only: an Offer while fn runs
// replaces the waiting one, so a slow link shows the newest frame instead of
// working through a backlog.
type Latest[T any] struct {
	fn      func(T)
	pending *T
	mu      sync.Mutex
	busy    bool
}

// NewLatest returns a Latest calling fn.
func NewLatest[T any](fn func(T)) *Latest[T] { return &Latest[T]{fn: fn} }

// Offer queues v, replacing a value still waiting.
func (l *Latest[T]) Offer(v T) {
	l.mu.Lock()
	if l.busy {
		l.pending = &v
		l.mu.Unlock()

		return
	}
	l.busy = true
	l.mu.Unlock()
	go l.run(v)
}

func (l *Latest[T]) run(v T) {
	for {
		l.fn(v)
		l.mu.Lock()
		if l.pending == nil {
			l.busy = false
			l.mu.Unlock()

			return
		}
		v, l.pending = *l.pending, nil
		l.mu.Unlock()
	}
}

// orNeg is *p, or -1 (the default) when unset.
func orNeg(p *float64) float64 {
	if p == nil {
		return -1
	}

	return *p
}

// CropBorder cuts px pixels off every edge of a frame's display image, as
// the app's "Auto-Crop Images" does to masters; a frame too small to crop
// is returned as it is.
func CropBorder(f Frame, px int) (Frame, error) {
	img, _, err := image.Decode(bytes.NewReader(f.Display))
	if err != nil {
		return f, err
	}
	b := img.Bounds()
	if b.Dx() <= 2*px || b.Dy() <= 2*px {
		return f, nil
	}

	return shown(f, cropped(img, px))
}

// cropped is img without px pixels at every edge, as an NRGBA image (a
// SubImage when it is one already); img itself when too small to crop.
func cropped(img image.Image, px int) image.Image {
	b := img.Bounds()
	if px <= 0 || b.Dx() <= 2*px || b.Dy() <= 2*px {
		return img
	}
	r := image.Rect(b.Min.X+px, b.Min.Y+px, b.Max.X-px, b.Max.Y-px)
	if m, ok := img.(*image.NRGBA); ok {
		return m.SubImage(r)
	}
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)

	return out
}
