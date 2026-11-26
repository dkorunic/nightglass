// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dkorunic/nightglass/origin"
)

// The app's ImageFilterUtils, worked by hand.
func TestApplyEditLikeApp(t *testing.T) {
	px := func(c color.NRGBA, e Edit) color.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, c)

		return applyEdit(img, e).NRGBAAt(0, 0)
	}
	none := Edit{Contrast: 1, Saturation: 1}
	grey := color.NRGBA{200, 200, 200, 255}
	if got := px(grey, none); got != grey {
		t.Errorf("no adjustment: %v", got)
	}
	// Contrast 1.5: 1.5·200 + (1-1.5)·0.5·255 = 236.25.
	if got := px(grey, Edit{Contrast: 1.5, Saturation: 1}); got.R != 236 {
		t.Errorf("contrast: %v", got)
	}
	// Brightness +0.5 adds 127.5, clamped at 255; out-of-range values are clamped first.
	if got := px(grey, Edit{Brightness: 3, Contrast: 1, Saturation: 1}); got.R != 255 {
		t.Errorf("brightness: %v", got)
	}
	// Saturation 0.5 on red: (0.213·0.5 + 0.5)·255 = 154.66, 0.213·0.5·255 = 27.16.
	if got := px(color.NRGBA{255, 0, 0, 255}, Edit{Contrast: 1, Saturation: 0.5}); got != (color.NRGBA{155, 27, 27, 255}) {
		t.Errorf("saturation: %v", got)
	}
	// Curves at x = 128/255: Red +1 → 2x - 2x² + x³ = 0.6265 (159.75), Blue -1 → x/2 + x² - x³/2 = 0.4397 (112.1).
	mid := color.NRGBA{128, 128, 128, 255}
	if got := px(mid, Edit{Contrast: 1, Saturation: 1, Red: 1, Blue: -1}); got.R != 160 || got.G != 128 || got.B != 112 {
		t.Errorf("curves: %v", got)
	}
	// Rotation then crop: [red blue] turned clockwise is red over blue; the bottom half is blue.
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 255})
	img.SetNRGBA(1, 0, color.NRGBA{0, 0, 255, 255})
	e := none
	e.Rotate = 1
	if r := applyEdit(img, e); r.Bounds().Dx() != 1 || r.NRGBAAt(0, 0).R != 255 || r.NRGBAAt(0, 1).B != 255 {
		t.Errorf("rotate: %v", r.Pix)
	}
	e.Crop.Y, e.Crop.W, e.Crop.H = 0.5, 1, 0.5
	if r := applyEdit(img, e); r.Bounds().Dy() != 1 || r.NRGBAAt(0, 0).B != 255 {
		t.Errorf("crop: %v", r.Pix)
	}
}

// The gallery lists, marks, overwrites (keeping EXIF), exports and deletes
// the save folder's images; names never leave the folder.
func TestGallery(t *testing.T) {
	a, _ := newApp(t)
	dir := a.Settings().SaveDir
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for i := range img.Pix {
		img.Pix[i] = 100
	}
	_ = png.Encode(&buf, img)
	final, _ := origin.PNGWithExif(buf.Bytes(), origin.FinalMetadata(origin.ImageReady{Uuid: "run-1"}, 45, 16))
	_ = os.WriteFile(filepath.Join(dir, "M_42-1.png"), final, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	list, err := a.Gallery()
	if err != nil || len(list) != 1 || list[0].Name != "M_42-1.png" || !list[0].Final || list[0].Favorite {
		t.Fatalf("gallery %v %v", list, err)
	}
	if err := a.SetFavorite("M_42-1.png", true); err != nil {
		t.Fatal(err)
	}
	if list, _ = a.Gallery(); !list[0].Favorite {
		t.Error("favorite not kept")
	}
	for _, bad := range []string{"../x.png", `..\x.png`, "notes.txt", ".png"} {
		if a.SetFavorite(bad, true) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/saved/M_42-1.png", nil))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), final) {
		t.Errorf("served %d", w.Code)
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/saved/..%2Fsettings.json", nil))
	if w.Code != 404 {
		t.Errorf("outside the folder: %d", w.Code)
	}

	e := Edit{Contrast: 1, Saturation: 1, Rotate: 1}
	if err := a.OverwriteImage("M_42-1.png", e); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "M_42-1.png"))
	if !bytes.Contains(b, []byte(`{"SkySafariAdjustments":{"version":1,"brightness":0,"contrast":1,"saturation":1,"red":0,"green":0,"blue":0}}`)) {
		t.Error("adjustments not recorded in EXIF UserComment")
	}
	got, err := png.Decode(bytes.NewReader(b))
	if err != nil || got.Bounds().Dx() != 2 || got.Bounds().Dy() != 4 || !isFinal(filepath.Join(dir, "M_42-1.png")) {
		t.Errorf("overwritten: %v %v, EXIF kept %v", got.Bounds(), err, isFinal(filepath.Join(dir, "M_42-1.png")))
	}
	copyName, err := a.ExportImage("M_42-1.png", Edit{Contrast: 1, Saturation: 1})
	if err != nil || copyName != "Nightglass_1768510800000.jpg" {
		t.Fatalf("export %q %v", copyName, err)
	}
	// As the app's saveEditedImageToUri: the original's EXIF and the
	// adjustments, in a JPEG.
	jb, _ := os.ReadFile(filepath.Join(dir, copyName))
	if x := origin.ExifOf(jb); x == nil || !bytes.Contains(x, []byte(`"contrast":1`)) || !bytes.Contains(x, []byte("run-1")) {
		t.Errorf("exported JPEG EXIF %q", x)
	}
	if _, err := jpeg.Decode(bytes.NewReader(jb)); err != nil {
		t.Errorf("exported JPEG does not decode: %v", err)
	}
	if err := a.DeleteImages([]string{"M_42-1.png", copyName}); err != nil {
		t.Fatal(err)
	}
	if list, _ = a.Gallery(); len(list) != 0 || slices.Contains(a.Settings().Favorites, "M_42-1.png") {
		t.Errorf("after delete: %v, favorites %v", list, a.Settings().Favorites)
	}
	if err := a.DeleteImages([]string{"gone.png"}); err == nil {
		t.Error("deleting a missing image")
	}
}
