// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"maps"
	"math"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
)

// imagingTargetRef is imagingTarget before its Dec shortcut, kept as the
// oracle.
func (a *App) imagingTargetRef(r ImageRequest, raDeg, decDeg float64, now time.Time) (catalog.Target, bool) {
	near := func(t catalog.Target) float64 { return separation(raDeg, decDeg, t.RADeg, t.DecDeg) }
	if r.Name != "" {
		for _, t := range a.targets(r.Catalog, now) {
			if t.Name == r.Name && near(t) < fovRadius {
				return t, true
			}
		}
	}
	tier := func(l string, t catalog.Target) int {
		switch {
		case l == catalog.SolarSystem || l == "Messier" || l == "Caldwell":
			return 0
		case t.Type == "Duplicate":
			return 4
		case t.Type == "*" || t.Type == "**":
			return 2
		}

		return 1
	}
	var best catalog.Target
	bestTier, bestSep := 5, math.Inf(1)
	for _, l := range a.lists("") {
		for _, t := range a.targets(l, now) {
			d := near(t)
			if d >= fovRadius {
				continue
			}
			if k := tier(l, t); k < bestTier || k == bestTier && d < bestSep {
				best, bestTier, bestSep = t, k, d
			}
		}
	}

	return best, bestTier < 4
}

// The target found at a position is the same with the shortcut: on
// targets (several lists hold most), just inside and outside the field's
// radius in Dec and RA, at the poles and the RA wrap, and at random.
func TestImagingTargetMatchesReference(t *testing.T) {
	a, _ := newApp(t)
	fov := fovRadius * 180 / math.Pi
	type pos struct{ ra, dec float64 }
	var ps []pos
	i := 0
	for _, l := range a.cat.Lists() {
		for _, x := range a.cat.Get(l) {
			if i++; i%600 == 0 || l == "Messier" {
				ps = append(ps, pos{x.RADeg, x.DecDeg},
					pos{x.RADeg, x.DecDeg + fov*0.999}, pos{x.RADeg, x.DecDeg - fov*1.001},
					pos{x.RADeg + fov*0.999, x.DecDeg}, pos{x.RADeg + 2.5*fov, x.DecDeg})
			}
		}
	}
	r := rand.New(rand.NewPCG(7, 8)) //nolint:gosec // reproducible positions
	for range 150 {
		ps = append(ps, pos{r.Float64() * 360, r.Float64()*180 - 90})
	}
	ps = append(ps, pos{0, 90}, pos{0, -90}, pos{359.9999, 0}, pos{0.0001, 89.999}, pos{180, -89.9})
	for n, p := range ps {
		ra := math.Mod(p.ra+360, 360)
		dec := max(-90, min(90, p.dec))
		reqs := []ImageRequest{{}, {Catalog: "Messier", Name: "M 42"}}
		if n%10 != 0 { // the selection's own loop is not what changed
			reqs = reqs[:1]
		}
		for _, req := range reqs {
			gt, gok := a.imagingTarget(req, ra, dec, evening)
			wt, wok := a.imagingTargetRef(req, ra, dec, evening)
			if gt != wt || gok != wok {
				t.Fatalf("imagingTarget(%v, %.6f, %.6f) = %v %v, reference %v %v", req, ra, dec, gt.Name, gok, wt.Name, wok)
			}
		}
	}
}

// deleteImagesRef is DeleteImages before it wrote the settings once.
func (a *App) deleteImagesRef(names []string) error {
	var errs []error
	for _, n := range names {
		p, err := a.galleryPath(n)
		if err == nil {
			err = os.Remove(p)
		}
		if err != nil {
			errs = append(errs, err)

			continue
		}
		_ = a.SetFavorite(n, false)
		a.update(func(s *Settings) { delete(s.Images, n) })
	}
	if len(errs) > 0 {
		if len(names) == 1 {
			return fmt.Errorf("alert:Unable to Delete Image: %w", errors.Join(errs...))
		}

		return fmt.Errorf("alert:Unable to delete Images: %w", errors.Join(errs...))
	}

	return nil
}

// Deleting leaves the same files, favorites and records as before, for
// deletions that succeed, repeat, miss or are refused.
func TestDeleteImagesMatchesReference(t *testing.T) {
	setup := func() *App {
		a, _ := newApp(t)
		dir := a.Settings().SaveDir
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"a.png", "b.png", "c.jpg", "d.png"} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		a.update(func(s *Settings) {
			s.Favorites = []string{"b.png", "x.png", "a.png", "d.png"}
			s.Images = map[string]ImageRecord{"a.png": {Title: "A"}, "c.jpg": {Title: "C"}, "z.png": {Title: "Z"}}
		})

		return a
	}
	for _, names := range [][]string{
		nil,
		{"a.png"},
		{"a.png", "c.jpg"},
		{"a.png", "a.png"},
		{"missing.png"},
		{"../a.png", "b.png"},
		{"d.png", "b.png", "a.png", "c.jpg"},
		{"x.png", "z.png"},
	} {
		got, want := setup(), setup()
		gerr, werr := got.DeleteImages(names), want.deleteImagesRef(names)
		msg := func(a *App, err error) string { // the folders differ
			if err == nil {
				return ""
			}

			return strings.ReplaceAll(err.Error(), a.Settings().SaveDir, "DIR")
		}
		if msg(got, gerr) != msg(want, werr) || (gerr == nil) != (werr == nil) {
			t.Errorf("%v: error %v, reference %v", names, gerr, werr)
		}
		gs, ws := got.Settings(), want.Settings()
		if !slices.Equal(gs.Favorites, ws.Favorites) || !maps.Equal(gs.Images, ws.Images) {
			t.Errorf("%v: favorites %v images %v, reference %v %v", names, gs.Favorites, gs.Images, ws.Favorites, ws.Images)
		}
		left := func(a *App) []string {
			es, _ := os.ReadDir(a.Settings().SaveDir)
			var out []string
			for _, e := range es {
				out = append(out, e.Name())
			}

			return out
		}
		if l, r := left(got), left(want); !slices.Equal(l, r) {
			t.Errorf("%v: files %v, reference %v", names, l, r)
		}
		// What was written is what is kept.
		if s, err := LoadSettings(got.settingsPath); err != nil || !slices.Equal(s.Favorites, gs.Favorites) || !maps.Equal(s.Images, gs.Images) {
			t.Errorf("%v: saved %v %v, kept %v %v (%v)", names, s.Favorites, s.Images, gs.Favorites, gs.Images, err)
		}
	}
}

// Choosing the list already remembered does not write the settings again;
// another list is remembered and written.
func TestTargetsWritesOnlyChanges(t *testing.T) {
	a, _ := newApp(t)
	a.Targets("Messier")
	if err := os.Remove(a.settingsPath); err != nil {
		t.Fatal(err)
	}
	a.Targets("Messier")
	if _, err := os.Stat(a.settingsPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the same list wrote the settings again (%v)", err)
	}
	a.Targets("Caldwell")
	if s, err := LoadSettings(a.settingsPath); err != nil || s.Catalog != "Caldwell" || a.Settings().Catalog != "Caldwell" {
		t.Errorf("another list: saved %q, kept %q (%v)", s.Catalog, a.Settings().Catalog, err)
	}
}

// The gallery's Final follows each file as isFinal reads it, also after
// the file changes (an overwrite, or a final saved under a deleted name).
func TestGalleryFinalFollowsFiles(t *testing.T) {
	a, _ := newApp(t)
	dir := a.Settings().SaveDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	final, err := origin.PNGWithExif(tinyPNG(t), origin.Metadata{Uuid: "RUN"})
	if err != nil {
		t.Fatal(err)
	}
	plain := tinyPNG(t)
	check := func(when string) {
		t.Helper()
		g, err := a.Gallery()
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range g {
			if want := isFinal(filepath.Join(dir, x.Name)); x.Final != want {
				t.Errorf("%s: %s Final %v, file says %v", when, x.Name, x.Final, want)
			}
		}
	}
	write := func(name string, b []byte, mod time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	write("f.png", final, t0)
	write("p.png", plain, t0)
	check("first listing")
	check("cached listing")
	write("f.png", plain, t0.Add(time.Second)) // overwritten: no longer a final
	write("p.png", final, t0.Add(time.Second))
	check("after changes")
	// Deleted, then saved anew under the same name, size and time: only
	// the content tells, so the delete must forget what was read.
	write("f.png", final, t0)
	check("final again")
	write("f.png", plain, t0) // same time, other size
	check("same time, other size")
	write("f.png", final, t0)
	check("final once more")
	notFinal := bytes.Replace(final, []byte("xapMM:DocumentID"), []byte("xapMM:DocumentXX"), -1)
	write("f.png", notFinal, t0.Add(2*time.Second)) // same size, later
	check("same size, newer")
	write("f.png", final, t0)
	if err := a.DeleteImages([]string{"f.png"}); err != nil {
		t.Fatal(err)
	}
	write("f.png", notFinal, t0)
	check("after a delete")
}

// Thumbnails are kept on disk under their Uuid only: a name that is not
// a plain file name is neither stored nor served.
func TestThumbnailNames(t *testing.T) {
	a, _ := newApp(t)
	if err := a.storeThumb("t1", []byte("\xff\xd8\xffjpeg")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "../t1", "a/b", ".x"} {
		if err := a.storeThumb(bad, []byte("x")); err == nil {
			t.Errorf("stored %q", bad)
		}
	}
	for path, code := range map[string]int{"/thumb/t1": 200, "/thumb/..%2ft1": 404, "/thumb/t2": 404, "/thumb/": 404} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code {
			t.Errorf("%s: %d, want %d", path, w.Code, code)
		}
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}
