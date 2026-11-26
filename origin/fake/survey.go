// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // tiles arrive as JPEG
	_ "image/png"  // or PNG, where a gap must show
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HiPS2FITS is the CDS service that cuts a field out of a HiPS survey (the
// alaskybis mirror answers cold fields several times faster than alasky);
// tests replace it.
var HiPS2FITS = "https://alaskybis.cds.unistra.fr/hips-image-services/hips2fits"

// LegacyViewer is the DESI Legacy Survey viewer's cutout service, for
// -sky legacy:<layer> (ls-dr10, ls-dr9, unwise-neo7…; Dec above about −68°,
// avoiding the Milky Way); tests replace it.
var LegacyViewer = "https://www.legacysurvey.org/viewer/cutout.jpg"

// A survey is fetched as north-up tiles of tileFov degrees, tilePx pixels
// square (3.75″ a pixel, about the Origin's 3.76″), centred on a grid of
// tileStep degrees: any field within 0.75° of a grid point (the Origin's
// half-diagonal) lies inside one tile, so a drifting or turning field is
// resampled locally rather than fetched again (a cold fetch takes ~20 s).
const (
	tileFov  = 2.5
	tilePx   = 2400
	tileStep = 0.5
)

type surveyTile struct {
	img       *image.RGBA
	survey    string
	ra0, dec0 float64
}

var (
	surveyMu    sync.Mutex
	surveyTiles []*surveyTile // newest last, at most four (~23 MB each)
	pendingKey  string        // the one fetch running: a slew's newer field cancels it
	pendingStop context.CancelFunc
	failedKey   string // retried a minute after failing
	failedAt    time.Time
	surveyHTTP  = &http.Client{Timeout: 2 * time.Minute}
)

// survey is the field cut from s.Survey as 0–1 RGB, w×h×3, or nil until its
// tile has arrived (fetched in the background), when Image draws procedural
// stars instead.
func (s Sky) survey(w, h int) []float64 {
	t := s.tile()
	if t == nil {
		return nil
	}
	sd, cd := math.Sincos(t.dec0 * math.Pi / 180)
	scale := tilePx / tileFov * 180 / math.Pi // pixels a radian
	px := make([]float64, w*h*3)
	for y := range h {
		for x := range w {
			ra, dec := s.At((float64(x)+0.5)/float64(w), (float64(y)+0.5)/float64(h))
			// Gnomonic (TAN) projection about the tile centre, east left.
			sdec, cdec := math.Sincos(dec * math.Pi / 180)
			sra, cra := math.Sincos((ra - t.ra0) * math.Pi / 180)
			c := sd*sdec + cd*cdec*cra
			xi, eta := cdec*sra/c, (cd*sdec-sd*cdec*cra)/c
			bilinear(t.img, tilePx/2-xi*scale, tilePx/2-eta*scale, px[(y*w+x)*3:])
		}
	}

	return px
}

// tile is a fetched tile holding the whole field; without one it starts
// fetching the grid's nearest and returns nil.
func (s Sky) tile() *surveyTile {
	surveyMu.Lock()
	defer surveyMu.Unlock()
	for _, t := range surveyTiles {
		if t.survey == s.Survey && distance(t.ra0, t.dec0, s.RA, s.Dec) < tileFov/2-0.75 {
			return t
		}
	}
	dec0 := math.Round(s.Dec/tileStep) * tileStep
	step := tileStep / max(math.Cos(dec0*math.Pi/180), 0.1)
	ra0 := math.Mod(math.Round(s.RA/step)*step+360, 360)
	key := fmt.Sprintf("%s@%.5f%+.5f", s.Survey, ra0, dec0)
	if key == pendingKey || key == failedKey && time.Since(failedAt) < time.Minute {
		return nil
	}
	if pendingStop != nil {
		pendingStop()
	}
	ctx, stop := context.WithCancel(context.Background())
	pendingKey, pendingStop = key, stop
	log.Printf("sky survey: fetching %s tile at %.2f %+.2f", s.Survey, ra0, dec0)
	go func() {
		defer stop()
		img, err := fetchFirst(ctx, s.Survey, ra0, dec0)
		surveyMu.Lock()
		defer surveyMu.Unlock()
		if pendingKey == key {
			pendingKey, pendingStop = "", nil
		}
		switch {
		case ctx.Err() != nil: // superseded
		case err != nil:
			log.Printf("sky survey: %v (procedural stars meanwhile)", err)
			failedKey, failedAt = key, time.Now()
		default:
			surveyTiles = append(surveyTiles, &surveyTile{img: img, survey: s.Survey, ra0: ra0, dec0: dec0})
			if len(surveyTiles) > 4 {
				surveyTiles = surveyTiles[1:]
			}
		}
	}()

	return nil
}

// fetchFirst fetches the tile about ra0, dec0 from the first of the
// comma-separated surveys that covers all of it, e.g. Pan-STARRS (Dec above
// −30°) then DSS2. Every survey but the last is asked for PNG, where a gap
// is exact black: more than 1% of it falls through to the next (a covered
// tile has ~0.1%, one at the coverage edge 4%; JPEG crushes dark sky to
// black too, so it cannot tell).
func fetchFirst(ctx context.Context, surveys string, ra0, dec0 float64) (*image.RGBA, error) {
	list := strings.Split(surveys, ",")
	for i, survey := range list {
		last := i == len(list)-1
		img, err := fetchTile(ctx, tileURL(survey, ra0, dec0, !last))
		switch {
		case err != nil && (last || ctx.Err() != nil):
			return nil, err
		case err != nil:
			log.Printf("sky survey: %s: %v, trying the next", survey, err)
		case !last && blank(img) > 0.01:
			log.Printf("sky survey: %s does not cover %.2f %+.2f, trying the next", survey, ra0, dec0)
		default:
			if len(list) > 1 {
				log.Printf("sky survey: %.2f %+.2f from %s", ra0, dec0, survey)
			}

			return img, nil
		}
	}

	return nil, errors.New("no survey") // unreachable: the last one returns
}

// blank is the fraction of img that is exact black, a survey's gaps.
func blank(img *image.RGBA) float64 {
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i]|img.Pix[i+1]|img.Pix[i+2] == 0 {
			n++
		}
	}

	return float64(n) / float64(len(img.Pix)/4)
}

// tileURL asks for the tile about ra0, dec0: from the Legacy Survey viewer
// for "legacy:<layer>" (JPEG only), else from hips2fits, as PNG when png
// is set. Both answer TAN, north up, east left.
func tileURL(survey string, ra0, dec0 float64, png bool) string {
	q := url.Values{
		"width": {strconv.Itoa(tilePx)}, "height": {strconv.Itoa(tilePx)},
		"ra": {fmt.Sprintf("%.5f", ra0)}, "dec": {fmt.Sprintf("%.5f", dec0)},
	}
	if layer, ok := strings.CutPrefix(survey, "legacy:"); ok {
		q.Set("layer", layer)
		q.Set("pixscale", strconv.FormatFloat(tileFov*3600/tilePx, 'f', -1, 64))

		return LegacyViewer + "?" + q.Encode()
	}
	q.Set("hips", survey)
	q.Set("projection", "TAN")
	q.Set("fov", strconv.FormatFloat(tileFov, 'f', -1, 64))
	q.Set("format", "jpg")
	if png {
		q.Set("format", "png")
	}

	return HiPS2FITS + "?" + q.Encode()
}

// distance is the angle between two sky positions, degrees.
func distance(ra1, dec1, ra2, dec2 float64) float64 {
	const d = math.Pi / 180
	c := math.Sin(dec1*d)*math.Sin(dec2*d) + math.Cos(dec1*d)*math.Cos(dec2*d)*math.Cos((ra1-ra2)*d)

	return math.Acos(max(-1, min(1, c))) / d
}

func fetchTile(ctx context.Context, u string) (*image.RGBA, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := surveyHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tile: %s", resp.Status)
	}
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tile: %w", err)
	}
	if b := img.Bounds(); b.Dx() != tilePx || b.Dy() != tilePx {
		return nil, fmt.Errorf("tile: %v image, asked %d square", b, tilePx)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, tilePx, tilePx))
	draw.Draw(rgba, rgba.Rect, img, img.Bounds().Min, draw.Src)

	return rgba, nil
}

// bilinear writes the RGB of img at x, y (pixels, edges clamped) into out.
func bilinear(img *image.RGBA, x, y float64, out []float64) {
	x, y = x-0.5, y-0.5 // pixel centres
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	n := img.Rect.Dx() - 1
	at := func(px, py float64) []uint8 {
		i := img.PixOffset(min(n, max(0, int(px))), min(n, max(0, int(py))))

		return img.Pix[i : i+3]
	}
	a, b, c, d := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
	for ch := range 3 {
		top := float64(a[ch])*(1-fx) + float64(b[ch])*fx
		bot := float64(c[ch])*(1-fx) + float64(d[ch])*fx
		out[ch] = (top*(1-fy) + bot*fy) / 255
	}
}

// blur smooths an RGB w×h×3 image by a Gaussian of sigma pixels, the
// focuser's defocus on a survey field; it returns a new slice.
func blur(px []float64, w, h int, sigma float64) []float64 {
	r := int(math.Ceil(3 * sigma))
	k := make([]float64, 2*r+1)
	var sum float64
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	pass := func(src []float64, dx, dy int) []float64 {
		dst := make([]float64, len(src))
		for y := range h {
			for x := range w {
				for i, kv := range k {
					sx, sy := min(w-1, max(0, x+(i-r)*dx)), min(h-1, max(0, y+(i-r)*dy))
					for ch := range 3 {
						dst[(y*w+x)*3+ch] += src[(sy*w+sx)*3+ch] * kv / sum
					}
				}
			}
		}

		return dst
	}

	return pass(pass(px, 1, 0), 0, 1)
}
