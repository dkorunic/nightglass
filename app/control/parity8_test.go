// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

// A final image that fails to download does not leave the app waiting for
// it: the wait ends as the download starts, as the app's.
func TestFailedFinalDownloadEndsWait(t *testing.T) {
	a, rec, s := connected(t)
	uuid := startRun(t, a, rec, s)
	if err := a.FinishImaging(); err != nil {
		t.Fatal(err)
	}
	if got := a.Activity().Text; got != "Requesting Final Image…" {
		t.Fatalf("status %q", got)
	}
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "gone.tiff", "ImageType": "FINAL_STACKED_MASTER", "Uuid": uuid})
	rec.wait(t, "activity", func(d any) bool { return !strings.Contains(d.(Activity).Text, "Final Image") })
	// Live view resumes.
	s.SetFile("live/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" })
}

// Cancelling ends a wait for the final image too.
func TestCancelEndsFinalWait(t *testing.T) {
	a, rec, s := connected(t)
	startRun(t, a, rec, s)
	if err := a.FinishImaging(); err != nil {
		t.Fatal(err)
	}
	if err := a.CancelImaging(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.Activity().Text, "Final Image") {
		t.Errorf("still waiting: %q", a.Activity().Text)
	}
}

// After Finish no stacked frame replaces the shown one.
func TestNoStackedAfterFinish(t *testing.T) {
	a, rec, s := connected(t)
	uuid := startRun(t, a, rec, s)
	if err := a.FinishImaging(); err != nil {
		t.Fatal(err)
	}
	n := countEvents(rec, "frame")
	s.SetFile("stacked/2.jpg", fake.JPEG(2))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "stacked/2.jpg", "ImageType": "STACKED_MASTER", "Uuid": uuid})
	time.Sleep(200 * time.Millisecond)
	if countEvents(rec, "frame") != n {
		t.Error("a stacked frame arrived after Finish")
	}
}

// A drop and reconnect start afresh: images go, a run in progress is joined
// again.
func TestReconnectResetsLink(t *testing.T) {
	a, rec, s := connected(t)
	s.SetFile("live/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" })
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": "run-7"}})
	s.DropAll()
	rec.wait(t, "conn", func(d any) bool { return !d.(Conn).Connected && d.(Conn).Host != "" }) // the drop
	if a.Activity().Image != "" {
		t.Error("an image survived the drop")
	}
	m, ok := s.Wait(fake.Is("ImageServer", "GetFinalStackedMaster"), 3*time.Second)
	if !ok || m["ImageUuid"] != "run-7" {
		t.Errorf("run not joined after reconnecting: %v", m)
	}
}

// A normal close from the telescope disconnects without redialling.
func TestNormalCloseDisconnects(t *testing.T) {
	a, rec, s := connected(t)
	s.CloseAll()
	rec.wait(t, "conn", func(d any) bool { return d.(Conn) == Conn{} })
	if _, err := a.cl(); err == nil {
		t.Error("still connected")
	}
}

// No filter change or its autofocus while imaging.
func TestFilterLockedWhileImaging(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	if err := a.SetFilter("Nebula"); !errors.Is(err, origin.ErrImaging) {
		t.Errorf("SetFilter: %v", err)
	}
	if err := a.AutoFocusAfterFilterChange(); !errors.Is(err, origin.ErrImaging) {
		t.Errorf("autofocus: %v", err)
	}
	if len(received(s, "Camera", "SetFilter")) != 0 {
		t.Error("filter sent mid-run")
	}
}

// An Origin error with the app's own message is shown as its "Origin
// Error" alert, waited for or not (onOriginMessageRelay → handleOriginError);
// a waiting caller gets errShown, so the frontend shows nothing more.
func TestBackgroundErrorsShown(t *testing.T) {
	a, rec, s := connected(t)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		r := s.DefaultHandle(req)
		if fake.Is("System", "PowerDownOrigin")(req) || fake.Is("LampController", "GetBrightnessLevel")(req) {
			r["ErrorCode"], r["ErrorMessage"] = -9, "x"
		}

		return r
	})
	c, _ := a.cl()
	_ = c.Post(t.Context(), "LampController", "GetBrightnessLevel", nil) // not asked at connect, so no stray error
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Title == "Origin Error" })
	n := countEvents(rec, "alert")
	if err := a.PowerOff(); !errors.Is(err, errShown) {
		t.Errorf("PowerOff: %v, want errShown", err)
	}
	for end := time.Now().Add(time.Second); countEvents(rec, "alert") == n && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if countEvents(rec, "alert") != n+1 {
		t.Error("the waited-for error was not shown as an alert")
	}
}

// A timed run sends TotalDuration (seconds); once the Origin has ended it,
// its final image is asked for as Finish would. An untimed run is not.
func TestTimedRun(t *testing.T) {
	a, _, s := connected(t)
	setMountAlt(t, a, s, 30)
	if err := a.Image(ImageRequest{DurationMin: 1.5}); err != nil {
		t.Fatal(err)
	}
	m := runImagings(s)[0]
	uuid, _ := m["Uuid"].(string)
	if m["TotalDuration"] != 90.0 {
		t.Errorf("TotalDuration %v, want 90", m["TotalDuration"])
	}
	// A status still IDLE from before the run started does not end it.
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	time.Sleep(50 * time.Millisecond)
	if len(received(s, "ImageServer", "GetFinalStackedMaster")) != 0 {
		t.Fatal("final image asked for before the run started")
	}
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": uuid}})
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	g, ok := s.Wait(fake.Is("ImageServer", "GetFinalStackedMaster"), 2*time.Second)
	if !ok || g["ImageUuid"] != uuid {
		t.Fatalf("final image not asked for: %v", g)
	}
	if got := a.Activity().Text; got != "Requesting Final Image…" {
		t.Errorf("status %q", got)
	}

	// Untimed: the run ending by itself asks for nothing.
	b, _, s2 := connected(t)
	setMountAlt(t, b, s2, 30)
	if err := b.Image(ImageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := runImagings(s2)[0]["TotalDuration"]; ok {
		t.Error("TotalDuration sent for an untimed run")
	}
	setTask(t, b, s2, fake.Msg{"State": "IMAGING_OBJECT"})
	setTask(t, b, s2, fake.Msg{"State": "IDLE", "IsReady": true})
	time.Sleep(100 * time.Millisecond)
	if len(received(s2, "ImageServer", "GetFinalStackedMaster")) != 0 {
		t.Error("final image asked for after an untimed run")
	}
	if b.Image(ImageRequest{DurationMin: -1}) == nil {
		t.Error("negative duration accepted")
	}
}

// Reboot: idle only; then a counted-down wait before reconnecting, which
// "Attempt To Connect" can end early.
func TestReboot(t *testing.T) {
	a, rec, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "FOCUSING"})
	if a.Reboot() == nil {
		t.Error("reboot while busy")
	}
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	a.restartDelay = time.Hour
	if err := a.Reboot(); err != nil {
		t.Fatal(err)
	}
	if len(received(s, "System", "Reboot")) != 1 {
		t.Error("Reboot not sent once")
	}
	rec.wait(t, "activity", func(d any) bool { return strings.HasPrefix(d.(Activity).Text, "Reconnecting to Origin in") })
	if err := a.ReconnectNow(); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
}

// A task that ends early is reported in TaskController/GetStatus itself:
// ErrorCode and ErrorMessage beside the State, pushed again each second
// (Celestron's web app, status.js). The state still counts, the failure is
// shown once rather than every second, and -7 (INTERRUPTED, after a halt) is
// not shown at all.
func TestTaskErrorStatus(t *testing.T) {
	a, rec, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "INITIALIZING"})
	alerts := func() int {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		n := 0
		for _, e := range rec.got {
			if al, ok := e.data.(Alert); ok && e.name == "alert" && al.Title == "Origin Error" {
				n++
			}
		}

		return n
	}
	failed := fake.Msg{"State": "IDLE", "ErrorCode": -75, "ErrorMessage": "Sun is above horizon"}
	for range 3 {
		s.Notify("TaskController", "GetStatus", failed)
	}
	rec.wait(t, "alert", func(d any) bool { return strings.Contains(d.(Alert).Message, "Sun is above horizon") })
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); c.TaskState() != "IDLE" && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	if st := c.TaskState(); st != "IDLE" {
		t.Errorf("task state %q after a failed task's status, want IDLE", st)
	}
	rec.wait(t, "status", func(d any) bool {
		st := d.(Status)

		return st.Key == "TaskController/GetStatus" && st.Fields["State"] == "IDLE"
	})
	time.Sleep(100 * time.Millisecond)
	if n := alerts(); n != 1 {
		t.Errorf("%d alerts for one failure pushed three times", n)
	}
	// A clean status ends it; the same failure again is shown again.
	s.Notify("TaskController", "GetStatus", fake.Msg{"State": "IDLE", "ErrorCode": 0})
	s.Notify("TaskController", "GetStatus", failed)
	for end := time.Now().Add(2 * time.Second); alerts() < 2 && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	if n := alerts(); n != 2 {
		t.Errorf("%d alerts, want the failure shown again after a clean status", n)
	}
	// INTERRUPTED is a halted task, not a failure to show.
	setTask(t, a, s, fake.Msg{"State": "FOCUSING", "ErrorCode": 0})
	s.Notify("TaskController", "GetStatus", fake.Msg{"State": "IDLE", "ErrorCode": -7, "ErrorMessage": "Interrupted"})
	for end := time.Now().Add(2 * time.Second); c.TaskState() != "IDLE" && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	if c.TaskState() != "IDLE" {
		t.Error("an interrupted task's status was not taken")
	}
	time.Sleep(100 * time.Millisecond)
	if n := alerts(); n != 2 {
		t.Errorf("INTERRUPTED shown (%d alerts)", n)
	}
}
