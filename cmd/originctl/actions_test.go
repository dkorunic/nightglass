// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dkorunic/nightglass/origin/fake"
)

// last returns the newest command dest/cmd the fake received.
func last(t *testing.T, s *fake.Server, dest, cmd string) fake.Msg {
	t.Helper()
	m, ok := s.Wait(fake.Is(dest, cmd), time.Second)
	if !ok {
		t.Fatalf("no %s/%s sent", dest, cmd)
	}
	for _, r := range s.Received() {
		if fake.Is(dest, cmd)(r) {
			m = r
		}
	}

	return m
}

// The Moon and planets: goto where they are now, image with their track
// and magnitude, as the GUI does.
func TestSolarSystemTargets(t *testing.T) {
	s := newFake(t)
	if code, _, errs := ctl(t, s, "goto", "-target", "jupiter", "-lat", "45.8", "-lon", "16"); code != 0 {
		t.Fatalf("goto: exit %d: %s", code, errs)
	}
	last(t, s, "Mount", "GotoRaDec")
	if code, _, errs := ctl(t, s, "image", "-target", "Venus"); code != 0 {
		t.Fatalf("image: exit %d: %s", code, errs)
	}
	m := last(t, s, "TaskController", "RunImaging")
	tr, _ := m["TargetTrack"].([]any)
	if m["Name"] != "Venus" || len(tr) != 16 || m["ObjectMagnitude"].(float64) >= 0 {
		t.Errorf("Venus run %v", m)
	}
	if _, ok := m["ObjectSize"]; ok {
		t.Error("ObjectSize sent for a planet")
	}
}

// Each GUI action has a command sending what the GUI sends.
func TestActions(t *testing.T) {
	s := newFake(t)
	for _, c := range []struct {
		args      []string
		dest, cmd string
		want      map[string]any
	}{
		{[]string{"snapshot", "-manual", "-exp", "0.5", "-iso", "500"}, "TaskController", "RunSampleCapture", map[string]any{"ExposureTime": 0.5, "ISO": 500.0}},
		{[]string{"init", "-lat", "45", "-lon", "16", "-tz", "Europe/Zagreb", "-demo"}, "TaskController", "RunInitialize", map[string]any{"FakeInitialize": true}},
		{[]string{"polar"}, "TaskController", "PolarAlign", nil},
		{[]string{"darks", "-iso", "200", "-exp", "10", "-n", "5"}, "TaskController", "RunGenerateNewDarks", map[string]any{"NbrSubs": 5.0, "ExposureTime": 10.0, "ISO": 200.0}},
		{[]string{"flats"}, "TaskController", "RunGenerateNewFlat", nil},
		{[]string{"updates", "channel", "beta"}, "System", "SetUpdateChannel", map[string]any{"Value": "beta"}},
		{[]string{"updates", "auto", "on"}, "System", "SetAutomaticUpdates", map[string]any{"Enabled": true}},
		{[]string{"center", "0.25", "0.75"}, "TaskController", "RunCenterTarget", map[string]any{"OffsetX": 0.25, "OffsetY": 0.75}},
		{[]string{"stretch", "-strength", "0.7"}, "ImageServer", "SetStretch", map[string]any{"Strength": 0.7}},
		{[]string{"filter", "Nebula"}, "Camera", "SetFilter", map[string]any{"Filter": "Nebula"}},
		{[]string{"focus", "-auto", "-from-origin"}, "TaskController", "RunInfinityAutoFocus", nil},
		{[]string{"files", "rm", "M42_1"}, "ImageServer", "DeleteImageDirectory", map[string]any{"DirectoryName": "M42_1"}},
		{[]string{"files", "usb"}, "TaskController", "CopyAllDirectoriesToFlashDrive", nil},
		{[]string{"set", "wedge", "on"}, "Mount", "SetMountConfig", map[string]any{"OnWedge": true, "IsEquatorial": true}},
		{[]string{"set", "led", "0.3"}, "LedRing", "SetBrightnessLevel", map[string]any{"BrightnessLevel": 0.3}},
		{[]string{"set", "fan", "cpu", "off"}, "Environment", "SetFans", map[string]any{"CpuFanOn": false}},
		{[]string{"set", "af-temp", "on", "3"}, "Focuser", "AutoFocusOnTemperatureChange", map[string]any{"EnableTemperatureShiftAutoFocus": true, "TemperatureThresholdC": 3.0}},
		{[]string{"set", "dew", "auto", "7"}, "DewHeater", "EnableAuto", map[string]any{"Aggression": 7.0}},
		{[]string{"set", "giotto", "40"}, "ElPanel", "SetLightLevel", map[string]any{"LightLevel": 40.0}},
		{[]string{"recalibrate-env"}, "Environment", "RecalibrateEnvironmentSensor", nil},
		{[]string{"poweroff"}, "System", "PowerDownOrigin", nil},
		{[]string{"reboot"}, "System", "Reboot", nil},
		{[]string{"update"}, "System", "Update", map[string]any{"Reboot": true}},
		{[]string{"mosaic", "start", "10.68", "41.27", "120", "60", "90"}, "TaskController", "RunCaptureMosaic", map[string]any{"TotalDuration": 600.0, "ISO": 200.0}},
		{[]string{"mosaic", "recapture", "MOS", "t2", "0", "1"}, "TaskController", "RecaptureMosaicTile", map[string]any{"MosaicUuid": "MOS", "ObjectUuid": "t2", "TileRow": 0.0, "TileCol": 1.0}},
		{[]string{"mosaic", "resume", "MOS"}, "TaskController", "ResumeImagingList", map[string]any{"Uuid": "MOS"}},
		{[]string{"mosaic", "compose", "MOS"}, "TaskController", "RunComposeMosaic", map[string]any{"Uuid": "MOS", "Scale": 0.5}},
		{[]string{"mosaic", "tiles", "MOS"}, "Mosaic", "GetMosaicTiles", map[string]any{"MosaicUuid": "MOS"}},
		{[]string{"restore", "darks"}, "TaskController", "RestoreDarksToFactory", nil},
		{[]string{"restore", "flats"}, "TaskController", "RestoreFlatsToFactory", nil},
	} {
		if code, _, errs := ctl(t, s, c.args...); code != 0 {
			t.Errorf("%v: exit %d: %s", c.args, code, errs)

			continue
		}
		m := last(t, s, c.dest, c.cmd)
		for k, v := range c.want {
			if m[k] != v {
				t.Errorf("%v: %s = %v, want %v", c.args, k, m[k], v)
			}
		}
		if c.cmd == "RunInfinityAutoFocus" && m["StartingPosition"] != nil {
			t.Error("-from-origin sent a StartingPosition")
		}
	}
	for _, bad := range [][]string{{"center", "2", "0"}, {"set", "dew", "auto", "11"}, {"darks"}, {"filter", "Red"}, {"set", "nope", "1"}} {
		if code, _, _ := ctl(t, s, bad...); code != 1 {
			t.Errorf("%v accepted", bad)
		}
	}
}

// Logs and a final image are downloaded once the Origin offers them.
func TestDownloads(t *testing.T) {
	s := newFake(t)
	dir := t.TempDir()
	s.SetFile("logs/bundle.tar.gz", []byte("logs"))
	s.SetHandle(func(req fake.Msg) fake.Msg {
		r := s.DefaultHandle(req)
		switch {
		case fake.Is("System", "GetLogs")(req):
			go func() {
				time.Sleep(50 * time.Millisecond)
				s.Notify("System", "LogsReady", fake.Msg{"DownloadLink": "logs/bundle.tar.gz"})
			}()
		case fake.Is("ImageServer", "GetFinalStackedMaster")(req):
			s.SetFile("final.tiff", fake.TIFF16())
			go func() {
				time.Sleep(50 * time.Millisecond)
				s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER"})
			}()
		}

		return r
	})
	code, out, errs := ctl(t, s, "logs", "-o", dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "bundle.tar.gz")); code != 0 || string(b) != "logs" || !strings.Contains(out, "bundle.tar.gz") {
		t.Errorf("logs: exit %d %q %s", code, b, errs)
	}
	file := filepath.Join(dir, "final.tiff")
	if code, _, errs := ctl(t, s, "final", "-o", file, "run-1"); code != 0 {
		t.Fatalf("final: exit %d: %s", code, errs)
	}
	if b, _ := os.ReadFile(file); len(b) == 0 {
		t.Error("final image not saved")
	}
}

// image -duration sends TotalDuration in seconds.
func TestTimedImage(t *testing.T) {
	s := newFake(t)
	if code, _, errs := ctl(t, s, "image", "-duration", "30m"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if m := last(t, s, "TaskController", "RunImaging"); m["TotalDuration"] != 1800.0 {
		t.Errorf("TotalDuration %v", m["TotalDuration"])
	}
}

// watch reports how the connection closed, with the close code.
func TestWatchReportsClose(t *testing.T) {
	s := newFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"-host", s.Host(), "watch"}, &out, &errb) }()
	if _, ok := s.Wait(fake.Is("Mount", "GetStatus"), time.Second); !ok {
		t.Fatal("watch did not connect")
	}
	time.Sleep(50 * time.Millisecond)
	s.CloseAllWith(websocket.StatusInternalError, "Ping timeout")
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if !strings.Contains(errb.String(), "connection closed (code 1011)") {
		t.Errorf("stderr %q", errb.String())
	}
}
