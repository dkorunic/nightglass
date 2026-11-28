// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

func setSettings(t *testing.T, a *App, f func(*Settings)) {
	t.Helper()
	s := a.Settings()
	f(&s)
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
}

// waitTask waits until the client has seen TaskController State st.
func waitTask(t *testing.T, a *App, st string) {
	t.Helper()
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); c.TaskState() != st; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("state %q, want %q", c.TaskState(), st)
		}
	}
}

func setTask(t *testing.T, a *App, s *fake.Server, fields fake.Msg) {
	t.Helper()
	s.SetStatus("TaskController", fields)
	s.NotifyStatus("TaskController")
	waitTask(t, a, fields["State"].(string))
}

// reportForced plays the Origin reporting the state a capture forced
// (forcedTempStatusForUI), then idle again, so the next capture is allowed.
func reportForced(t *testing.T, a *App, s *fake.Server) {
	t.Helper()
	a.mu.Lock()
	st := a.forced
	a.mu.Unlock()
	if st == "" {
		return
	}
	setTask(t, a, s, fake.Msg{"State": st, "IsReady": true})
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	// setTask waits for the client; the forced state ends when forward has
	// taken the report too, which may come a moment later.
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		a.mu.Lock()
		f := a.forced
		a.mu.Unlock()
		if f == "" {
			return
		}
	}
	t.Fatalf("forced state %q not ended", st)
}

// P: in snapshot mode the capture button takes a snapshot
// (RunSampleCapture), with exposure and ISO only in manual mode.
func TestSnapshotCapture(t *testing.T) {
	a, rec, s := connected(t)
	setSettings(t, a, func(s *Settings) {
		s.Snapshot, s.ManualImaging, s.SnapshotExposureSec, s.ExposureSec, s.ISO = true, true, 0.5, 10, 500
	})
	if err := a.Image(ImageRequest{Name: "Tree"}); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Wait(fake.Is("TaskController", "RunSampleCapture"), time.Second)
	if !ok || m["ExposureTime"] != 0.5 || m["ISO"] != 500.0 { // the snapshot exposure
		t.Fatalf("RunSampleCapture %v", m)
	}
	if len(received(s, "TaskController", "RunImaging")) != 0 {
		t.Error("snapshot mode started an imaging run")
	}
	reportForced(t, a, s)
	setSettings(t, a, func(s *Settings) { s.ManualImaging = false })
	if err := a.Image(ImageRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := received(s, "TaskController", "RunSampleCapture"); len(got) != 2 || got[1]["ExposureTime"] != nil || got[1]["ISO"] != nil {
		t.Errorf("automatic snapshot payload %v", got)
	}

	// The snapshot is shown to save or discard (SaveDeleteImageFragment).
	s.SetFile("snap.jpg", fake.JPEG(5))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "snap.jpg", "ImageType": "SAMPLE_CAPTURE"})
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Image == "snapshot" })
	p, err := a.SaveSnapshot()
	if err != nil || !strings.HasSuffix(p, "Snapshot-20260115-220000.png") {
		t.Fatalf("SaveSnapshot %s %v", p, err)
	}
	b, _ := os.ReadFile(p)
	if !bytes.HasPrefix(b, []byte("\x89PNG")) || !bytes.Contains(b, []byte("eXIf")) {
		t.Error("snapshot not saved as a PNG with EXIF (GPS)")
	}
	if a.Activity().Image == "snapshot" {
		t.Error("snapshot still shown after saving")
	}
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "snap.jpg", "ImageType": "SAMPLE_CAPTURE"})
	// Poll the current state: rec.wait would match the first snapshot's
	// event and discard before this download lands.
	for end := time.Now().Add(3 * time.Second); a.Activity().Image != "snapshot"; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("second snapshot not shown")
		}
	}
	a.DiscardSnapshot()
	if a.Activity().Image == "snapshot" {
		t.Error("snapshot still shown after discarding")
	}
	if _, err := a.SaveSnapshot(); err == nil {
		t.Error("saved a discarded snapshot")
	}
}

// U: nothing is captured while the telescope moves, nor unless the Origin
// is idle (startImageCapture).
func TestCaptureRefusedWhileMovingOrBusy(t *testing.T) {
	a, _, s := connected(t)
	setMountAlt(t, a, s, 30)
	s.SetStatus("Mount", fake.Msg{"IsGotoOver": false})
	s.NotifyStatus("Mount")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if m, _ := c.Last("Mount/GetStatus"); m.Fields()["IsGotoOver"] == false {
			break
		}
	}
	if err := a.Image(ImageRequest{Name: "M 42"}); err == nil || !strings.Contains(err.Error(), "Please wait for the telescope to stop moving") {
		t.Errorf("while going to: %v", err)
	}
	s.SetStatus("Mount", fake.Msg{"IsGotoOver": true})
	s.NotifyStatus("Mount")
	time.Sleep(50 * time.Millisecond)
	if err := a.Slew(4, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Image(ImageRequest{Name: "M 42"}); err == nil || !strings.Contains(err.Error(), "Telescope in Motion") {
		t.Errorf("while slewing: %v", err)
	}
	if err := a.StopSlew(); err != nil {
		t.Fatal(err)
	}
	setTask(t, a, s, fake.Msg{"State": "FOCUSING"})
	if err := a.Image(ImageRequest{Name: "M 42"}); !errors.Is(err, errShown) { // the app returns silently
		t.Errorf("while focusing: %v", err)
	}
	if len(received(s, "TaskController", "RunImaging"))+len(received(s, "TaskController", "RunSampleCapture")) != 0 {
		t.Error("captured anyway")
	}
}

// Q: Center Here sends the crosshair's place in the image (0–1, x right, y
// down) as RunCenterTarget, only while idle, initialized and not in
// snapshot mode.
func TestCenterHere(t *testing.T) {
	a, _, s := connected(t)
	if err := a.CenterHere(0.25, 1.5); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Wait(fake.Is("TaskController", "RunCenterTarget"), time.Second)
	if !ok || m["OffsetX"] != 0.25 || m["OffsetY"] != 1.0 {
		t.Fatalf("RunCenterTarget %v", m)
	}
	setSettings(t, a, func(s *Settings) { s.Snapshot = true })
	if a.CenterHere(0.5, 0.5) == nil {
		t.Error("reframe allowed in snapshot mode")
	}
	setSettings(t, a, func(s *Settings) { s.Snapshot = false })
	s.SetStatus("TaskController", fake.Msg{"IsReady": false})
	s.NotifyStatus("TaskController")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); ready(c) && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	if a.CenterHere(0.5, 0.5) == nil {
		t.Error("reframe allowed before initialization")
	}
	if len(received(s, "TaskController", "RunCenterTarget")) != 1 {
		t.Error("refused reframes were sent")
	}
}

// An Origin answers every Optics command with an error (its optics are a
// fixed RASA 6; Celestron's web app hides Optics for it), so none is sent:
// not on connect, not for Center Here, whose offsets are the image's as shown.
func TestNoOpticsCommands(t *testing.T) {
	a, _, s := connected(t)
	if err := a.CenterHere(0.25, 0.6); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Wait(fake.Is("TaskController", "RunCenterTarget"), time.Second)
	if !ok || m["OffsetX"] != 0.25 || m["OffsetY"] != 0.6 {
		t.Fatalf("RunCenterTarget %v", m)
	}
	for _, m := range s.Received() {
		if m["Destination"] == "Optics" {
			t.Errorf("sent %v", m)
		}
	}
}

// R: the stretch is read on connect and set one value at a time, then read
// back.
func TestStretch(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetInfo("ImageServer/GetStretch", fake.Msg{"Background": 0.4, "Strength": 0.6})
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "status", func(d any) bool { return d.(Status).Key == "ImageServer/GetStretch" })
	if st := a.Stretch(); st.Background != 0.4 || st.Strength != 0.6 {
		t.Errorf("Stretch() = %+v", st)
	}
	if err := a.SetStretch("Strength", 0.7); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Wait(fake.Is("ImageServer", "SetStretch"), time.Second)
	if m["Strength"] != 0.7 || m["Background"] != nil {
		t.Errorf("SetStretch %v", m)
	}
	if n := len(received(s, "ImageServer", "GetStretch")); n < 2 {
		t.Errorf("stretch not read back: %d GetStretch", n)
	}
	if a.SetStretch("Gamma", 1) == nil || a.SetStretch("Background", 1.5) == nil {
		t.Error("bad stretch accepted")
	}
}

// S: the filter button installs the other filter; the Nebula filter is
// refused in snapshot mode; after the change the app offers autofocus.
func TestFilterChange(t *testing.T) {
	a, _, s := connected(t)
	if err := a.SetFilter("Nebula"); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Wait(fake.Is("Camera", "SetFilter"), time.Second); m["Filter"] != "Nebula" {
		t.Errorf("SetFilter %v", m)
	}
	if err := a.AutoFocusAfterFilterChange(); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Wait(fake.Is("TaskController", "RunInfinityAutoFocus"), time.Second); m["StartingPosition"] != nil {
		t.Errorf("filter-change autofocus %v, want no StartingPosition", m)
	}
	setSettings(t, a, func(s *Settings) { s.Snapshot = true })
	if err := a.SetFilter("Nebula"); err == nil || !strings.Contains(err.Error(), "Cannot use Nebula Filter in Snapshot mode") {
		t.Errorf("nebula in snapshot mode: %v", err)
	}
	if err := a.SetFilter("Clear"); err != nil {
		t.Errorf("clear in snapshot mode: %v", err)
	}
	if a.SetFilter("Red") == nil {
		t.Error("unknown filter accepted")
	}
}

// T: focus moves to an absolute position within the Origin's limits, only
// while idle.
func TestFocusLimits(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetInfo("Focuser/GetPositionLimits", fake.Msg{"Min": 2000, "Max": 40000})
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "status", func(d any) bool { return d.(Status).Key == "Focuser/GetPositionLimits" })
	if l := a.FocusLimits(); l.Min != 2000 || l.Max != 40000 {
		t.Errorf("limits %+v", l)
	}
	rec.wait(t, "status", func(d any) bool { return d.(Status).Key == "TaskController/GetStatus" })
	for _, c := range [][2]int{{50000, 40000}, {100, 2000}, {31000, 31000}} {
		if err := a.FocusTo(c[0]); err != nil {
			t.Fatal(err)
		}
		got := received(s, "Focuser", "MoveTo")
		if v := got[len(got)-1]["Value"]; v != float64(c[1]) {
			t.Errorf("FocusTo(%d) moved to %v, want %d", c[0], v, c[1])
		}
	}
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	if err := a.FocusTo(30000); err == nil || !strings.Contains(err.Error(), "Focus cannot be set unless Origin is idle") {
		t.Errorf("focus while imaging: %v", err)
	}
}

func TestFocusLimitsDefault(t *testing.T) {
	a, _ := newApp(t)
	if l := a.FocusLimits(); l.Min != 1000 || l.Max != 35000 {
		t.Errorf("default limits %+v, want the app's 1000–35000", l)
	}
}

// X: the Origin's image folders can be listed, re-downloaded, deleted and
// copied to a USB drive; copying shows its progress and can be cancelled.
func TestFileManager(t *testing.T) {
	a, rec, s := connected(t)
	s.SetInfo("ImageServer/GetListOfAvailableDirectories", fake.Msg{"DirectoryList": []any{"", "M42_2026-01-15", "M31_2026-01-16"}})
	dirs, err := a.ImageDirectories()
	if err != nil || len(dirs) != 2 || dirs[0] != "M42_2026-01-15" {
		t.Fatalf("ImageDirectories %v %v", dirs, err)
	}
	s.SetInfo("ImageServer/GetStackedMasterFromDirectory", fake.Msg{"FileExists": false})
	if err := a.DownloadDirectory("M31_2026-01-16"); err == nil || err.Error() != "alert:Final stacked master does not exist. Cannot download." {
		t.Errorf("missing master: %v", err)
	}
	s.SetInfo("ImageServer/GetStackedMasterFromDirectory", fake.Msg{"FileExists": true})
	if err := a.DownloadDirectory("M42_2026-01-15"); err != nil {
		t.Error(err)
	}
	if m := received(s, "ImageServer", "GetStackedMasterFromDirectory"); m[1]["DirectoryName"] != "M42_2026-01-15" {
		t.Errorf("GetStackedMasterFromDirectory %v", m)
	}
	for _, f := range []func() error{
		func() error { return a.DeleteDirectory("M31_2026-01-16") },
		a.DeleteAllDirectories,
		func() error { return a.CopyDirectoryToUSB("M42_2026-01-15") },
		a.CopyAllToUSB,
	} {
		if err := f(); err != nil {
			t.Error(err)
		}
	}
	if m := received(s, "ImageServer", "DeleteImageDirectory"); len(m) != 1 || m[0]["DirectoryName"] != "M31_2026-01-16" {
		t.Errorf("DeleteImageDirectory %v", m)
	}
	if m := received(s, "TaskController", "CopyDirectoryToFlashDrive"); len(m) != 1 || m[0]["DirectoryName"] != "M42_2026-01-15" {
		t.Errorf("CopyDirectoryToFlashDrive %v", m)
	}
	if len(received(s, "ImageServer", "DeleteAllImageDirectories")) != 1 || len(received(s, "TaskController", "CopyAllDirectoriesToFlashDrive")) != 1 {
		t.Error("delete all / copy all not sent")
	}

	// While copying: progress in the status line, Copy All cancels.
	s.Notify("TaskController", "FileTransferStatus", fake.Msg{"PercentageComplete": 40})
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text == "Exporting Images: 40% Complete" })
	rec.wait(t, "status", func(d any) bool { return d.(Status).Key == "TaskController/FileTransferStatus" })
	if a.DownloadDirectory("M42_2026-01-15") == nil {
		t.Error("download allowed while copying")
	}
	if err := a.CopyAllToUSB(); err != nil {
		t.Fatal(err)
	}
	if sentCount(s, "TaskController", "HaltTasks", 1) != 1 || len(received(s, "TaskController", "CopyAllDirectoriesToFlashDrive")) != 1 {
		t.Error("Copy All while copying did not cancel")
	}
	// HaltTasks's answer ends the progress, as the app's clearCachedState,
	// and tells the file manager so.
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text != "Exporting Images: 40% Complete" })
	rec.wait(t, "status", func(d any) bool {
		st := d.(Status)

		return st.Key == "TaskController/FileTransferStatus" && st.Fields["PercentageComplete"] == nil
	})

	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	if err := a.DeleteAllDirectories(); err == nil || !strings.Contains(err.Error(), "only be used while Origin is idle") {
		t.Errorf("delete all while imaging: %v", err)
	}
}

// W: the idle hint follows snapshot mode and manual slews at once: the
// event is sent before the call returns.
func TestIdleHintFollowsSettingsAndSlew(t *testing.T) {
	a, rec, _ := connected(t)
	time.Sleep(100 * time.Millisecond) // let the on-connect replies settle
	newest := func() string {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, v := range slices.Backward(rec.got) {
			if act, ok := v.data.(Activity); ok {
				return act.Text
			}
		}

		return ""
	}
	setSettings(t, a, func(s *Settings) { s.Snapshot = true })
	if got := newest(); !strings.Contains(got, "quick exposure") {
		t.Errorf("after snapshot mode: %q", got)
	}
	if err := a.Slew(4, 0); err != nil {
		t.Fatal(err)
	}
	if got := newest(); !strings.Contains(got, "Telescope moving") {
		t.Errorf("while slewing: %q", got)
	}
	if err := a.StopSlew(); err != nil {
		t.Fatal(err)
	}
	if got := newest(); !strings.Contains(got, "quick exposure") {
		t.Errorf("after the slew: %q", got)
	}
}
