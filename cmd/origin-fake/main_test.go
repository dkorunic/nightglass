// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

// One imaging cycle as the app runs it: stacked frames while imaging, the
// final TIFF after HaltTasks + GetFinalStackedMaster (FinishImaging).
func TestImagingCycle(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	newSim(s, 20*time.Millisecond)
	c := origin.New(s.Host())
	c.PollInterval = -1
	sub := c.Subscribe()
	ctx := t.Context()
	go func() { _ = c.Run(ctx) }()
	for !c.Connected() {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.RunImaging(ctx, origin.Imaging{Name: "M 42"}); err != nil {
		t.Fatal(err)
	}
	want := "STACKED_MASTER"
	for end := time.After(3 * time.Second); ; {
		select {
		case m := <-sub:
			var info origin.ImageReady
			if m.Key() != "ImageServer/NewImageReady" || m.Decode(&info) != nil || info.ImageType != want {
				continue
			}
			f, err := origin.Fetch(ctx, http.DefaultClient, s.Host(), info)
			if err != nil {
				t.Fatalf("%s not served: %v", want, err)
			}
			// Where it points, as a real Origin reports it.
			if math.Abs(info.FovX-fake.OriginField*math.Pi/180) > 1e-9 || info.FovY == 0 || math.Abs(info.Orientation-fake.OriginOrientation*math.Pi/180) > 1e-9 {
				t.Errorf("%s field %v × %v at %v", want, info.FovX, info.FovY, info.Orientation)
			}
			// A star field of the Origin's shape.
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(f.Display)); err != nil || cfg.Width != fullW || cfg.Height != fullH {
				t.Errorf("%s is %v %v, want %d × %d", want, cfg, err, fullW, fullH)
			}
			// The run's object reports its stack for the status line.
			var ts origin.TaskStatus
			if m, _ := c.Send(ctx, "TaskController", "GetStatus", nil); want == "STACKED_MASTER" && (m.Decode(&ts) != nil || ts.ImagingInfo.Current() == nil || ts.ImagingInfo.Current().StackDepth == nil) {
				t.Errorf("no current object in %s", m.Raw)
			}
			if want == "FINAL_STACKED_MASTER" {
				return
			}
			want = "FINAL_STACKED_MASTER"
			if err := c.FinishImaging(ctx, ""); err != nil {
				t.Fatal(err)
			}
		case <-end:
			t.Fatalf("no %s", want)
		}
	}
}

// CancelImaging discards the run: no final image, and it is not offered as
// an undownloaded image afterwards.
func TestCancelDiscards(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	newSim(s, 10*time.Millisecond)
	c := origin.New(s.Host())
	c.PollInterval = -1
	ctx := t.Context()
	go func() { _ = c.Run(ctx) }()
	for !c.Connected() {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.RunImaging(ctx, origin.Imaging{Name: "A", Uuid: "keep"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := c.Send(ctx, "TaskController", "HaltTasks", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.RunImaging(ctx, origin.Imaging{Name: "B", Uuid: "drop"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := c.CancelImaging(ctx); err != nil {
		t.Fatal(err)
	}
	avail, err := c.AvailableStackedMasters(ctx, []string{"keep", "drop"})
	if err != nil || len(avail) != 1 || avail[0] != "keep" {
		t.Errorf("available after halt/cancel: %v, %v", avail, err)
	}
}

func simClient(t *testing.T) (*origin.Client, <-chan origin.Message, context.Context) {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	sim := newSim(s, 20*time.Millisecond)
	sim.step = 10 * time.Millisecond
	c := origin.New(s.Host())
	c.PollInterval = -1
	sub := c.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	for !c.Connected() {
		time.Sleep(5 * time.Millisecond)
	}

	return c, sub, ctx
}

// waitFor returns the first message with key whose fields satisfy ok.
func waitFor(t *testing.T, sub <-chan origin.Message, key string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	for end := time.After(3 * time.Second); ; {
		select {
		case m := <-sub:
			if m.Key() == key && ok(m.Fields()) {
				return m.Fields()
			}
		case <-end:
			t.Fatalf("no %s", key)
		}
	}
}

// Snapshots and reframing, as the app's imaging screen uses them.
func TestSnapshotAndCenter(t *testing.T) {
	c, sub, ctx := simClient(t)
	if _, err := c.Send(ctx, "TaskController", "RunSampleCapture", nil); err != nil {
		t.Fatal(err)
	}
	f := waitFor(t, sub, "ImageServer/NewImageReady", func(f map[string]any) bool { return f["ImageType"] == "SAMPLE_CAPTURE" })
	var info origin.ImageReady
	info.FileLocation = f["FileLocation"].(string)
	if _, err := origin.Fetch(ctx, http.DefaultClient, c.Host(), info); err != nil {
		t.Fatalf("snapshot not served: %v", err)
	}
	if _, err := c.Send(ctx, "TaskController", "RunCenterTarget", map[string]any{"OffsetX": 0.3, "OffsetY": 0.6}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, sub, "TaskController/GetStatus", func(f map[string]any) bool { return f["State"] == "CENTERING_TARGET" })
	waitFor(t, sub, "TaskController/GetStatus", func(f map[string]any) bool { return f["State"] == "IDLE" })
}

// The file manager: a finished run's folder is listed, re-downloaded,
// copied to USB with progress, and deleted.
func TestImageDirectories(t *testing.T) {
	c, sub, ctx := simClient(t)
	if err := c.RunImaging(ctx, origin.Imaging{Name: "M 42", Uuid: "run-1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, sub, "ImageServer/NewImageReady", func(f map[string]any) bool { return f["ImageType"] == "STACKED_MASTER" })
	if err := c.HaltTasks(ctx); err != nil {
		t.Fatal(err)
	}
	list := func() []any {
		m, err := c.Send(ctx, "ImageServer", "GetListOfAvailableDirectories", nil)
		if err != nil {
			t.Fatal(err)
		}
		l, _ := m.Fields()["DirectoryList"].([]any)

		return l
	}
	dirs := list()
	for end := time.Now().Add(2 * time.Second); len(dirs) == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		dirs = list() // HaltTasks is posted, so the run ends a moment later
	}
	if len(dirs) != 1 {
		t.Fatalf("directories %v", dirs)
	}
	m, err := c.Send(ctx, "ImageServer", "GetStackedMasterFromDirectory", map[string]any{"DirectoryName": dirs[0]})
	if err != nil || m.Fields()["FileExists"] != true {
		t.Fatalf("GetStackedMasterFromDirectory %v %v", m.Raw, err)
	}
	waitFor(t, sub, "ImageServer/NewImageReady", func(f map[string]any) bool { return f["ImageType"] == "FINAL_STACKED_MASTER" })
	if _, err := c.Send(ctx, "TaskController", "CopyAllDirectoriesToFlashDrive", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, sub, "TaskController/FileTransferStatus", func(f map[string]any) bool { return f["PercentageComplete"] == 100.0 })
	if _, err := c.Send(ctx, "ImageServer", "DeleteImageDirectory", map[string]any{"DirectoryName": dirs[0]}); err != nil {
		t.Fatal(err)
	}
	if dirs := list(); len(dirs) != 0 {
		t.Errorf("after delete: %v", dirs)
	}
}

// Initialization (and demo mode), polar alignment and calibration frames,
// as the app's settings run them.
func TestInitPolarAndCalibration(t *testing.T) {
	c, sub, ctx := simClient(t)
	state := func(want string) map[string]any {
		return waitFor(t, sub, "TaskController/GetStatus", func(f map[string]any) bool { return f["State"] == want })
	}
	if _, err := c.Send(ctx, "TaskController", "RunInitialize", map[string]any{"FakeInitialize": true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, sub, "TaskController/GetStatus", func(f map[string]any) bool { return f["State"] == "INITIALIZING" && f["Stage"] == "COMPLETE" })
	if f := state("IDLE"); f["IsFakeInitialized"] != true || f["IsReady"] != true {
		t.Errorf("after demo initialization %v", f)
	}
	if _, err := c.Send(ctx, "TaskController", "PolarAlign", nil); err != nil {
		t.Fatal(err)
	}
	if f := state("POLAR_ALIGNING"); f["PolarAlignInfo"] == nil {
		t.Errorf("polar alignment without errors: %v", f)
	}
	if err := c.HaltTasks(ctx); err != nil {
		t.Fatal(err)
	}
	state("IDLE")
	for _, cmd := range [][2]string{{"RunGenerateNewDarks", "GENERATING_DARKS"}, {"RunGenerateNewFlat", "GENERATING_FLATS"}} {
		if _, err := c.Send(ctx, "TaskController", cmd[0], nil); err != nil {
			t.Fatal(err)
		}
		state(cmd[1])
		state("IDLE")
	}
}

// GetLogs: progress, then a LogsReady whose file is served.
func TestLogs(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, 20*time.Millisecond)
	m.step = 10 * time.Millisecond
	c := origin.New(s.Host())
	c.PollInterval = -1
	sub := c.Subscribe()
	ctx := t.Context()
	go func() { _ = c.Run(ctx) }()
	for !c.Connected() {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := c.Send(ctx, "System", "GetLogs", nil); err != nil {
		t.Fatal(err)
	}
	progress := false
	for end := time.After(3 * time.Second); ; {
		select {
		case msg := <-sub:
			progress = progress || msg.Key() == "TaskController/FileTransferStatus"
			var lr struct{ DownloadLink string }
			if msg.Key() != "System/LogsReady" || msg.Decode(&lr) != nil {
				continue
			}
			if b, err := origin.FetchFile(ctx, http.DefaultClient, s.Host(), lr.DownloadLink); err != nil || len(b) == 0 || !progress {
				t.Fatalf("logs %q, %v, progress %v", b, err, progress)
			}

			return
		case <-end:
			t.Fatal("no LogsReady")
		}
	}
}

// A timed run ends by itself and keeps its master.
func TestTimedRunEnds(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, 20*time.Millisecond)
	c := origin.New(s.Host())
	c.PollInterval = -1
	ctx := t.Context()
	go func() { _ = c.Run(ctx) }()
	for !c.Connected() {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.RunImaging(ctx, origin.Imaging{Uuid: "timed", TotalDurationSec: 0.1}); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		m.mu.Lock()
		_, kept := m.kept["timed"]
		m.mu.Unlock()
		if kept {
			return
		}
	}
	t.Fatal("timed run did not end by itself")
}

// As a real Origin: every status pushed on connect, pings answered, and a
// 1011 "Ping timeout" close followed by a reconnect.
func TestRealisticLink(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	newSim(s, 20*time.Millisecond)
	captured(s)
	s.PingInterval = 20 * time.Millisecond
	mods := []string{"Mount", "Camera", "TaskController", "Environment", "Disk", "DewHeater"}
	s.OnConnect = func() { pushAll(s, mods) }
	c := origin.New(s.Host())
	c.PollInterval, c.MinBackoff = -1, 10*time.Millisecond
	ups := make(chan bool, 8)
	c.OnState = func(ok bool, _ error) { ups <- ok }
	sub := c.Subscribe()
	ctx := t.Context()
	go func() { _ = c.Run(ctx) }()
	if !<-ups {
		t.Fatal("not connected")
	}
	seen := map[string]bool{}
	for end := time.After(3 * time.Second); len(seen) < len(mods); {
		select {
		case m := <-sub:
			if m.Type == origin.TypeNotification {
				seen[m.Module()] = true
			}
		case <-end:
			t.Fatalf("pushed %v", seen)
		}
	}
	if m, _ := c.Last("Focuser/GetStatus"); m.Fields()["Position"] != 18447.0 {
		t.Errorf("captured focuser %v", m.Fields())
	}
	if m, ok := c.Last("Camera/GetCaptureParameters"); !ok || m.Fields()["ISO"] != 2000.0 {
		t.Errorf("camera pushed as GetCaptureParameters: %v", m.Fields())
	}
	time.Sleep(100 * time.Millisecond) // several pings
	if !c.Connected() {
		t.Fatal("pings dropped the link")
	}
	s.CloseAllWith(websocket.StatusInternalError, "Ping timeout")
	if <-ups || !<-ups {
		t.Error("no reconnect after 1011")
	}
}

// A mosaic captures its tiles, lists them, composes into a final image.
func TestMosaicSim(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, 5*time.Millisecond)
	m.step = 5 * time.Millisecond
	c := origin.New(s.Host())
	c.PollInterval = -1
	sub := c.Subscribe()
	ctx := t.Context()
	go func() { _ = c.Run(ctx) }()
	for !c.Connected() {
		time.Sleep(5 * time.Millisecond)
	}
	// Two tiles wide, one high.
	if _, err := c.Send(ctx, "TaskController", "RunCaptureMosaic", map[string]any{"Uuid": "MOS", "ObjectName": "M 31", "MosaicWidth": 0.02, "MosaicHeight": 0.01}); err != nil {
		t.Fatal(err)
	}
	var meta struct {
		MosaicMetadata struct {
			Status                     string
			TilesTotal, TilesCompleted int
		}
	}
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r, err := c.Send(ctx, "Mosaic", "GetMosaicTiles", map[string]any{"MosaicUuid": "MOS"})
		if err == nil && r.Decode(&meta) == nil && meta.MosaicMetadata.Status == "COMPLETED" {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("mosaic did not complete: %+v", meta)
		}
	}
	if meta.MosaicMetadata.TilesTotal != 2 || meta.MosaicMetadata.TilesCompleted != 2 {
		t.Errorf("tiles %+v", meta)
	}
	r, err := c.Send(ctx, "TaskController", "RunComposeMosaic", map[string]any{"Uuid": "MOS"})
	if err != nil || r.Fields()["CompositionUuid"] != "MOS-COMPOSED" {
		t.Fatalf("compose %v %v", r.Fields(), err)
	}
	for end := time.After(3 * time.Second); ; {
		select {
		case msg := <-sub:
			var info origin.ImageReady
			if msg.Key() == "ImageServer/NewImageReady" && msg.Decode(&info) == nil && info.ImageType == "FINAL_STACKED_MASTER" && info.Uuid == "MOS-COMPOSED" {
				return
			}
		case <-end:
			t.Fatal("no composed image")
		}
	}
}

// The frames show where the mount points, and a tile its place in the grid.
func TestFramesFollowMount(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, time.Millisecond)
	m.mount.mu.Lock()
	m.mount.ra, m.mount.dec = 83.8, -5.4
	m.mount.mu.Unlock()
	if f := m.skyHere(liveW, liveH, 1); f.RA != 83.8 || f.Dec != -5.4 || f.Width != fake.OriginField {
		t.Errorf("live field %+v", f)
	}
	if f, g := m.skyHere(liveW, liveH, 1), m.skyHere(liveW, liveH, 1); f.NoiseSeed == g.NoiseSeed {
		t.Error("successive frames share their noise")
	}
	if f := m.field("unknown"); f.Depth != 30 || f.RA != 83.8 {
		t.Errorf("unrecorded master %+v", f)
	}
}

// Reframe's RunCenterTarget moves the mount to the point clicked.
func TestCenterTargetMoves(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, time.Millisecond)
	m.mount.mu.Lock()
	m.mount.ra, m.mount.dec, m.mount.tracking = 83.8, -5.4, true
	m.mount.mu.Unlock()
	wantRA, wantDec := m.skyHere(fullW, fullH, 1).At(0.8, 0.3)
	m.handle(fake.Msg{"Destination": "TaskController", "Command": "RunCenterTarget", "OffsetX": 0.8, "OffsetY": 0.3})
	if st := s.Status("TaskController")["State"]; st != "CENTERING_TARGET" {
		t.Errorf("state %v while centering", st)
	}
	for end := time.Now().Add(3 * time.Second); s.Status("TaskController")["State"] != "IDLE"; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("centering never ended")
		}
	}
	if ra, dec := m.mount.pointing(); math.Abs(ra-wantRA) > 1e-6 || math.Abs(dec-wantDec) > 1e-6 {
		t.Errorf("centred on %v/%v, want %v/%v", ra, dec, wantRA, wantDec)
	}
	// The new frames' centre is the old frames' point clicked.
	if f := m.skyHere(fullW, fullH, 1); math.Abs(f.RA-wantRA) > 1e-6 || f.Rotation != fake.OriginOrientation {
		t.Errorf("new field %+v", f)
	}
	if msg := withField(fake.Msg{}, m.skyHere(fullW, fullH, 1)); math.Abs(msg["Dec"].(float64)-wantDec*math.Pi/180) > 1e-9 {
		t.Errorf("NewImageReady fields %v", msg)
	}
}

// Frames go where a real Origin puts them: live and snapshot frames reuse
// Images/Temp/0–9.jpg, a run's masters go to its folder.
func TestFilePaths(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, time.Millisecond)
	first := m.tempFile()
	for range 9 {
		m.tempFile()
	}
	if first != "Images/Temp/1.jpg" || m.tempFile() != first {
		t.Errorf("temp files do not cycle through ten: first %q", first)
	}
	if got := finalFile("0123456789", "M 42"); got != "Images/Astrophotography/M 42_01234567/FinalStackedMaster.tiff" {
		t.Errorf("final file %q", got)
	}
}

// The environment readings vary a little, within the sensors' bounds.
func TestJitter(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetStatus("Environment", fake.Msg{"AmbientTemperature": 16.0, "CpuTemperature": 45.0, "DewPoint": 8.0, "Humidity": 67.0})
	changed := false
	for range 200 {
		jitter(s)
		st := s.Status("Environment")
		a, c, d := st["AmbientTemperature"].(float64), st["CpuTemperature"].(float64), st["DewPoint"].(float64)
		if a < 15 || a > 17 || c < 42 || c > 45 || math.Abs(d-8) > 0.6 || st["Humidity"] != 67.0 {
			t.Fatalf("out of bounds: %v", st)
		}
		changed = changed || a != 16
	}
	if !changed {
		t.Error("readings never changed")
	}
}

// Settings sent are taken: the camera's are reported back, tracking turns
// on and off, and the regulatory domain is kept.
func TestSettingsTaken(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newSim(s, time.Millisecond)
	m.handle(fake.Msg{"Destination": "Camera", "Command": "SetCaptureParameters", "Exposure": 30.0, "ISO": 800.0})
	if st := s.Status("Camera"); st["Exposure"] != 30.0 || st["ISO"] != 800.0 || st["Binning"] != 1 {
		t.Errorf("camera status %v", st)
	}
	m.handle(fake.Msg{"Destination": "Mount", "Command": "EnableTracking", "Value": true})
	if on, _ := s.Status("Mount")["IsTracking"].(bool); !on {
		t.Error("tracking not on")
	}
	m.handle(fake.Msg{"Destination": "Mount", "Command": "EnableTracking", "Value": false})
	if on, _ := s.Status("Mount")["IsTracking"].(bool); on {
		t.Error("tracking not off")
	}
	m.handle(fake.Msg{"Destination": "Network", "Command": "SetRegulatoryDomain", "CountryCode": "HR"})
	if m.country != "HR" {
		t.Errorf("country %q", m.country)
	}
}
