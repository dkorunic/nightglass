// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

// Spec §9.14: the imaging flow as the app's.

// Once the final image is saved, the masters are dropped so live view
// returns, and the saved image is offered to open (SkySafariActivityImaging$5).
func TestFinalSavedReturnsToLive(t *testing.T) {
	a, rec, s := connected(t)
	uuid := startRun(t, a, rec, s)
	if err := a.FinishImaging(); err != nil {
		t.Fatal(err)
	}
	s.SetFile("final.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER", "Uuid": uuid, "ObjectName": "M 42"})
	saved := rec.wait(t, "saved", func(any) bool { return true }).(Saved)
	if !strings.HasPrefix(saved.Name, "Orion_Nebula-") {
		t.Errorf("saved %q", saved.Name)
	}
	if img := a.Activity().Image; img == kindFinal || img == kindStacked {
		t.Errorf("still showing %q after the save", img)
	}
	// Saved again: a new file beside the first (-1), never over it.
	p, err := a.SaveFinal()
	if want := strings.TrimSuffix(saved.Name, ".png") + "-1.png"; err != nil || filepath.Base(p) != want {
		t.Errorf("Save final image again: %s %v, want %s", p, err, want)
	}
	if _, err := os.Stat(filepath.Join(a.Settings().SaveDir, saved.Name)); err != nil {
		t.Errorf("first save gone: %v", err)
	}
}

// Cancel drops both masters (resetStackedMasterImaging); a final image only
// awaited holds back tile downloads, not Reframe (updateUIForScopeStatus).
func TestCancelDropsMastersAndReframeWaits(t *testing.T) {
	a, rec, s := connected(t)
	startRun(t, a, rec, s)
	if err := a.CancelImaging(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	_, stacked := a.frames[kindStacked]
	_, final := a.frames[kindFinal]
	a.waitingFinal = true // a final image is asked for
	a.mu.Unlock()
	if stacked || final {
		t.Error("masters kept after cancel")
	}
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	// Only waiting: Reframe stays (isBusyDownloadingOrProcessingMasterImage),
	// a tile download waits (waitingForFinalStackedMasterImage).
	if a.Activity().MasterBusy {
		t.Error("reframe blocked while a final image is only awaited")
	}
	if err := a.DownloadTile("tile"); err == nil || !strings.Contains(err.Error(), "Please wait") {
		t.Errorf("tile download while a final image is awaited: %v", err)
	}
}

// Autofocus starts from the focuser position clamped to its limits.
func TestAutoFocusClampsStart(t *testing.T) {
	a, _, s := connected(t)
	s.SetStatus("Focuser", fake.Msg{"Position": 500})
	s.NotifyStatus("Focuser")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if m, ok := c.Last("Focuser/GetStatus"); ok && m.Fields()["Position"] == 500.0 {
			break
		}
	}
	if err := a.AutoFocus(); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Wait(fake.Is("TaskController", "RunInfinityAutoFocus"), time.Second); m["StartingPosition"] != 1000.0 {
		t.Errorf("StartingPosition %v, want the 1000 limit", m["StartingPosition"])
	}
}

// The share screen's copy is saved beside the image, of the same target;
// anything but a PNG is refused.
func TestSaveShared(t *testing.T) {
	a, _ := newApp(t)
	dir := a.Settings().SaveDir
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 4, 4)))
	if err := os.WriteFile(filepath.Join(dir, "M_42-20260115-213000.png"), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	a.update(func(s *Settings) { s.Images = map[string]ImageRecord{"M_42-20260115-213000.png": {Title: "M 42"}} })
	copyName, err := a.SaveShared("M_42-20260115-213000.png", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(b.Bytes()))
	if err != nil || copyName != "M_42-20260115-213000-shared.png" || a.Settings().Images[copyName].Title != "M 42" {
		t.Errorf("shared %q %v %+v", copyName, err, a.Settings().Images)
	}
	if _, err := a.SaveShared("M_42-20260115-213000.png", "data:text/html;base64,PGI+"); err == nil {
		t.Error("non-PNG accepted")
	}
	if _, err := a.SaveShared("../x.png", "data:image/png;base64,"); err == nil {
		t.Error("path outside SaveDir accepted")
	}
}

// Spec §9.14: payloads as the app sends them.
func TestPayloadsLikeApp(t *testing.T) {
	// Bright stars' negative magnitudes are known; the lists' 0 and -9999 are not.
	for m, want := range map[float64]bool{-1.46: true, 4.4: true, 0: false, -9999: false} {
		if catalog.KnownMag(m) != want {
			t.Errorf("KnownMag(%v) = %v", m, !want)
		}
	}
	a, _, s := connected(t)
	// Begin Mosaic Capture sends no camera command first; Resume leaves
	// snapshot and manual mode without asking the Origin to be idle.
	setSettings(t, a, func(st *Settings) { st.Snapshot, st.ManualImaging = true, true })
	before := len(s.Received())
	if err := a.StartMosaic(MosaicStart{RA: 10, Dec: 41, WidthArcmin: 60, HeightArcmin: 30, DurationSec: 60, ExposureSec: 10}); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Received()[before:] {
		if m["Destination"] == "LiveStream" {
			t.Errorf("StartMosaic sent %v", m)
		}
	}
	setSettings(t, a, func(st *Settings) { st.Snapshot = true })
	setTask(t, a, s, fake.Msg{"State": "CAPTURING_MOSAIC"})
	if err := a.ResumeMosaic("MOS"); err != nil || a.Settings().Snapshot {
		t.Errorf("resume: %v, snapshot %v", err, a.Settings().Snapshot)
	}
	// Re-capture sends the row and the column each when known.
	if err := a.RecaptureTile("MOS", "t1", 2, -1); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RecaptureMosaicTile"); m["TileRow"] != 2.0 || m["TileCol"] != nil {
		t.Errorf("recapture %v", m)
	}
	// Fans and the flat panel are not read back.
	n := len(received(s, "Environment", "GetStatus"))
	if err := a.SetFan("CPU", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if len(received(s, "Environment", "GetStatus")) != n {
		t.Error("fans read back")
	}
	// The dark frames' default exposure is the snapshot one in snapshot mode
	// (camera settings change only while not imaging).
	setTask(t, a, s, fake.Msg{"State": "IDLE"})
	setSettings(t, a, func(st *Settings) {
		st.Snapshot, st.ManualImaging, st.SnapshotExposureSec, st.ExposureSec = true, true, 0.5, 10
	})
	if d := a.DarkDefaults(); d.ExposureSec != 0.5 {
		t.Errorf("dark exposure %v, want the snapshot 0.5", d.ExposureSec)
	}
}

// A row's go-to aims at the framing point for the large nebulae the app
// frames itself (here Caldwell's C 49, the Rosette, at its cluster), else at
// the row's own position.
func TestGotoTargetFraming(t *testing.T) {
	a, _, s := connected(t)
	c49, _, ok := a.cat.Find("C 49")
	ngc2244, _, _ := a.cat.Find("NGC 2244")
	if !ok {
		t.Fatal("C 49 not loaded")
	}
	if err := a.GotoTarget("Caldwell", "C 49"); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Wait(fake.Is("Mount", "GotoRaDec"), time.Second)
	r := math.Pi / 180
	if math.Abs(m["Ra"].(float64)-ngc2244.RADeg*r) > 1e-9 || math.Abs(m["Dec"].(float64)-ngc2244.DecDeg*r) > 1e-9 {
		t.Errorf("C 49 aimed at %v %v, want NGC 2244 (C 49 is at %v %v)", m["Ra"], m["Dec"], c49.RADeg*r, c49.DecDeg*r)
	}
	if err := a.GotoTarget("Messier", "M 42"); err != nil {
		t.Fatal(err)
	}
	m42 := messier(t, a, "M 42")
	sentCount(s, "Mount", "GotoRaDec", 2) // the second go-to is posted
	if got := lastSent(t, s, "Mount", "GotoRaDec"); math.Abs(got["Ra"].(float64)-m42.RADeg*r) > 1e-9 {
		t.Errorf("M 42 aimed at %v, want its position", got["Ra"])
	}
}

// A final image's target: the run's recorded one, else any " - " part of
// its ObjectName, each only when in the image's field (a name counts as near
// without a field), else what is at its centre (resolveTargetObjectIDForPayload).
func TestResolveTarget(t *testing.T) {
	a, _ := newApp(t)
	r := math.Pi / 180
	m42 := messier(t, a, "M 42")
	at := func(name string, x catalog.Target) origin.ImageReady {
		return origin.ImageReady{Uuid: "run", ObjectName: name, Ra: x.RADeg * r, Dec: x.DecDeg * r, FovX: 1.27 * r, FovY: 0.85 * r}
	}
	name := func(info origin.ImageReady, mosaic bool) string {
		tg, ok := a.resolveTarget(info, mosaic)
		if !ok {
			return ""
		}
		return tg.Name
	}
	if got := name(at("Orion - M 42", m42), false); got != "M 42" {
		t.Errorf("a later part in its field: %q", got)
	}
	if got := name(at("M 31", m42), false); got == "M 31" {
		t.Error("a name far from the image's centre kept")
	}
	if got := name(origin.ImageReady{ObjectName: "M 31"}, false); got != "M 31" {
		t.Errorf("no centre: the name counts as near: %q", got)
	}
	if _, ok := a.resolveTarget(origin.ImageReady{ObjectName: "Odd"}, false); ok {
		t.Error("nothing to resolve, yet resolved")
	}
	a.mu.Lock()
	a.runTargets["run"] = m42
	a.mu.Unlock()
	if got := name(at("Other", m42), false); got != "M 42" {
		t.Errorf("the run's recorded target: %q", got)
	}
}

// Nothing answering at the address fails the TCP probe: "Connection
// Failure" with the app's error -10 text, and the link dropped.
func TestConnectionFailure(t *testing.T) {
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	if err := a.Connect("127.0.0.1:1"); err != nil { // nothing listens there
		t.Fatal(err)
	}
	rec.wait(t, "alert", func(d any) bool { return d.(Alert) == errConnFailure })
	if _, err := a.cl(); err == nil {
		t.Error("still connected after giving up")
	}
}

// The Origin announcement is shown once, only when chosen; a link that is
// not a web link is dropped.
func TestAnnouncement(t *testing.T) {
	body := "New Origin software\nVersion 2 is out.\njavascript:alert(1)\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	old := announcementURL
	announcementURL = srv.URL
	t.Cleanup(func() { announcementURL = old })
	a, _ := newApp(t)
	if a.CheckAnnouncement() != nil {
		t.Error("announcement fetched without being chosen")
	}
	a.update(func(s *Settings) { s.Announcements = true })
	n := a.CheckAnnouncement()
	if n == nil || n.Title != "New Origin software" || n.Body != "Version 2 is out." || n.URL != "" {
		t.Fatalf("announcement %+v", n)
	}
	if a.CheckAnnouncement() != nil {
		t.Error("shown twice")
	}
	body = "Event\nTonight.\nhttps://example.com/x\n"
	if n := a.CheckAnnouncement(); n == nil || n.URL != "https://example.com/x" {
		t.Errorf("with a link: %+v", n)
	}
}

// Keep Screen On holds the display's inhibitor for the session, connected
// or not; switched off, it is let go.
func TestKeepScreenOn(t *testing.T) {
	a, _ := newApp(t)
	var mu sync.Mutex
	held, display := 0, false
	a.actMu.Lock()
	a.inhibit = func(_ string, d bool) func() {
		mu.Lock()
		held, display = held+1, d
		mu.Unlock()

		return func() { mu.Lock(); held--; mu.Unlock() }
	}
	a.actMu.Unlock()
	s := a.Settings()
	s.KeepScreenOn = true
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if held != 1 || !display {
		t.Errorf("held %d, display %v; want the display's", held, display)
	}
	mu.Unlock()
	s.KeepScreenOn = false
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if held != 0 {
		t.Errorf("held %d after switching it off", held)
	}
}

// ConnectOrigin is the app's connectOrigin: a typed address is probed (one
// that does not answer is refused and forgotten as typed); else a search
// finds one (connected to), several (offered) or none (Quick Start).
func TestConnectOrigin(t *testing.T) {
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	a.update(func(s *Settings) { s.Host, s.ManualHost = "127.0.0.1:1", true })
	if err := a.ConnectOrigin(); err != nil || a.Settings().ManualHost {
		t.Errorf("typed address not answering: %v, manual %v", err, a.Settings().ManualHost)
	}
	rec.wait(t, "manualinvalid", func(d any) bool { return d.(ManualInvalid).Host == "127.0.0.1:1" })
	a.discover = func(context.Context) ([]string, error) { return []string{"10.0.0.1", "10.0.0.2"}, nil }
	if err := a.ConnectOrigin(); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "origins", func(d any) bool { return len(d.(Origins).Hosts) == 2 })
	a.discover = func(context.Context) ([]string, error) { return nil, nil }
	_ = a.ConnectOrigin()
	rec.wait(t, "quickstart", func(any) bool { return true }) // never connected yet
	a.update(func(s *Settings) { s.EverConnected = true })
	_ = a.ConnectOrigin()
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Title == "Automatic Telescope Wi-Fi Connection Failed" })
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	a.discover = func(context.Context) ([]string, error) { return []string{s.Host()}, nil }
	if err := a.ConnectOrigin(); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected && d.(Conn).Host == s.Host() })
}

// Labels name objects as the app's "common - catalogue" style.
func TestStyledNames(t *testing.T) {
	a, _ := newApp(t)
	if got := a.styled("M 31"); got != "Andromeda Galaxy - M 31" {
		t.Errorf("styled M 31 = %q", got)
	}
	if got := a.styled("Unlisted 7"); got != "Unlisted 7" {
		t.Errorf("an unlisted name: %q", got)
	}
	in := statusIn{Connected: true, ImagingName: "Andromeda Galaxy - M 31"}
	in.Task = &origin.TaskStatus{State: "IMAGING_OBJECT"}
	in.Task.ImagingInfo.ImageUuid = "u"
	in.Task.ImagingInfo.ObjectInfoList = []origin.ObjectImagingInfo{{Uuid: "u", ObjectName: "M 31", StackingStatus: "RUNNING"}}
	if got := targetLabel(in); got != "Imaging: Andromeda Galaxy - M 31" {
		t.Errorf("imaging label %q", got)
	}
}

// A ConnectOrigin whose search ends after the user disconnected (or
// connected elsewhere) drops its result, and only one runs at a time.
func TestConnectOriginUserActed(t *testing.T) {
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	srv, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	release := make(chan struct{})
	a.discover = func(context.Context) ([]string, error) { <-release; return []string{srv.Host()}, nil }
	done := make(chan error, 1)
	go func() { done <- a.ConnectOrigin() }()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		a.mu.Lock()
		running := a.connecting
		a.mu.Unlock()
		if running {
			break
		}
	}
	if err := a.ConnectOrigin(); err != nil {
		t.Fatal(err) // a second one returns at once
	}
	a.Disconnect() // the user acts during the search
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := a.cl(); err == nil || countEvents(rec, "conn") > 1 {
		t.Error("the search connected after the user disconnected")
	}
}

// Through a real status: "Imaging: Andromeda Galaxy - M 31".
func TestImagingLabelLive(t *testing.T) {
	a, rec, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{
		"ImageUuid": "u", "ObjectInfoList": []any{fake.Msg{"Uuid": "u", "ObjectName": "M 31", "StackingStatus": "RUNNING"}},
	}})
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Target == "Imaging: Andromeda Galaxy - M 31" })
}

// A disconnect the app starts ends the waits and drops the images, as a drop
// does (telescopeConnectionClosed).
func TestDisconnectResetsSession(t *testing.T) {
	a, _, _ := connected(t)
	a.mu.Lock()
	a.waitingFinal = true
	a.mu.Unlock()
	a.Disconnect()
	if a.Activity().MasterBusy {
		t.Error("still waiting for a final image after disconnecting")
	}
}

// A second capture before the Origin reports the first is refused silently
// (forcedTempStatusForUI), and the forced state is what the screen shows.
func TestCaptureDoubleClick(t *testing.T) {
	a, _, s := connected(t)
	setMountAlt(t, a, s, 30)
	if err := a.Image(ImageRequest{Name: "One"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Image(ImageRequest{Name: "Two"}); !errors.Is(err, errShown) {
		t.Errorf("second capture: %v, want errShown", err)
	}
	if n := len(runImagings(s)); n != 1 {
		t.Errorf("%d RunImaging sent, want 1", n)
	}
	if act := a.Activity(); act.Forced != "IMAGING_OBJECT" {
		t.Errorf("Forced %q", act.Forced)
	}
	reportForced(t, a, s)
	if act := a.Activity(); act.Forced != "" {
		t.Errorf("Forced %q after the Origin reported it", act.Forced)
	}
}

// A schedule's run in progress is still offered for download; only a single
// object's is left out (queryPending).
func TestPendingDuringSchedule(t *testing.T) {
	a, _, s := connectedTo(t, func(s *fake.Server) {
		s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_LIST", "ImagingInfo": fake.Msg{"ImageUuid": "now"}})
	})
	a.remember("now")
	a.queryPending(a.client) // the connect's own may run too: every one must ask for it
	got := received(s, "ImageServer", "GetAvailableStackedMasters")
	if len(got) == 0 || fmt.Sprint(got[len(got)-1]["UuidList"]) != "[now]" {
		t.Errorf("asked %v, want the schedule's run included", got)
	}
}

// Finish needs a single object's run; Cancel is not offered for a
// schedule's recapture (RECAPTURE_OBJECT).
func TestFinishCancelStates(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IMAGING_LIST", "ImagingInfo": fake.Msg{"ImageUuid": "u"}})
	if a.FinishImaging() == nil {
		t.Error("Finish accepted for a schedule")
	}
	setTask(t, a, s, fake.Msg{"State": "RECAPTURE_OBJECT"})
	if err := a.CancelImaging(); !errors.Is(err, errShown) {
		t.Errorf("Cancel during a recapture: %v", err)
	}
	if len(received(s, "TaskController", "HaltTasks"))+len(received(s, "TaskController", "CancelImaging")) != 0 {
		t.Error("cancel sent during a recapture")
	}
}

// A timed run's auto-finish survives a reconnect to the same Origin, and
// joining a run in progress asks for nothing in snapshot mode.
func TestTimedRunSurvivesReconnect(t *testing.T) {
	a, _, s := connected(t)
	setMountAlt(t, a, s, 30)
	if err := a.Image(ImageRequest{DurationMin: 1}); err != nil {
		t.Fatal(err)
	}
	uuid, _ := runImagings(s)[0]["Uuid"].(string)
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": uuid}})
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, err := a.cl(); err == nil {
			break
		}
	}
	waitTask(t, a, "IMAGING_OBJECT")
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		for _, g := range received(s, "ImageServer", "GetFinalStackedMaster") {
			if g["ImageUuid"] == uuid && a.Activity().Text == "Requesting Final Image…" {
				return
			}
		}
	}
	t.Errorf("timed run not finished after the reconnect: %v", received(s, "ImageServer", "GetFinalStackedMaster"))
}

func TestJoinRunNotInSnapshotMode(t *testing.T) {
	a, _, s := connected(t)
	st := a.Settings()
	st.Snapshot = true
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": "u"}})
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, err := a.cl(); err == nil {
			break
		}
	}
	waitTask(t, a, "IMAGING_OBJECT")
	time.Sleep(100 * time.Millisecond)
	if g := received(s, "ImageServer", "GetFinalStackedMaster"); len(g) != 0 {
		t.Errorf("joined the run in snapshot mode: %v", g)
	}
}

// While the telescope moves, the capture button's end-imaging choices are
// refused first with "Telescope in Motion" (captureImageButtonTouched).
func TestEndImagingWhileMoving(t *testing.T) {
	a, rec, s := connected(t)
	startRun(t, a, rec, s)
	s.SetStatus("Mount", fake.Msg{"IsGotoOver": false})
	s.NotifyStatus("Mount")
	for end := time.Now().Add(2 * time.Second); a.NotMoving() == nil && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if err := a.NotMoving(); !errors.Is(err, errMoving) {
		t.Errorf("NotMoving while slewing: %v", err)
	}
	if err := a.FinishImaging(); !errors.Is(err, errMoving) {
		t.Errorf("Finish while slewing: %v", err)
	}
	s.SetStatus("Mount", fake.Msg{"IsGotoOver": true})
	s.NotifyStatus("Mount")
	for end := time.Now().Add(2 * time.Second); a.NotMoving() != nil && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if err := a.NotMoving(); err != nil {
		t.Errorf("NotMoving at rest: %v", err)
	}
}

// A snapshot that cannot be saved gets the app's "Error Saving Image" and is
// closed all the same (SaveDeleteImageFragment).
func TestSnapshotSaveFailure(t *testing.T) {
	a, rec, s := connected(t)
	st := a.Settings()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st.SaveDir = filepath.Join(blocker, "out") // under a file: cannot be created
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	s.SetFile("snap.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "snap.jpg", "ImageType": "SAMPLE_CAPTURE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "snapshot" })
	if _, err := a.SaveSnapshot(); err == nil || err.Error() != "alert:Error Saving Image: Could not save image." {
		t.Errorf("save failure: %v", err)
	}
	if a.Activity().Image == kindSnapshot {
		t.Error("snapshot still shown after a failed save")
	}
}
