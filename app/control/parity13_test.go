// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkorunic/nightglass/origin"
)

// Change Target… (ImageMetaDataFragment.onTargetSelected) names a saved
// image after another object; the rest of its record and the file stay.
func TestChangeImageTarget(t *testing.T) {
	a, _ := newApp(t)
	dir := a.Settings().SaveDir
	for _, n := range []string{"M_42-1.png", "plain.png"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.update(func(s *Settings) {
		s.Images = map[string]ImageRecord{"M_42-1.png": {Title: "M 42", RaDeg: 83.8, Uuid: "run-1"}}
	})
	before := a.Settings().Images // a copy handed out earlier must not change

	if err := a.SetImageTarget("M_42-1.png", "NGC 1977"); err != nil {
		t.Fatal(err)
	}
	r := a.Settings().Images["M_42-1.png"]
	if r.Title != "NGC 1977" || r.RaDeg != 83.8 || r.Uuid != "run-1" {
		t.Errorf("record %+v", r)
	}
	if before["M_42-1.png"].Title != "M 42" {
		t.Error("an earlier settings copy changed")
	}
	// An image with no record gets one naming its target.
	if err := a.SetImageTarget("plain.png", " M 31 "); err != nil {
		t.Fatal(err)
	}
	list, _ := a.Gallery()
	for _, g := range list {
		if g.Name == "plain.png" && (g.Record == nil || g.Record.Title != "M 31") {
			t.Errorf("plain.png record %+v", g.Record)
		}
	}
	for _, c := range [][2]string{{"../x.png", "M 1"}, {"M_42-1.png", " "}, {"gone.png", "M 1"}} {
		if a.SetImageTarget(c[0], c[1]) == nil {
			t.Errorf("%q → %q accepted", c[0], c[1])
		}
	}
}

// Tonight's Sun & Moon card, at the site, for newApp's Zagreb evening of
// 15 January 2026 (PyEphem: sunset 16:37:49, dusk 18:23:46, dawn 05:47:07,
// sunrise 07:32:50, moonrise 06:13:47, moonset 13:09:33 CET; 8 %, 26.80 d).
func TestTonightCard(t *testing.T) {
	a, _ := newApp(t)
	got, err := a.Tonight()
	if err != nil {
		t.Fatal(err)
	}
	want := SunMoon{
		SunSet: "16:38", AstroDusk: "18:24", AstroDawn: "05:48", SunRise: "07:33",
		MoonRise: "06:14", MoonSet: "13:10",
		MoonPhase: "Waning Crescent", MoonText: "8% illuminated, 26.80 days old",
	}
	if got != want {
		t.Errorf("card\n got %+v\nwant %+v", got, want)
	}
	setSettings(t, a, func(s *Settings) { s.LatDeg, s.LonDeg = 0, 0 })
	if _, err := a.Tonight(); err == nil {
		t.Error("no location accepted")
	}
}

// The final image's status line is the app's (statusStringForOriginDefault)
// while its progress box carries the box texts (updateFinalMasterHUDPhase).
func TestFinalImageLineAndBox(t *testing.T) {
	pct := 42
	final := &origin.ImageReady{ImageType: "FINAL_STACKED_MASTER"}
	idle := task(t, `{"State":"IDLE"}`)
	wait := "Please wait. This can take several minutes."
	for _, c := range []struct {
		name      string
		in        statusIn
		line, box string
	}{
		{"requesting", statusIn{Connected: true, WaitingFinal: true, Task: idle}, "Requesting Final Image…", ""},
		{"downloading", statusIn{Connected: true, DownloadingFinal: true, FinalPercent: &pct, Task: idle}, "Downloading Final Image…", "Downloading Image — 42%\n" + wait},
		{"processing", statusIn{Connected: true, DownloadingFinal: true, Processing: final, Task: idle}, "Processing Final Image…", "Processing Image…\n" + wait},
		{
			"mosaic",
			statusIn{Connected: true, DownloadingFinal: true, Composition: true, Task: idle},
			"Downloading Final Image…",
			"Downloading Image — 0%\nPlease wait. Mosaic composition and download in progress, this can take several minutes.",
		},
		{"saving", statusIn{Connected: true, SavingFinal: true, Task: idle}, "Saving Final Image…", "Saving Image…"},
		{"stacked", statusIn{Connected: true, DownloadingStacked: &origin.ImageReady{StackDepth: 3}, Task: task(t, `{"State":"IMAGING_OBJECT"}`)}, "Downloading Image 3 (0.00 MB/s)…", ""},
	} {
		if got := statusText(c.in); got != c.line {
			t.Errorf("%s: line %q, want %q", c.name, got, c.line)
		}
		if got := finalBox(c.in); got != c.box {
			t.Errorf("%s: box %q, want %q", c.name, got, c.box)
		}
	}
}
