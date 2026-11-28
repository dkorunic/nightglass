// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"errors"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// setReady sets the fake's IsReady while idle and waits for the client.
func setReady(t *testing.T, a *App, s *fake.Server, ready bool) {
	t.Helper()
	s.SetStatus("TaskController", fake.Msg{"State": "IDLE", "IsReady": ready})
	s.NotifyStatus("TaskController")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if m, ok := c.Last("TaskController/GetStatus"); ok && m.Fields()["State"] == "IDLE" && m.Fields()["IsReady"] == ready {
			return
		}
	}
	t.Fatal("IsReady not cached")
}

// The initialize button as the app's: only while idle and not initialized;
// it cancels while initializing. Re-initializing goes through settings.
func TestInitializeButtonLikeApp(t *testing.T) {
	a, _, s := connected(t)
	if a.Activity().Init != "" || a.Initialize() == nil {
		t.Error("initialize offered on an initialized Origin")
	}
	setReady(t, a, s, false)
	if got := a.Activity().Init; got != "Re-Initialize Telescope" {
		t.Errorf("button %q", got)
	}
	if err := a.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Wait(fake.Is("TaskController", "RunInitialize"), time.Second); !ok { // posted, as the native library
		t.Fatal("no RunInitialize sent")
	}
	setTask(t, a, s, fake.Msg{"State": "INITIALIZING"})
	if got := a.Activity().Init; got != "Cancel Initialization" {
		t.Errorf("button %q", got)
	}
	if err := a.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Wait(fake.Is("TaskController", "HaltTasks"), time.Second); !ok {
		t.Fatal("no HaltTasks sent")
	}
	if got := a.Activity().Init; got != "Cancelling Initialization…" {
		t.Errorf("button %q", got)
	}
	setReady(t, a, s, true)
	if err := a.Reinitialize(); err != nil {
		t.Fatal(err)
	}
	if n := sentCount(s, "TaskController", "RunInitialize", 2); n != 2 {
		t.Errorf("%d RunInitialize, want 2", n)
	}
	setTask(t, a, s, fake.Msg{"State": "FOCUSING"})
	if a.Reinitialize() == nil || a.Initialize() == nil {
		t.Error("initialized while busy")
	}
}

// Start Imaging needs an initialized Origin; a snapshot only an idle one.
func TestImageNeedsInitialization(t *testing.T) {
	a, _, s := connected(t)
	setReady(t, a, s, false)
	if err := a.Image(ImageRequest{}); !errors.Is(err, errShown) { // the app returns silently
		t.Errorf("imaging before initialization: %v", err)
	}
	setSettings(t, a, func(st *Settings) { st.Snapshot = true })
	if err := a.Image(ImageRequest{}); err != nil {
		t.Errorf("snapshot before initialization: %v", err)
	}
}

// While imaging the camera settings are locked, as the app's menu, and the
// camera mode is not sent; dither values end at 30 and 10.
func TestCameraSettingsLockedWhileImaging(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	st := a.Settings()
	st.ManualImaging, st.ExposureSec, st.ISO, st.Snapshot, st.Night = !st.ManualImaging, 20, 1000, !st.Snapshot, true
	st.DitherDistance, st.DitherInterval = 99, 99
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	got := a.Settings()
	if got.ManualImaging == st.ManualImaging || got.ExposureSec == 20 || got.ISO == 1000 || got.Snapshot == st.Snapshot || !got.Night {
		t.Errorf("camera settings changed mid-run: %+v", got)
	}
	if got.DitherDistance != 30 || got.DitherInterval != 10 {
		t.Errorf("dither %d %d, want 30 10", got.DitherDistance, got.DitherInterval)
	}
	n := len(received(s, "LiveStream", "SetEnableAuto")) + len(received(s, "LiveStream", "SetEnableManual"))
	if a.ApplyCapture() == nil {
		t.Error("camera mode applied mid-run")
	}
	if len(received(s, "LiveStream", "SetEnableAuto"))+len(received(s, "LiveStream", "SetEnableManual")) != n {
		t.Error("camera mode sent mid-run")
	}
}

// Joining a run in progress asks for its image at once.
func TestReconnectToRunAsksForImage(t *testing.T) {
	_, _, s := connectedTo(t, func(s *fake.Server) {
		s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": "run-9"}})
	})
	m, ok := s.Wait(fake.Is("ImageServer", "GetFinalStackedMaster"), 3*time.Second)
	if !ok || m["ImageUuid"] != "run-9" {
		t.Errorf("GetFinalStackedMaster %v", m)
	}
}

// The ISO picker: the saved ISO, else the camera's default; disabled until
// the camera reports its range.
func TestISOChoices(t *testing.T) {
	a, _ := newApp(t)
	if c := a.ISOChoices(); c.Known || c.Value != 200 {
		t.Errorf("offline: %+v", c)
	}
	b, _, _ := connectedTo(t, func(s *fake.Server) {
		s.SetInfo("Camera/GetCameraInfo", fake.Msg{"MinIso": 100, "MaxIso": 5000, "DefaultIso": 500})
	})
	cl, _ := b.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ci, _ := cl.CameraInfo(); ci.MaxIso > 0 {
			break
		}
	}
	if c := b.ISOChoices(); !c.Known || c.Value != 500 || c.Values[len(c.Values)-1] != 5000 {
		t.Errorf("connected: %+v", c)
	}
}

// A snapshot that cannot be downloaded says so, as the app's.
func TestSnapshotDownloadFailure(t *testing.T) {
	_, rec, s := connected(t)
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "snap/missing.jpg", "ImageType": "SAMPLE_CAPTURE"})
	rec.wait(t, "toast", func(d any) bool { return d.(Toast).Message == "Unable to load Image" })
}

// A cooled camera (the Origin-585C-TEC, HasCooling) is cooled as Celestron's
// web app does: Camera/SetCooling with CoolingEnabled and, when on,
// TemperatureTarget; the choice is a Nightglass preference, sent again on
// each connect. A camera without a cooler is not sent one.
func TestCameraCooling(t *testing.T) {
	a, _, s := connectedTo(t, func(s *fake.Server) {
		s.SetInfo("Camera/GetCameraInfo", fake.Msg{"HasCooling": true})
	})
	cl, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ci, _ := cl.CameraInfo(); ci.HasCooling {
			break
		}
	}
	if len(received(s, "Camera", "SetCooling")) != 0 {
		t.Error("cooling sent before it was chosen")
	}
	if err := a.SetCooling(true, -5); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Camera", "SetCooling"); m["CoolingEnabled"] != true || m["TemperatureTarget"] != -5.0 {
		t.Errorf("SetCooling %v", m)
	}
	if err := a.SetCooling(false, -5); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Camera", "SetCooling"); m["CoolingEnabled"] != false || m["TemperatureTarget"] != nil {
		t.Errorf("SetCooling off %v", m)
	}
	if a.SetCooling(true, -40) == nil {
		t.Error("a -40 °C target accepted")
	}
	// Chosen, it is sent again on the next connect.
	setSettings(t, a, func(s *Settings) { s.Cooling, s.CoolingTargetC = true, -10 })
	n := len(received(s, "Camera", "SetCooling"))
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Wait(func(m fake.Msg) bool {
		return fake.Is("Camera", "SetCooling")(m) && len(received(s, "Camera", "SetCooling")) > n
	}, 3*time.Second)
	if !ok || m["CoolingEnabled"] != true {
		t.Fatalf("cooling not sent on connect: %v", m)
	}
	if m := lastSent(t, s, "Camera", "SetCooling"); m["TemperatureTarget"] != -10.0 {
		t.Errorf("SetCooling on connect %v", m)
	}
	// Without a cooler: refused, and nothing is sent on connect.
	b, _, s2 := connected(t)
	setSettings(t, b, func(s *Settings) { s.Cooling = true })
	if b.SetCooling(true, 0) == nil {
		t.Error("cooling a camera without a cooler accepted")
	}
	if len(received(s2, "Camera", "SetCooling")) != 0 {
		t.Error("SetCooling sent to a camera without a cooler")
	}
}
