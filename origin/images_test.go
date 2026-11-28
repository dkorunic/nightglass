// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
	"golang.org/x/image/tiff"
)

func TestFileURL(t *testing.T) {
	got := FileURL("10.0.0.1", "/Images/M 42/live.jpg")
	if want := "http://10.0.0.1/SmartScope-1.0/dev2/Images/M%2042/live.jpg"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFetch(t *testing.T) {
	s := newFake(t)
	jpg, tif := fake.JPEG(1), fake.TIFF16()
	s.SetFile("live.jpg", jpg)
	s.SetFile("final.tiff", tif)
	s.SetFile("page.jpg", []byte("<html>oops</html>"))
	ctx := context.Background()

	f, err := Fetch(ctx, http.DefaultClient, s.Host(), ImageReady{FileLocation: "live.jpg"})
	if err != nil || f.DisplayType != "image/jpeg" || !bytes.Equal(f.Display, jpg) {
		t.Errorf("jpeg: %v %s", err, f.DisplayType)
	}

	f, err = Fetch(ctx, http.DefaultClient, s.Host(), ImageReady{FileLocation: "final.tiff"})
	if err != nil || f.DisplayType != "image/png" || !bytes.Equal(f.Original, tif) {
		t.Fatalf("tiff: %v %s", err, f.DisplayType)
	}
	img, err := png.Decode(bytes.NewReader(f.Display))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Errorf("tiff -> png: %v", err)
	}

	// A 404 page and an HTML body served as an image are errors, not frames.
	if _, err := Fetch(ctx, http.DefaultClient, s.Host(), ImageReady{FileLocation: "missing.jpg"}); err == nil {
		t.Error("404: want error")
	}
	if _, err := Fetch(ctx, http.DefaultClient, s.Host(), ImageReady{FileLocation: "page.jpg"}); !errors.Is(err, ErrNotImage) {
		t.Errorf("html body: err %v, want ErrNotImage", err)
	}
}

func TestLatestKeepsNewest(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []int
	done := make(chan struct{}, 4)
	l := NewLatest(func(v int) {
		if v == 1 {
			<-release
		}
		mu.Lock()
		got = append(got, v)
		mu.Unlock()
		done <- struct{}{}
	})
	l.Offer(1)
	time.Sleep(10 * time.Millisecond)
	l.Offer(2)
	l.Offer(3)
	close(release)
	<-done
	<-done
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("ran %v, want [1 3]", got)
	}
}

// With the app's post-processing off, a master is decoded unstretched.
func TestDecodePlain(t *testing.T) {
	info := ImageReady{ImageType: "FINAL_STACKED_MASTER"}
	tif := fake.TIFF16()
	stretched, err := Decode(info, tif)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecodePlain(info, tif)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stretched.Display, plain.Display) || plain.DisplayType != "image/png" || !bytes.Equal(plain.Original, tif) {
		t.Error("DecodePlain stretched the master, or lost it")
	}
}

// An Origin set to save its final image as PNG (ImageServer/SetFsmFormat
// "png") sends a 16-bit master: stretched as a 16-bit TIFF one is; an 8-bit
// PNG shows as it is.
func TestDecodePNGMaster(t *testing.T) {
	img, err := tiff.Decode(bytes.NewReader(fake.TIFF16()))
	if err != nil {
		t.Fatal(err)
	}
	var b16 bytes.Buffer
	if err := png.Encode(&b16, img); err != nil {
		t.Fatal(err)
	}
	info := ImageReady{ImageType: "FINAL_STACKED_MASTER"}
	stretched, err := Decode(info, b16.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecodePlain(info, b16.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stretched.Display, plain.Display) || stretched.DisplayType != "image/png" || !bytes.Equal(stretched.Original, b16.Bytes()) {
		t.Error("a 16-bit PNG master was not stretched, or was lost")
	}
	var p8 bytes.Buffer
	if err := png.Encode(&p8, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	b8 := p8.Bytes()
	if f, err := Decode(info, b8); err != nil || !bytes.Equal(f.Display, b8) {
		t.Errorf("8-bit PNG master changed: %v", err)
	}
}

// A JPEG 2000 final image (SetFsmFormat "jp2", as a JP2 file or a bare
// codestream) is recognised, so the caller can keep the file it cannot show.
func TestDecodeJPEG2000(t *testing.T) {
	for _, b := range [][]byte{
		{0, 0, 0, 0x0c, 'j', 'P', ' ', ' ', 0x0d, 0x0a, 0x87, 0x0a},
		{0xff, 0x4f, 0xff, 0x51, 0, 0x2f},
	} {
		if _, err := Decode(ImageReady{FileLocation: "FinalStackedMaster.jp2"}, b); !errors.Is(err, ErrJPEG2000) || !errors.Is(err, ErrNotImage) {
			t.Errorf("% x: err %v, want ErrJPEG2000", b[:4], err)
		}
	}
}
