// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math"
	"strings"
	"testing"
)

// exifTags reads every tag of the PNG's eXIf chunk, following the Exif and
// GPS IFD pointers, as raw value bytes.
func exifTags(t *testing.T, b []byte) map[uint16][]byte {
	t.Helper()
	i := bytes.Index(b, []byte("eXIf"))
	if i < 0 {
		t.Fatal("no eXIf chunk")
	}
	if ihdr := bytes.Index(b, []byte("IHDR")); i != ihdr+4+13+4+4 {
		t.Errorf("eXIf at %d, want right after IHDR", i)
	}
	tiff := b[i+4 : i+4+int(binary.BigEndian.Uint32(b[i-4:]))]
	if string(tiff[:4]) != "MM\x00*" {
		t.Fatalf("TIFF header %q", tiff[:4])
	}
	be := binary.BigEndian
	size := map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 7: 1}
	out := map[uint16][]byte{}
	var walk func(off uint32)
	walk = func(off uint32) {
		n := int(be.Uint16(tiff[off:]))
		for k := range n {
			e := tiff[int(off)+2+12*k:]
			id, typ, count := be.Uint16(e), be.Uint16(e[2:]), be.Uint32(e[4:])
			l := size[typ] * int(count)
			v := e[8:12]
			if l > 4 {
				p := be.Uint32(e[8:])
				v = tiff[p : int(p)+l]
			}
			out[id] = v[:l]
			if id == 34665 || id == 34853 {
				walk(be.Uint32(v))
			}
		}
	}
	walk(be.Uint32(tiff[4:]))

	return out
}

func rationals(b []byte) []float64 {
	var out []float64
	for ; len(b) >= 8; b = b[8:] {
		out = append(out, float64(binary.BigEndian.Uint32(b))/float64(binary.BigEndian.Uint32(b[4:])))
	}

	return out
}

func TestPNGWithExif(t *testing.T) {
	var src bytes.Buffer
	_ = png.Encode(&src, image.NewGray16(image.Rect(0, 0, 4, 3)))
	info := ImageReady{
		Uuid: "6f1c-uuid", ExposureTime: 10.7, ISO: 200, StackDepth: 42,
		Date: "15 01 2026", Time: "21:30:05", Latitude: 45.815 * math.Pi / 180, Longitude: -15.982 * math.Pi / 180,
	}
	b, err := PNGWithExif(src.Bytes(), FinalMetadata(info, 10, 20))
	if err != nil {
		t.Fatal(err)
	}
	if img, err := png.Decode(bytes.NewReader(b)); err != nil || img.Bounds().Dx() != 4 {
		t.Fatalf("result does not decode: %v", err)
	}
	tags := exifTags(t, b)
	str := func(id uint16) string { return strings.TrimRight(string(tags[id]), "\x00") }
	short := func(id uint16, i int) int { return int(binary.BigEndian.Uint16(tags[id][2*i:])) }
	if got := str(42016); got != "6f1c-uuid" {
		t.Errorf("ImageUniqueID %q", got)
	}
	if got := rationals(tags[33434]); len(got) != 1 || got[0] != 10 { // the app writes whole seconds
		t.Errorf("ExposureTime %v", got)
	}
	if short(34855, 0) != 200 {
		t.Errorf("ISO %v", tags[34855])
	}
	if got := str(36867); got != "2026:01:15 21:30:05" {
		t.Errorf("DateTimeOriginal %q", got)
	}
	if short(42080, 0) != 3 || short(42081, 0) != 42 || short(42081, 1) != 42 {
		t.Errorf("composite tags %v %v", tags[42080], tags[42081])
	}
	if x := str(700); !strings.Contains(x, "<xapMM:DocumentID>6f1c-uuid</xapMM:DocumentID>") || !strings.HasPrefix(x, `<x:xmpmeta xmlns:x="adobe:ns:meta/" >`) {
		t.Errorf("XMP %q", x)
	}
	lat, lon := rationals(tags[2]), rationals(tags[4])
	if str(1) != "N" || str(3) != "W" || math.Abs(lat[0]+lat[1]/60+lat[2]/3600-45.815) > 1e-6 || math.Abs(lon[0]+lon[1]/60+lon[2]/3600-15.982) > 1e-6 {
		t.Errorf("GPS %s %v %s %v", str(1), lat, str(3), lon)
	}
}

// Without a position in the payload the site location is used, as the app
// uses the device's; with neither there is no GPS.
func TestFinalMetadataLocation(t *testing.T) {
	m := FinalMetadata(ImageReady{}, -33.87, 151.21)
	if !m.HasLocation || m.LatDeg != -33.87 || m.LonDeg != 151.21 {
		t.Errorf("site fallback: %+v", m)
	}
	if m := FinalMetadata(ImageReady{}, 0, 0); m.HasLocation {
		t.Errorf("no location: %+v", m)
	}
	if m := FinalMetadata(ImageReady{Date: "bad", Time: "21:00:00"}, 0, 0); !m.Taken.IsZero() {
		t.Errorf("bad date parsed: %v", m.Taken)
	}
}

func TestPNGWithExifRejectsNonPNG(t *testing.T) {
	if _, err := PNGWithExif([]byte("\xff\xd8\xff jpeg"), Metadata{}); err == nil {
		t.Error("JPEG accepted")
	}
}

// A snapshot carries only the location (BitmapWithMetadata with no
// metadata map): no XMP and no Exif IFD.
func TestPNGWithExifLocationOnly(t *testing.T) {
	var src bytes.Buffer
	_ = png.Encode(&src, image.NewGray(image.Rect(0, 0, 2, 2)))
	b, err := PNGWithExif(src.Bytes(), Metadata{LatDeg: -33.87, LonDeg: 151.21, HasLocation: true})
	if err != nil {
		t.Fatal(err)
	}
	tags := exifTags(t, b)
	for _, id := range []uint16{700, 34665, 42016, 33434} {
		if _, ok := tags[id]; ok {
			t.Errorf("tag %d written for a snapshot", id)
		}
	}
	if string(tags[1][:1]) != "S" || string(tags[3][:1]) != "E" {
		t.Errorf("GPS refs %q %q", tags[1], tags[3])
	}
}

func TestCopyExif(t *testing.T) {
	var a, b bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	_ = png.Encode(&a, img)
	_ = png.Encode(&b, img)
	src, err := PNGWithExif(a.Bytes(), Metadata{Uuid: "u", HasLocation: true, LatDeg: 45, LonDeg: 16})
	if err != nil {
		t.Fatal(err)
	}
	got, err := CopyExif(src, b.Bytes())
	if err != nil || !bytes.Equal(got, src) {
		t.Errorf("copied EXIF differs (%v)", err)
	}
	if got, _ := CopyExif(a.Bytes(), b.Bytes()); !bytes.Equal(got, b.Bytes()) {
		t.Error("no EXIF to copy changed the PNG")
	}
	if _, err := png.Decode(bytes.NewReader(got)); err != nil {
		t.Error(err)
	}
}

// The editor's adjustments go into UserComment; every other tag stays.
func TestSetUserComment(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 2)))
	final, err := PNGWithExif(buf.Bytes(), Metadata{Uuid: "run-1", ExposureSec: 10, ISO: 200, StackDepth: 5, HasLocation: true, LatDeg: 45.8, LonDeg: -16})
	if err != nil {
		t.Fatal(err)
	}
	before := exifTags(t, final)
	b, err := SetUserComment(final, `{"SkySafariAdjustments":{"version":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	after := exifTags(t, b)
	if got := string(after[tagUserComment]); got != "ASCII\x00\x00\x00"+`{"SkySafariAdjustments":{"version":1}}` {
		t.Errorf("UserComment %q", got)
	}
	for tag, v := range before {
		if tag == 34665 || tag == 34853 {
			continue // pointers move
		}
		if !bytes.Equal(after[tag], v) {
			t.Errorf("tag %d changed: %q -> %q", tag, v, after[tag])
		}
	}
	// Set again: replaced, not added.
	b, _ = SetUserComment(b, "x")
	if got := string(exifTags(t, b)[tagUserComment]); got != "ASCII\x00\x00\x00x" {
		t.Errorf("replaced UserComment %q", got)
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Error(err)
	}
	// A PNG without EXIF gets one with the comment.
	b, err = SetUserComment(buf.Bytes(), "y")
	if err != nil || string(exifTags(t, b)[tagUserComment]) != "ASCII\x00\x00\x00y" {
		t.Errorf("new EXIF: %v", err)
	}
}
