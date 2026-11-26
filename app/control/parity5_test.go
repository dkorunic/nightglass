// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// connectedTo connects a new app to a fake prepared by prep before the
// connection, so the on-connect queries already see its answers.
func connectedTo(t *testing.T, prep func(*fake.Server)) (*App, *recorder, *fake.Server) {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	prep(s)
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	a.discover = func(context.Context) ([]string, error) { return []string{s.Host()}, nil } // found again after a drop
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); c.TaskState() == "" && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}

	return a, rec, s
}

func countEvents(rec *recorder, name string) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := 0
	for _, e := range rec.got {
		if e.name == name {
			n++
		}
	}

	return n
}

// AD: demo mode initializes with FakeInitialize, refused when already on
// and unless idle.
func TestDemoMode(t *testing.T) {
	a, _, s := connected(t)
	if err := a.DemoMode(); err != nil {
		t.Fatal(err)
	}
	m := lastSent(t, s, "TaskController", "RunInitialize")
	if m["FakeInitialize"] != true || m["Latitude"] == nil {
		t.Errorf("RunInitialize %v", m)
	}
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsFakeInitialized": true})
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if m, _ := c.Last("TaskController/GetStatus"); m.Fields()["IsFakeInitialized"] == true {
			break
		}
	}
	if err := a.DemoMode(); err == nil || !strings.Contains(err.Error(), "Demo Mode is already active.") {
		t.Errorf("already on: %v", err)
	}
	setTask(t, a, s, fake.Msg{"State": "FOCUSING", "IsFakeInitialized": false})
	if a.DemoMode() == nil {
		t.Error("demo mode while busy")
	}
	if sentCount(s, "TaskController", "RunInitialize", 1) != 1 {
		t.Error("refused demo modes were sent")
	}
}

// AC: an equatorial Origin that finishes initializing gets the app's
// polar alignment recommendation, once per initialization.
func TestPolarAlignmentRecommended(t *testing.T) {
	a, rec, s := connectedTo(t, func(s *fake.Server) {
		s.SetInfo("Mount/GetMountConfig", fake.Msg{"IsEquatorial": true, "OnWedge": true})
	})
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if mc, ok := c.MountConfig(); ok && mc.IsEquatorial {
			break
		}
	}
	done := fake.Msg{"State": "INITIALIZING", "Stage": "COMPLETE"}
	setTask(t, a, s, fake.Msg{"State": "INITIALIZING", "Stage": "IN_PROGRESS"})
	s.SetStatus("TaskController", done)
	s.NotifyStatus("TaskController")
	s.NotifyStatus("TaskController") // repeated: still one recommendation
	rec.wait(t, "polar", func(any) bool { return true })
	setTask(t, a, s, fake.Msg{"State": "IDLE", "Stage": "COMPLETE"})
	if n := countEvents(rec, "polar"); n != 1 {
		t.Errorf("%d recommendations, want 1", n)
	}
}

func TestPolarAlignmentNotRecommendedAltAz(t *testing.T) {
	a, rec, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "INITIALIZING", "Stage": "COMPLETE"})
	time.Sleep(50 * time.Millisecond)
	if countEvents(rec, "polar") != 0 {
		t.Error("polar alignment recommended on an alt-az Origin")
	}
}

// AC: polar alignment runs while idle; ending it halts the task so the
// Origin re-initializes.
func TestPolarAlign(t *testing.T) {
	a, _, s := connected(t)
	if err := a.PolarAlign(); err != nil {
		t.Fatal(err)
	}
	if len(received(s, "TaskController", "PolarAlign")) != 1 {
		t.Fatal("no TaskController/PolarAlign")
	}
	if err := a.EndPolarAlignment(); err != nil {
		t.Fatal(err)
	}
	if len(received(s, "TaskController", "HaltTasks")) != 0 {
		t.Error("halted although not polar aligning")
	}
	setTask(t, a, s, fake.Msg{"State": "POLAR_ALIGNING"})
	if a.PolarAlign() == nil {
		t.Error("polar align while busy")
	}
	if err := a.EndPolarAlignment(); err != nil {
		t.Fatal(err)
	}
	if sentCount(s, "TaskController", "HaltTasks", 1) != 1 {
		t.Error("ending polar alignment did not halt")
	}
}

// Z: dark and flat frames, with the app's defaults.
func TestDarksAndFlats(t *testing.T) {
	a, _, s := connected(t)
	if d := a.DarkDefaults(); d.ISO != 200 || d.ExposureSec != 10 || d.Count != 10 {
		t.Errorf("automatic-mode defaults %+v", d)
	}
	setSettings(t, a, func(s *Settings) { s.ManualImaging, s.ISO, s.ExposureSec = true, 1000, 4 })
	if d := a.DarkDefaults(); d.ISO != 1000 || d.ExposureSec != 4 || d.Count != 10 {
		t.Errorf("manual-mode defaults %+v", d)
	}
	if err := a.CaptureDarks(800, 5, 20); err != nil {
		t.Fatal(err)
	}
	m := lastSent(t, s, "TaskController", "RunGenerateNewDarks")
	if m["ISO"] != 800.0 || m["ExposureTime"] != 5.0 || m["NbrSubs"] != 20.0 {
		t.Errorf("RunGenerateNewDarks %v", m)
	}
	for _, bad := range [][2]float64{{0, 5}, {101, 5}, {10, 0}} {
		if a.CaptureDarks(800, bad[1], int(bad[0])) == nil {
			t.Errorf("darks %v accepted", bad)
		}
	}
	if err := a.RecaptureAllDarks(); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RunGenerateNewDarks"); m["NbrSubs"] != nil || m["ISO"] != nil {
		t.Errorf("complete set %v, want no fields", m)
	}
	if err := a.CaptureFlats(); err != nil {
		t.Fatal(err)
	}
	if len(received(s, "TaskController", "RunGenerateNewFlat")) != 1 {
		t.Error("no RunGenerateNewFlat")
	}
	setTask(t, a, s, fake.Msg{"State": "GENERATING_DARKS"})
	for _, f := range []func() error{a.RecaptureAllDarks, a.CaptureFlats, func() error { return a.CaptureDarks(800, 5, 20) }} {
		if f() == nil {
			t.Error("calibration frames while busy")
		}
	}
}

// AA: version, model, serial number and update settings; the Origin is
// asked on connect, as the native library does.
func TestOriginInfo(t *testing.T) {
	a, _, s := connectedTo(t, func(s *fake.Server) {
		s.SetInfo("System/GetVersion", fake.Msg{"Number": "1.2.3"})
		s.SetInfo("System/GetModel", fake.Msg{"Value": "Origin"})
		s.SetInfo("System/GetUpdateChannel", fake.Msg{"Value": "beta"})
		s.SetInfo("System/GetAutomaticUpdates", fake.Msg{"Enabled": true})
		s.SetInfo("Network/HasInternetConnection", fake.Msg{"Value": true})
		s.SetInfo("FactoryCalibrationController/GetSerialNumber", fake.Msg{"SerialNumber": "OR-123"})
	})
	if m, ok := s.Wait(fake.Is("System", "HasUpdateAvailable"), time.Second); !ok || m["ForceCheck"] != true {
		t.Errorf("HasUpdateAvailable on connect: %v", m)
	}
	o, err := a.OriginSettings()
	if err != nil {
		t.Fatal(err)
	}
	if o.Version != "1.2.3" || o.Model != "Origin" || o.Serial != "OR-123" || o.Channel != "beta" ||
		o.AutoUpdates == nil || !*o.AutoUpdates || o.Internet == nil || !*o.Internet {
		t.Errorf("info %+v", o)
	}
	if err := a.SetAutoUpdates(false); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "System", "SetAutomaticUpdates"); m["Enabled"] != false {
		t.Errorf("SetAutomaticUpdates %v", m)
	}
	if err := a.SetUpdateChannel("stable"); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "System", "SetUpdateChannel"); m["Value"] != "stable" {
		t.Errorf("SetUpdateChannel %v", m)
	}
	if a.SetUpdateChannel("") == nil {
		t.Error("empty channel accepted")
	}
	// The channel defaults to the app's "stable" when the Origin has none.
	s.SetInfo("System/GetUpdateChannel", fake.Msg{"Value": ""})
	if o, _ := a.OriginSettings(); o.Channel != "stable" {
		t.Errorf("default channel %q", o.Channel)
	}
}

// AB: once an update is installed the Origin restarts: the app waits,
// counting down, then reconnects.
func TestReconnectAfterUpdate(t *testing.T) {
	a, rec, s := connected(t)
	a.restartDelay = 1500 * time.Millisecond
	s.Notify("System", "UpdateStatus", fake.Msg{"Status": "UPDATE_DOWNLOADING", "PercentComplete": 100})
	rec.wait(t, "status", func(d any) bool { return d.(Status).Key == "System/UpdateStatus" })
	time.Sleep(50 * time.Millisecond)
	if c, err := a.cl(); err != nil || !c.Connected() {
		t.Fatal("a finished download disconnected")
	}
	s.Notify("System", "UpdateStatus", fake.Msg{"Status": "UPDATE_INSTALLING", "PercentComplete": 100})
	rec.wait(t, "activity", func(d any) bool {
		return d.(Activity).Text == "Reconnecting to Origin in 1…" || d.(Activity).Text == "Reconnecting to Origin in 2…"
	})
	if _, err := a.cl(); err == nil {
		t.Error("still connected while the Origin restarts")
	}
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text == "Origin Restarting…" })
	n := countEvents(rec, "conn")
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if c, err := a.cl(); err == nil && c.Connected() && countEvents(rec, "conn") > n {
			return
		}
	}
	t.Fatal("did not reconnect after the restart")
}

// AB: a connect or disconnect by the user ends the wait.
func TestRestartWaitCancelled(t *testing.T) {
	a, rec, s := connected(t)
	a.restartDelay = 1200 * time.Millisecond
	s.Notify("System", "UpdateStatus", fake.Msg{"Status": "UPDATE_INSTALLING", "PercentComplete": 100})
	rec.wait(t, "activity", func(d any) bool { return strings.HasPrefix(d.(Activity).Text, "Reconnecting to Origin in") })
	a.Disconnect()
	time.Sleep(1500 * time.Millisecond)
	if _, err := a.cl(); err == nil {
		t.Error("reconnected after the user disconnected")
	}
	if got := a.Activity().Text; got != "No Connection" {
		t.Errorf("status %q", got)
	}
}
