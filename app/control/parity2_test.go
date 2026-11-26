// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

func received(s *fake.Server, dest, cmd string) []fake.Msg {
	var out []fake.Msg
	for _, m := range s.Received() {
		if fake.Is(dest, cmd)(m) {
			out = append(out, m)
		}
	}

	return out
}

// startRun images, puts the fake in IMAGING_OBJECT for that run and delivers
// one stacked frame, and returns the run's Uuid.
func startRun(t *testing.T, a *App, rec *recorder, s *fake.Server) string {
	t.Helper()
	setMountAlt(t, a, s, 30)
	setTask(t, a, s, fake.Msg{"State": "IDLE"}) // a capture starts only from IDLE
	if err := a.Image(ImageRequest{Name: "M 42"}); err != nil {
		t.Fatal(err)
	}
	uuid := runImagings(s)[len(runImagings(s))-1]["Uuid"].(string)
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": uuid}})
	s.NotifyStatus("TaskController")
	c, _ := a.cl()
	for c.TaskState() != "IMAGING_OBJECT" {
		time.Sleep(5 * time.Millisecond)
	}
	s.SetFile("stacked/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "stacked/1.jpg", "ImageType": "STACKED_MASTER", "Uuid": uuid})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "stacked" && d.(FrameEvent).Info.Uuid == uuid })

	return uuid
}

// A: Finish keeps the run (HaltTasks + GetFinalStackedMaster), Cancel
// discards it (CancelImaging) and forgets its Uuid.
func TestFinishAndCancelLikeApp(t *testing.T) {
	a, rec, s := connected(t)
	if err := a.FinishImaging(); err == nil {
		t.Error("finish while not imaging accepted")
	}
	uuid := startRun(t, a, rec, s)
	if !slices.Contains(a.Settings().PendingImages, uuid) {
		t.Errorf("run %s not remembered: %v", uuid, a.Settings().PendingImages)
	}
	if err := a.FinishImaging(); err != nil {
		t.Fatal(err)
	}
	// Both go out back to back, without waiting for the halt's answer.
	if _, ok := s.Wait(fake.Is("TaskController", "HaltTasks"), time.Second); !ok {
		t.Error("finish did not halt")
	}
	if m, ok := s.Wait(fake.Is("ImageServer", "GetFinalStackedMaster"), time.Second); !ok || m["ImageUuid"] != uuid {
		t.Errorf("GetFinalStackedMaster %v", m)
	}
	if len(received(s, "TaskController", "CancelImaging")) != 0 {
		t.Error("finish sent CancelImaging, which discards the run")
	}
	s.SetFile("final.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER", "Uuid": uuid})
	// A run is forgotten once its final image is saved.
	rec.wait(t, "toast", func(d any) bool { return strings.HasPrefix(d.(Toast).Message, "Saved ") })
	if slices.Contains(a.Settings().PendingImages, uuid) {
		t.Error("saved run still pending")
	}

	uuid2 := startRun(t, a, rec, s)
	if err := a.CancelImaging(); err != nil {
		t.Fatal(err)
	}
	if len(received(s, "TaskController", "CancelImaging")) != 1 || slices.Contains(a.Settings().PendingImages, uuid2) {
		t.Errorf("cancel: %v pending %v", received(s, "TaskController", "CancelImaging"), a.Settings().PendingImages)
	}
}

func TestFinishNeedsAStackedFrame(t *testing.T) {
	a, _, s := connected(t)
	setMountAlt(t, a, s, 30)
	if err := a.Image(ImageRequest{Name: "X"}); err != nil {
		t.Fatal(err)
	}
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT"})
	s.NotifyStatus("TaskController")
	c, _ := a.cl()
	for c.TaskState() != "IMAGING_OBJECT" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.FinishImaging(); err == nil || !strings.Contains(err.Error(), "stacked") {
		t.Errorf("finish without a stacked frame: %v", err)
	}
}

// G: runs whose final image never arrived are offered on the next connect.
func TestPendingImagesRecoveredOnConnect(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		r := s.DefaultHandle(req)
		if fake.Is("ImageServer", "GetAvailableStackedMasters")(req) {
			r["AvailableStackedMasters"] = fake.Msg{"u1": "Images/u1"}
		}

		return r
	})
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	a.remember("u1")
	a.remember("u2")
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "orphans", func(d any) bool { return slices.Equal(d.(Orphans).Uuids, []string{"u1"}) })
	if err := a.DownloadPending("u1"); err != nil {
		t.Fatal(err)
	}
	if g := received(s, "ImageServer", "GetFinalStackedMaster"); len(g) != 1 || g[0]["ImageUuid"] != "u1" {
		t.Errorf("download: %v", g)
	}
	a.ForgetPending("u2")
	if got := a.Settings().PendingImages; !slices.Equal(got, []string{"u1"}) {
		t.Errorf("after forgetting u2: %v", got)
	}
	a.ForgetPending("")
	if got := a.Settings().PendingImages; len(got) != 0 {
		t.Errorf("after forgetting all: %v", got)
	}
}

// H: a reported Nebula filter is questioned on connect; "No" sets Clear.
func TestNebulaFilterPrompt(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetInfo("Camera/GetFilter", fake.Msg{"Filter": "Nebula"})
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "nebula", func(any) bool { return true })
	if err := a.SetFilter("Clear"); err != nil {
		t.Fatal(err)
	}
	if m := received(s, "Camera", "SetFilter"); len(m) != 1 || m[0]["Filter"] != "Clear" {
		t.Errorf("SetFilter %v", m)
	}
}

// B: alignment and focusing frames are shown as live view.
func TestAlignmentAndFocusingFramesShownLive(t *testing.T) {
	_, rec, s := connected(t)
	for i, typ := range []string{"ALIGNMENT", "FOCUSING"} {
		loc := typ + ".jpg"
		s.SetFile(loc, fake.JPEG(i))
		s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": loc, "ImageType": typ})
		rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" && d.(FrameEvent).Info.ImageType == typ })
	}
}

// J: a frame announced while a stacked master downloads is dropped, as the
// app's handleNewImageReadyWithMessage; the next one after it is shown.
func TestLiveWaitsForMasterDownload(t *testing.T) {
	a, rec, s := connected(t)
	a.mu.Lock()
	a.dlStacked = &origin.ImageReady{}
	a.mu.Unlock()
	s.SetFile("live.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live.jpg", "ImageType": "LIVE"})
	time.Sleep(150 * time.Millisecond)
	a.mu.Lock()
	a.dlStacked = nil
	a.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	if countEvents(rec, "frame") != 0 {
		t.Error("live frame shown although announced while a master downloaded")
	}
	// A snapshot is taken regardless (the capture screen's own handler).
	a.mu.Lock()
	a.dlStacked = &origin.ImageReady{}
	a.mu.Unlock()
	s.SetFile("snap.jpg", fake.JPEG(3))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "snap.jpg", "ImageType": "SAMPLE_CAPTURE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "snapshot" })
	a.mu.Lock()
	a.dlStacked = nil
	a.mu.Unlock()
	s.SetFile("live2.jpg", fake.JPEG(2))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live2.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Info.FileLocation == "live2.jpg" })
}

// I: slow downloads warn once per connection, after more than 60 samples.
func TestLowBandwidthWarnedOnce(t *testing.T) {
	a, rec, _ := connected(t)
	for range 60 {
		a.noteSpeed(0.1)
	}
	count := func() int {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		n := 0
		for _, e := range rec.got {
			if al, ok := e.data.(Alert); ok && al.Title == "Low Network Bandwidth!" && strings.Contains(al.Message, "(0.10 MB/s). A minimum of 0.20 MB/s") {
				n++
			}
		}

		return n
	}
	if count() != 0 {
		t.Fatal("warned before 60 samples")
	}
	for range 10 {
		a.noteSpeed(0.1)
	}
	if count() != 1 {
		t.Errorf("%d warnings, want exactly 1", count())
	}
	for range 10 {
		a.noteSpeed(5)
	}
	if count() != 1 {
		t.Error("fast downloads warned")
	}
}

// C: autofocus only when idle; terrestrial in snapshot mode; else infinity
// from the current focuser position.
func TestAutoFocusLikeApp(t *testing.T) {
	a, _, s := connected(t)
	c, _ := a.cl()
	for c.TaskState() != "IDLE" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.AutoFocus(); err != nil {
		t.Fatal(err)
	}
	if m := received(s, "TaskController", "RunInfinityAutoFocus"); len(m) != 1 || m[0]["StartingPosition"] != 30000.0 {
		t.Errorf("infinity autofocus %v", m)
	}
	st := a.Settings()
	st.Snapshot = true
	_ = a.SaveSettings(st)
	if err := a.AutoFocus(); err != nil {
		t.Fatal(err)
	}
	if len(received(s, "TaskController", "RunTerrestrialAutoFocus")) != 1 {
		t.Error("snapshot mode did not run terrestrial autofocus")
	}
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT"})
	s.NotifyStatus("TaskController")
	for c.TaskState() != "IMAGING_OBJECT" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.AutoFocus(); err == nil {
		t.Error("autofocus while imaging accepted")
	}
}

// E: Initialize cancels a running initialization instead of restarting it.
func TestInitializeCancelsWhileInitializing(t *testing.T) {
	a, _, s := connected(t)
	s.SetStatus("TaskController", fake.Msg{"State": "INITIALIZING"})
	s.NotifyStatus("TaskController")
	c, _ := a.cl()
	for c.TaskState() != "INITIALIZING" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.Initialize(); err != nil {
		t.Fatal(err)
	}
	if sentCount(s, "TaskController", "HaltTasks", 1) != 1 || len(received(s, "TaskController", "RunInitialize")) != 0 {
		t.Error("Initialize while initializing must send HaltTasks, not RunInitialize")
	}
}
