// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"maps"
	"math"
	"testing"

	"github.com/dkorunic/nightglass/origin/fake"
)

func send(m *sim, dest, c string, extra fake.Msg) fake.Msg {
	req := fake.Msg{"Destination": dest, "Command": c}
	maps.Copy(req, extra)

	return m.handle(req)
}

// row is the GetVisibleWiFi row of ssid in the band listed first.
func row(m *sim, ssid string) fake.Msg {
	nets, _ := send(m, "Network", "GetVisibleWiFi", nil)["Networks"].([]any)
	for _, n := range nets {
		if n := n.(fake.Msg); n["SSID"] == ssid {
			return n
		}
	}

	return nil
}

func TestWiFiJoinAndForget(t *testing.T) {
	m, s := quickSim(t)
	status := func() any { return s.Status("Network")["Value"] }
	if status() != wifiClient || row(m, "HomeNet")["IsConnected"] != true {
		t.Fatalf("start: %v %v", s.Status("Network"), row(m, "HomeNet"))
	}
	if n := len(send(m, "Network", "GetVisibleWiFi", nil)["Networks"].([]any)); n != 6 {
		t.Errorf("%d rows, want 6 (HomeNet on two bands)", n)
	}
	if o := row(m, "Office"); o["IsVisible"] != false || o["SignalStrength"] != 0 || o["HasConfiguredPassword"] != true {
		t.Errorf("out of range network %v", o)
	}

	for _, c := range []struct {
		ssid, pass string
		code       int
	}{
		{"Garage", "short", -101},
		{"Neighbour-WPA3", "longenough", -98},
		{"CafeGuest", "", 0},
		{"Garage", "garage-pass", 0},
	} {
		if r := send(m, "Network", "AddWiFiNetwork", fake.Msg{"Ssid": c.ssid, "Password": c.pass}); r["ErrorCode"] != c.code {
			t.Errorf("add %s %q: %v, want %d", c.ssid, c.pass, r["ErrorCode"], c.code)
		}
	}
	if r := send(m, "Network", "ConnectToSpecificNetwork", fake.Msg{"Ssid": "Office"}); r["ErrorCode"] != -103 {
		t.Errorf("joining an out of range network: %v", r["ErrorCode"])
	}
	send(m, "Network", "ConnectToSpecificNetwork", fake.Msg{"Ssid": "Garage"})
	until(t, "the join", func() bool { return status() == wifiClient && row(m, "Garage")["IsConnected"] == true })
	if row(m, "HomeNet")["IsConnected"] != false {
		t.Error("HomeNet still connected")
	}

	send(m, "Network", "AddWiFiNetwork", fake.Msg{"Ssid": "CafeGuest", "Password": wrongPassword})
	send(m, "Network", "ConnectToSpecificNetwork", fake.Msg{"Ssid": "CafeGuest"})
	until(t, "the failed join", func() bool { return row(m, "CafeGuest")["HasWrongPassword"] == true })
	if status() != wifiDisconnected {
		t.Errorf("after a wrong password: %v", s.Status("Network"))
	}

	send(m, "Network", "DeleteWiFiNetwork", fake.Msg{"Ssid": "Garage"})
	if row(m, "Garage")["HasConfiguredPassword"] != false {
		t.Error("Garage not forgotten")
	}
	send(m, "Network", "ForceDirectConnect", fake.Msg{"Value": true})
	if st := s.Status("Network"); st["Value"] != wifiDirect || st["IPAddress"] != "10.0.0.1" {
		t.Errorf("direct connect %v", st)
	}
	if r := send(m, "Network", "SetDirectConnectPassword", fake.Msg{"Password": "1234567"}); r["ErrorCode"] != -101 {
		t.Errorf("short access point password: %v", r["ErrorCode"])
	}
	send(m, "Network", "RemoveAllKnownNetworks", nil)
	if r := send(m, "Network", "ConnectToSpecificNetwork", fake.Msg{"Ssid": "HomeNet"}); r["ErrorCode"] != -102 {
		t.Errorf("joining a forgotten network: %v", r["ErrorCode"])
	}
}

func TestObservingList(t *testing.T) {
	m, s := quickSim(t)
	ra, dec := m.mount.pointing() // near, so the slews are short
	const d = math.Pi / 180
	obj := func(uuid string, off float64, start string) map[string]any {
		return map[string]any{
			"Uuid": uuid, "Name": uuid, "Ra": (ra + off) * d, "Dec": dec * d, "TotalDuration": 20.0, "ExposureTime": 10.0,
			"ISO": 200.0, "MinimumStartTime": start,
		}
	}
	list := fake.Msg{"Uuid": "L1", "Location": []any{obj("A", 0.1, "01 01 2000 00:00:00"), obj("B", 0.2, "01 01 2999 00:00:00")}}
	if r := send(m, "TaskController", "RunObservingList", list); r["ErrorCode"] != 0 {
		t.Fatal(r)
	}
	if st := s.Status("TaskController"); st["State"] != "IMAGING_LIST" {
		t.Errorf("state %v", st["State"])
	}
	if r := send(m, "TaskController", "RunSampleCapture", nil); r["ErrorCode"] != -18 {
		t.Errorf("a snapshot during the list: %v", r["ErrorCode"])
	}
	lists := func() []any {
		l, _ := send(m, "ImageServer", "GetAvailableImagingLists", nil)["AvailableObservingLists"].([]any)

		return l
	}
	until(t, "the list to finish", func() bool { l := lists(); return len(l) == 1 && l[0].(fake.Msg)["Status"] == "FINISHED" })
	if l := lists()[0].(fake.Msg); l["ObjectsCompleted"] != 2 || l["ObjectsTotal"] != 2 {
		t.Errorf("finished list %v", l)
	}
	if st := s.Status("TaskController"); st["State"] != "IDLE" {
		t.Errorf("after the list: %v", st["State"])
	}
	info, _ := send(m, "TaskController", "GetObservingListInfo", fake.Msg{"ListUuid": "L1"})["ImagingInfo"].(fake.Msg)
	if objs, _ := info["ObjectInfoList"].([]any); len(objs) != 2 || objs[1].(fake.Msg)["StackingStatus"] != "COMPLETED" || objs[1].(fake.Msg)["StackDepth"] != 2 {
		t.Errorf("list info %v", info)
	}
	if r := send(m, "TaskController", "GetObservingListInfo", fake.Msg{"ListUuid": "nope"}); r["ErrorCode"] == 0 {
		t.Error("an unknown list answered")
	}

	// Resumed in full, then halted: the list and its object are STOPPED.
	send(m, "TaskController", "ResumeImagingList", fake.Msg{"Uuid": "L1", "AllTargets": true})
	send(m, "TaskController", "HaltTasks", nil)
	until(t, "the halt", func() bool { return lists()[0].(fake.Msg)["Status"] == "STOPPED" })
	if st := s.Status("TaskController"); st["State"] != "IDLE" {
		t.Errorf("after the halt: %v", st["State"])
	}

	send(m, "ImageServer", "DeleteImagingList", fake.Msg{"ListUuid": "L1", "RecursiveDelete": true})
	if l := lists(); len(l) != 0 {
		t.Errorf("deleted list still there: %v", l)
	}
}

func TestCaptureParameters(t *testing.T) {
	m, s := quickSim(t)
	for _, c := range []struct {
		p    fake.Msg
		code int
	}{
		{fake.Msg{"Binning": 3.0}, -3},
		{fake.Msg{"BitDepth": 12.0}, -3},
		{fake.Msg{"ColorRBalance": 256.0}, -3},
		{fake.Msg{"Exposure": 60.0}, -3},
		{fake.Msg{"ISO": 50.0}, -3},
		{fake.Msg{"Binning": 2.0, "BitDepth": 8.0, "ColorBBalance": 0.0, "ISO": 1000.0}, 0},
	} {
		if r := send(m, "Camera", "SetCaptureParameters", c.p); r["ErrorCode"] != c.code {
			t.Errorf("%v: %v, want %d", c.p, r["ErrorCode"], c.code)
		}
	}
	if st := s.Status("Camera"); st["Binning"] != 2.0 || st["BitDepth"] != 8.0 || st["ISO"] != 1000.0 {
		t.Errorf("camera status %v", st)
	}
	if ci := send(m, "Camera", "GetCameraInfo", nil); ci["MaxBinning"] != 2 || ci["MaxIso"] != 20000 {
		t.Errorf("camera info %v", ci)
	}
	if r := send(m, "TaskController", "RunSampleCapture", fake.Msg{"Binning": 2.0, "BitDepth": 16.0, "Calibrate": true}); r["ErrorCode"] != 0 {
		t.Errorf("snapshot with binning: %v", r)
	}
	send(m, "LiveStream", "SetDisableLiveStream", fake.Msg{"Disable": true})
	if !m.liveDisabled() || send(m, "LiveStream", "GetDisableLiveStream", nil)["IsDisabled"] != true {
		t.Error("live stream not disabled")
	}
}

func TestCaptureVideo(t *testing.T) {
	m, s := quickSim(t)
	state := func() any { return s.Status("TaskController")["State"] }
	if r := send(m, "TaskController", "CaptureVideo", fake.Msg{"ObjectName": "Moon", "Container": "mp4"}); r["ErrorCode"] != -3 {
		t.Errorf("mp4: %v", r["ErrorCode"])
	}
	video := fake.Msg{"ObjectName": "Moon", "Container": "ser", "BitDepth": 16.0, "Frames": 100.0}
	if r := send(m, "TaskController", "CaptureVideo", video); r["ErrorCode"] != 0 || state() != "VIDEO_CAPTURE" {
		t.Fatalf("capture: %v, state %v", r["ErrorCode"], state())
	}
	if r := send(m, "TaskController", "CaptureVideo", video); r["ErrorCode"] != -18 {
		t.Errorf("a second capture: %v", r["ErrorCode"])
	}
	until(t, "the video", func() bool { return state() == "IDLE" })
	dirs, _ := send(m, "ImageServer", "GetListOfAvailableDirectories", nil)["DirectoryList"].([]any)
	if len(dirs) != 1 {
		t.Errorf("directories %v", dirs)
	}

	// HaltTasks ends one early.
	send(m, "TaskController", "CaptureVideo", video)
	send(m, "TaskController", "HaltTasks", nil)
	until(t, "the halt", func() bool { return state() == "IDLE" })
}

func TestOpticsAndFactoryReset(t *testing.T) {
	m, s := quickSim(t)
	m.rebootTime = 0
	if r := send(m, "Optics", "GetSupportedModels", nil); r["ErrorCode"] == 0 {
		t.Error("Optics answered without -optics")
	}
	s.SetAbsent("Optics", 0, "")
	if r := send(m, "Optics", "GetConfiguration", fake.Msg{"ModelName": "SCT9F10"}); r["Aperture"] != 9.25*25.4 || r["FocalLength"] != 9.25*25.4*10 {
		t.Errorf("SCT 9.25 %v", r)
	}
	send(m, "Optics", "SetConfiguration", fake.Msg{"ModelName": "NEWT6F5"})
	send(m, "Optics", "SetConfiguration", fake.Msg{"MirrorFlip": true})
	if r := send(m, "Optics", "GetConfiguration", nil); r["Design"] != "NEWT" || r["MirrorFlip"] != true {
		t.Errorf("configured %v", r)
	}
	if r := send(m, "Optics", "SetConfiguration", fake.Msg{"ModelName": "Custom", "Design": "MAK"}); r["ErrorCode"] != -3 {
		t.Errorf("custom without figures: %v", r["ErrorCode"])
	}
	send(m, "Network", "RemoveAllKnownNetworks", nil)
	send(m, "System", "RunFactoryReset", fake.Msg{"Dev": false, "Settings": true})
	if r := send(m, "Optics", "GetConfiguration", nil); r["ModelName"] != "RASA6F2" {
		t.Errorf("optics after reset %v", r)
	}
	if row(m, "HomeNet")["IsConnected"] != true {
		t.Error("Wi-Fi not reset")
	}
	until(t, "the restart", func() bool { return !s.Down() && s.Status("TaskController")["IsReady"] == false })
}
