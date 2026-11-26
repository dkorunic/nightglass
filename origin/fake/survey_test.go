// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A survey field is resampled from one fetched tile, east on the left as on
// the real sky; procedural stars stand in until the tile arrives and when the
// service fails.
func TestSkySurvey(t *testing.T) {
	var hits, cancelled, gappy atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("hips") == "test/slow" { // answers only to a cancel
			<-r.Context().Done()
			cancelled.Add(1)

			return
		}
		if q := r.URL.Query(); q.Get("hips") == "test/gappy" { // west half missing
			if q.Get("format") != "png" {
				http.Error(w, "gaps need PNG", http.StatusBadRequest)

				return
			}
			gappy.Add(1)
			img := image.NewRGBA(image.Rect(0, 0, tilePx, tilePx))
			for i := 0; i < len(img.Pix); i += 4 {
				if (i/4)%tilePx < tilePx/2 {
					copy(img.Pix[i:], []byte{0, 220, 0, 255})
				} else {
					img.Pix[i+3] = 255
				}
			}
			_ = png.Encode(w, img)

			return
		}
		hits.Add(1)
		if q := r.URL.Query(); q.Get("hips") != "test/survey" && (q.Get("layer") != "ls-test" || q.Get("pixscale") != "3.75") {
			http.Error(w, "no such survey", http.StatusNotFound)

			return
		}
		// East (left) half red, west half blue.
		img := image.NewRGBA(image.Rect(0, 0, tilePx, tilePx))
		for i := 0; i < len(img.Pix); i += 4 {
			if (i/4)%tilePx < tilePx/2 {
				copy(img.Pix[i:], []byte{220, 0, 0, 255})
			} else {
				copy(img.Pix[i:], []byte{0, 0, 220, 255})
			}
		}
		_ = jpeg.Encode(w, img, &jpeg.Options{Quality: 100})
	}))
	defer srv.Close()
	old := HiPS2FITS
	oldLegacy := LegacyViewer
	HiPS2FITS, LegacyViewer = srv.URL, srv.URL
	defer func() { HiPS2FITS, LegacyViewer = old, oldLegacy }()
	reset := func() { // forget earlier runs' tiles; end the one fetch left
		surveyMu.Lock()
		if pendingStop != nil {
			pendingStop()
		}
		surveyTiles, pendingKey, pendingStop, failedKey = nil, "", nil, ""
		surveyMu.Unlock()
	}
	reset()
	defer reset() // before srv.Close, which waits for the request

	s := Sky{Survey: "test/survey", RA: 36.5, Dec: 62, Width: OriginField, W: 60, H: 40, Depth: 1 << 30}
	stars := s
	stars.Survey = ""
	if !bytes.Equal(s.JPEG(), stars.JPEG()) {
		t.Error("the first frame is not the procedural field")
	}
	var img *image.RGBA64
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if img = s.Image(); img.RGBA64At(5, 20).R > 40000 {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("no survey field: left pixel %v", img.RGBA64At(5, 20))
		}
	}
	if c := img.RGBA64At(55, 20); c.B < 40000 || c.R > 10000 {
		t.Errorf("west pixel %v, want blue", c)
	}
	// A drifted, turned field comes from the same tile, east now on the right.
	s.RA += 0.05
	s.Rotation = 180
	if img := s.Image(); img.RGBA64At(5, 20).B < 40000 || img.RGBA64At(55, 20).R < 40000 {
		t.Errorf("turned field: left %v, right %v", img.RGBA64At(5, 20), img.RGBA64At(55, 20))
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("%d fetches for one tile", n)
	}
	// A survey with gaps in the tile falls through to the next.
	s.Survey = "test/gappy,test/survey"
	for end := time.Now().Add(5 * time.Second); s.Image().RGBA64At(55, 20).R < 40000; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("no fallback field")
		}
	}
	if gappy.Load() != 1 || hits.Load() != 2 {
		t.Errorf("%d gappy, %d fallback fetches", gappy.Load(), hits.Load())
	}
	// The Legacy Survey viewer serves the same tiles, by layer and scale.
	s.Survey = "legacy:ls-test"
	for end := time.Now().Add(5 * time.Second); s.Image().RGBA64At(55, 20).R < 40000; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("no Legacy Survey field")
		}
	}
	s.Survey = ""
	stars = s
	s.Survey = "test/nonesuch"
	for range 3 {
		if !bytes.Equal(s.JPEG(), stars.JPEG()) {
			t.Error("a failing survey is not the procedural field")
		}
	}
	// A slew passing through fields cancels the fetch it no longer needs.
	s.Survey = "test/slow"
	s.Image()
	s.Dec += 5
	s.Image()
	for end := time.Now().Add(5 * time.Second); cancelled.Load() == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the passed field's fetch was not cancelled")
		}
	}
}
