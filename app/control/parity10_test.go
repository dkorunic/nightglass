// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// The slew pad works only while the Origin is idle; a stop always goes out.
func TestSlewOnlyWhileIdle(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "INITIALIZING"})
	if err := a.Slew(4, 0); !errors.Is(err, errBusy) {
		t.Errorf("slew while initializing: %v", err)
	}
	if len(received(s, "Mount", "Slew")) != 0 {
		t.Error("slew sent while busy")
	}
	if err := a.StopSlew(); err != nil {
		t.Errorf("stop: %v", err)
	}
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	if err := a.Slew(4, 0); err != nil {
		t.Errorf("slew while idle: %v", err)
	}
	_ = a.StopSlew()
}

// "Don't Warn Again" forgets the offered runs, not the one in progress.
func TestDontWarnAgainKeepsCurrentRun(t *testing.T) {
	a, rec, s := connectedTo(t, func(s *fake.Server) {
		s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": "now"}})
		s.SetHandle(func(req fake.Msg) fake.Msg {
			r := s.DefaultHandle(req)
			if fake.Is("ImageServer", "GetAvailableStackedMasters")(req) {
				r["AvailableStackedMasters"] = fake.Msg{"old": "Images/old"}
			}

			return r
		})
	})
	a.remember("old")
	a.remember("gone") // no longer on the Origin: forgotten as well
	a.remember("now")
	if err := a.FilesOpened(); err != nil { // asks again, as the file manager does
		t.Fatal(err)
	}
	rec.wait(t, "orphans", func(d any) bool { return slices.Equal(d.(Orphans).Uuids, []string{"old"}) })
	lastSent(t, s, "Disk", "GetStatus")
	a.ForgetPending("")
	if got := a.Settings().PendingImages; !slices.Equal(got, []string{"now"}) {
		t.Errorf("pending %v, want the current run kept", got)
	}
}

// The recommendation's Run sends PolarAlign without waiting for idle.
func TestPolarAlignNow(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "INITIALIZING", "Stage": "COMPLETE"})
	if a.PolarAlign() == nil {
		t.Error("settings' polar alignment while busy")
	}
	if err := a.PolarAlignNow(); err != nil {
		t.Fatal(err)
	}
	lastSent(t, s, "TaskController", "PolarAlign")
}

// The imaging screen's manual mode summary and snapshot label.
func TestManualAndSnapshotLabels(t *testing.T) {
	for _, c := range []struct {
		iso  int
		exp  float64
		want string
	}{
		{500, 10, "Manual Mode: ISO 500, Exposure 10.0s"},
		{200, 0.01, "Manual Mode: ISO 200, Exposure 0.0s"},
		{200, 0.005, "Manual Mode: ISO 200, Exposure 5.0ms"},
		{100, 0.00005, "Manual Mode: ISO 100, Exposure 50.0μs"},
	} {
		if got := manualText(c.iso, c.exp); got != c.want {
			t.Errorf("manualText(%d, %v) = %q, want %q", c.iso, c.exp, got, c.want)
		}
	}
	a, _, _ := connected(t)
	if a.Activity().Manual != "" {
		t.Error("manual label in automatic mode")
	}
	setSettings(t, a, func(st *Settings) { st.ManualImaging, st.ExposureSec, st.ISO = true, 20, 0 })
	if got := a.Activity().Manual; got != "Manual Mode: ISO 200, Exposure 20.0s" {
		t.Errorf("manual label %q", got)
	}
	setSettings(t, a, func(st *Settings) { st.Snapshot = true })
	if got := a.Activity().Target; got != "⬤  Snapshot Mode" {
		t.Errorf("snapshot label %q", got)
	}
}

// The final image's download shows its progress, as the app's progress box.
func TestFinalDownloadProgress(t *testing.T) {
	pct := 42
	in := statusIn{Connected: true, DownloadingFinal: true, FinalPercent: &pct, Task: task(t, `{"State":"IDLE"}`)}
	if got := finalBox(in); got != "Downloading Image — 42%\nPlease wait. This can take several minutes." {
		t.Errorf("box %q", got)
	}
	if progress(in) != 0.42 {
		t.Errorf("progress %v", progress(in))
	}
}

// Center Here blanks the image until the new place's frames arrive.
func TestCenterHereBlanks(t *testing.T) {
	a, rec, s := connected(t)
	s.SetFile("live/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" })
	if err := a.CenterHere(0.3, 0.6); err != nil {
		t.Fatal(err)
	}
	if got := a.Activity().Image; got != "" {
		t.Errorf("image %q still shown", got)
	}
}

// Speeds measured while the Origin downloads an update are left out.
func TestSpeedsSkippedDuringUpdate(t *testing.T) {
	a, rec, s := connected(t)
	s.Notify("System", "UpdateStatus", fake.Msg{"Status": "UPDATE_DOWNLOADING", "PercentComplete": 10})
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, ok := c.Last("System/UpdateStatus"); ok {
			break
		}
	}
	s.SetFile("live/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" })
	a.mu.Lock()
	n := a.samples
	a.mu.Unlock()
	if n != 0 {
		t.Errorf("%d speed samples during an update download", n)
	}
	if strings.Contains(a.Activity().Text, "Bandwidth") {
		t.Error("bandwidth warning")
	}
}

// The app connects only with a location.
func TestConnectNeedsLocation(t *testing.T) {
	a, _ := newApp(t)
	st := a.Settings()
	st.LatDeg, st.LonDeg = 0.001, -0.002 // within 1e-4 rad of 0°, 0°
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := a.Connect("127.0.0.1:1"); err == nil || !strings.HasPrefix(err.Error(), "Precise Location Unavailable:") {
		t.Errorf("connect without a location: %v", err)
	}
	if _, err := a.cl(); err == nil {
		t.Error("a client was started")
	}
}

// The shown frame can be saved; factory darks and flats restored while idle.
func TestSaveShownAndRestore(t *testing.T) {
	a, rec, s := connected(t)
	if _, err := a.SaveShown(); err == nil {
		t.Error("saved with nothing shown")
	}
	s.SetFile("live/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" })
	p, err := a.SaveShown()
	if err != nil || !strings.Contains(p, "Live-") {
		t.Errorf("saved %q, %v", p, err)
	}
	if err := a.RestoreFactoryDarks(); err != nil {
		t.Fatal(err)
	}
	if err := a.RestoreFactoryFlats(); err != nil {
		t.Fatal(err)
	}
	lastSent(t, s, "TaskController", "RestoreDarksToFactory")
	lastSent(t, s, "TaskController", "RestoreFlatsToFactory")
	setTask(t, a, s, fake.Msg{"State": "FOCUSING"})
	if a.RestoreFactoryDarks() == nil {
		t.Error("restored while busy")
	}
}
