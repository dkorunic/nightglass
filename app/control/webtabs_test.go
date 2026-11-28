// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

// The tabs of Celestron's web client the Android app has not: Wi-Fi,
// Optics, Schedules, its camera controls and Factory Reset.

func TestMergeWiFi(t *testing.T) {
	got := mergeWiFi([]WiFiNetwork{
		{SSID: "Home", SignalStrength: -70, AuthType: "COMPATIBLE", IsVisible: true},
		{SSID: "Away", HasConfiguredPassword: true}, // known, out of range
		{SSID: "Home", SignalStrength: -50, AuthType: "COMPATIBLE", IsConnected: true, HasConfiguredPassword: true},
		{SSID: "Cafe", SignalStrength: -60, AuthType: "OPEN", IsVisible: true},
	})
	if len(got) != 3 || got[0].SSID != "Home" || got[1].SSID != "Cafe" || got[2].SSID != "Away" {
		t.Fatalf("order %+v", got)
	}
	// A dual-band router: the strongest signal, any band's flags.
	if h := got[0]; h.SignalStrength != -50 || !h.IsConnected || !h.HasConfiguredPassword || !h.IsVisible {
		t.Errorf("merged %+v", h)
	}
}

func TestWiFi(t *testing.T) {
	a, _, s := connected(t)
	s.SetStatus("Network", fake.Msg{"Value": 4, "InternetStatus": 1, "IPAddress": "192.168.1.50"})
	s.SetInfo("Network/Get5GHzAccessPoint", fake.Msg{"5GHzAllowed": true, "5GHzRequested": true})
	s.SetInfo("Network/GetVisibleWiFi", fake.Msg{"Networks": []any{
		fake.Msg{"SSID": "Home", "SignalStrength": -50, "AuthType": "COMPATIBLE", "IsConnected": true, "HasConfiguredPassword": true, "IsVisible": true},
	}})
	st, err := a.WiFi()
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != 4 || !st.Internet || st.IPAddress != "192.168.1.50" || !st.Allowed5GHz || !st.Use5GHz || len(st.Networks) != 1 {
		t.Errorf("state %+v", st)
	}
	// A passphrase WPA2 refuses is not sent; a good one is stored, then joined.
	if a.JoinWiFi("Cafe", "short", true) == nil {
		t.Error("short passphrase taken")
	}
	if err := a.JoinWiFi("Cafe", "longenough", true); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Network", "AddWiFiNetwork"); m["Ssid"] != "Cafe" || m["Password"] != "longenough" {
		t.Errorf("add %v", m)
	}
	if m := lastSent(t, s, "Network", "ConnectToSpecificNetwork"); m["Ssid"] != "Cafe" {
		t.Errorf("connect %v", m)
	}
	if err := a.ForgetWiFi(""); err != nil {
		t.Fatal(err)
	}
	lastSent(t, s, "Network", "RemoveAllKnownNetworks")
	// Leaving Direct Connect joins the known network in range.
	if err := a.SetForceDirectConnect(false); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Network", "ConnectToSpecificNetwork"); m["Ssid"] != "Home" {
		t.Errorf("rejoin %v", m)
	}
	if err := a.SetWiFi5GHz(false); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Network", "5GHzAccessPoint"); m["Value"] != false {
		t.Errorf("5 GHz %v", m)
	}
}

func TestOptics(t *testing.T) {
	a, _, s := connected(t)
	// An Origin refuses Optics: the tab is not shown.
	if st, err := a.Optics(); err != nil || st.Available {
		t.Fatalf("origin optics %+v, %v", st, err)
	}
	s.SetAbsent("Optics", 0, "")
	s.SetInfo("Optics/GetSupportedModels", fake.Msg{"SupportedModels": []any{"SCT9F10", "ODD"}})
	s.SetInfo("Optics/GetConfiguration", fake.Msg{"ModelName": "SCT9F10", "Design": "SCT", "FocalLength": 2350.0, "Aperture": 235.0, "ReducerRatio": 1.0, "MirrorFlip": true})
	st, err := a.Optics()
	if err != nil || !st.Available || st.Config.FocalLength != 2350 || st.Config.MirrorFlip == nil || !*st.Config.MirrorFlip {
		t.Fatalf("optics %+v, %v", st, err)
	}
	if st.Models[0].Name != "9.25″ f/10 SCT" || st.Models[1].Name != "ODD" {
		t.Errorf("names %+v", st.Models)
	}
	if a.SetOptics(OpticsConfig{ModelName: "Custom", Design: "NEWT", FocalLength: 50, Aperture: 150, ReducerRatio: 1}) == nil {
		t.Error("focal length 50 mm taken")
	}
	// A known model alone first, then its reducer and the flip as reported.
	if err := a.SetOptics(OpticsConfig{ModelName: "SCT9F10", ReducerRatio: 0.7, MirrorFlip: st.Config.MirrorFlip}); err != nil {
		t.Fatal(err)
	}
	sets := received(s, "Optics", "SetConfiguration")
	if len(sets) != 2 || sets[0]["ModelName"] != "SCT9F10" || sets[0]["ReducerRatio"] != nil ||
		sets[1]["ReducerRatio"] != 0.7 || sets[1]["MirrorFlip"] != true {
		t.Errorf("sets %v", sets)
	}
}

func TestSchedule(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	if a.RunSchedule() == nil {
		t.Error("an empty schedule ran")
	}
	if a.SetSchedule([]ScheduleEntry{{Name: "M 42", RA: 83.8, Dec: -5.4}}) == nil {
		t.Error("a target without duration taken")
	}
	setSettings(t, a, func(st *Settings) {
		st.LatDeg, st.LonDeg, st.TimeZone, st.SchedulePowerDown = 45.8, 16, "Europe/Zagreb", true
	})
	entries := []ScheduleEntry{
		{Name: "M 42 Orion/Nebula", RA: 83.82, Dec: -5.39, DurationSec: 600, ExposureSec: 10, ISO: 1000},
		{Name: "Vega", RA: 279.23, Dec: 38.78, DurationSec: 300, ExposureSec: 5, ISO: 500},
	}
	if err := a.SetSchedule(entries); err != nil {
		t.Fatal(err)
	}
	// The draft is Go's: a frontend's older copy does not replace it.
	stale := a.Settings()
	stale.Schedule = nil
	if err := a.SaveSettings(stale); err != nil || len(a.Settings().Schedule) != 2 {
		t.Fatalf("draft after SaveSettings: %v, %d", err, len(a.Settings().Schedule))
	}
	if err := a.RunSchedule(); err != nil {
		t.Fatal(err)
	}
	m := lastSent(t, s, "TaskController", "RunObservingList")
	locs, _ := m["Location"].([]any)
	if len(locs) != 2 || m["PowerDownOnCompletion"] != true || m["AutoFocusAfterSlew"] != false || m["Uuid"] == "" {
		t.Fatalf("list %v", m)
	}
	l := locs[0].(map[string]any)
	if l["Name"] != origin.FolderName("M 42 Orion/Nebula") || l["TotalDuration"] != 600.0 || l["ISO"] != 1000.0 || l["SaveRawImage"] != true {
		t.Errorf("first target %v", l)
	}
	if _, ok := l["MinimumStartTime"].(string); !ok {
		t.Error("MinimumStartTime not sent") // always, or the Origin cannot list it again
	}
	// Lists come newest first, without the mosaics.
	a.update(func(st *Settings) { st.Mosaics = []MosaicRecord{{Uuid: "MOS"}} })
	s.SetInfo("ImageServer/GetAvailableImagingLists", fake.Msg{"AvailableObservingLists": []any{
		fake.Msg{"Uuid": "A", "DateTime": "2026-11-01 20:00:00", "Status": "FINISHED", "ObjectsTotal": 2, "ObjectsCompleted": 2},
		fake.Msg{"Uuid": "MOS", "DateTime": "2026-11-03 20:00:00", "Status": "FINISHED"},
		fake.Msg{"Uuid": "B", "DateTime": "2026-11-02 20:00:00", "Status": "FINISHED", "ObjectsTotal": 1},
	}})
	lists, err := a.Schedules()
	if err != nil || len(lists) != 2 || lists[0].Uuid != "B" || lists[1].ObjectsCompleted != 2 {
		t.Fatalf("lists %+v, %v", lists, err)
	}
	if err := a.RerunSchedule("A", false); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "ResumeImagingList"); m["Uuid"] != "A" || m["AllTargets"] != false {
		t.Errorf("rerun %v", m)
	}
	if err := a.DeleteSchedule("A", true); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "ImageServer", "DeleteImagingList"); m["ListUuid"] != "A" || m["RecursiveDelete"] != true {
		t.Errorf("delete %v", m)
	}
}

func TestVisibility(t *testing.T) {
	a, _ := newApp(t)
	setSettings(t, a, func(st *Settings) { st.LatDeg, st.LonDeg, st.TimeZone = 45, 16, "UTC" })
	if v := a.Visibility(0, 89); !v.Circumpolar {
		t.Errorf("dec 89 %+v", v)
	}
	if v := a.Visibility(0, -80); !v.NeverRises {
		t.Errorf("dec -80 %+v", v)
	}
}

func TestCameraControls(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	for _, bad := range []struct {
		name string
		v    int
	}{{"Binning", 9}, {"BitDepth", 12}, {"ColorRBalance", 256}, {"Gain", 1}} {
		if a.SetCameraParameter(bad.name, bad.v) == nil {
			t.Errorf("%s %d taken", bad.name, bad.v)
		}
	}
	if err := a.SetCameraParameter("ColorBBalance", 120); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Camera", "SetCaptureParameters"); m["ColorBBalance"] != 120.0 {
		t.Errorf("white balance %v", m)
	}
	if err := a.SetLiveStream(false); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "LiveStream", "SetDisableLiveStream"); m["Disable"] != true {
		t.Errorf("live stream %v", m)
	}
	if err := a.CaptureSingle(); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RunSampleCapture"); m["Calibrate"] != true || m["ExposureTime"] != nil {
		t.Errorf("single capture %v", m) // calibrated by default; automatic exposure
	}
	// Video: an exposure of 0.1 s or more is refused in manual live view.
	setSettings(t, a, func(st *Settings) { st.ManualImaging, st.Snapshot, st.SnapshotExposureSec = true, true, 0.5 })
	if a.CaptureVideo(VideoRequest{Container: "ser", BitDepth: 8}) == nil {
		t.Error("video at 0.5 s")
	}
	setSettings(t, a, func(st *Settings) { st.SnapshotExposureSec = 0.01 })
	v := VideoRequest{Container: "avi", BitDepth: 16, Frames: 500}
	v.Roi.X, v.Roi.Y, v.Roi.W, v.Roi.H = 10, 20, 300, 200
	if err := a.CaptureVideo(v); err != nil {
		t.Fatal(err)
	}
	m := lastSent(t, s, "TaskController", "CaptureVideo")
	if m["ObjectName"] != "Video" || m["Container"] != "avi" || m["Frames"] != 500.0 || m["RoiWidth"] != 300.0 || m["Iso"] == nil {
		t.Errorf("video %v", m)
	}
}

func TestFactoryReset(t *testing.T) {
	a, rec, s := connected(t)
	a.restartDelay = time.Hour
	if err := a.FactoryReset(); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "System", "RunFactoryReset"); m["Dev"] != false || m["Settings"] != false {
		t.Errorf("reset %v", m)
	}
	rec.wait(t, "activity", func(d any) bool { return strings.HasPrefix(d.(Activity).Text, "Reconnecting to Origin in") })
}

// The Final Image tab stretches the 16-bit master on the computer; an 8-bit
// final cannot be.
func TestFinalView(t *testing.T) {
	a, _ := newApp(t)
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "/final/view?s=0.5&b=0.1&d=2", nil))

		return w
	}
	if w := get(); w.Code != 404 {
		t.Errorf("no final: %d", w.Code)
	}
	img := image.NewGray16(image.Rect(0, 0, 80, 80))
	for i := range 80 * 80 {
		img.SetGray16(i%80, i/80, color.Gray16{Y: uint16(i * 7)})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.lastFinal = &origin.Frame{Original: b.Bytes()}
	a.mu.Unlock()
	w := get()
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("view: %d %s", w.Code, w.Body)
	}
	if _, err := png.Decode(w.Body); err != nil {
		t.Error(err)
	}
	a.mu.Lock()
	a.lastFinal = &origin.Frame{Original: fake.JPEG(0)}
	a.mu.Unlock()
	if w := get(); w.Code != 409 {
		t.Errorf("8-bit final: %d", w.Code)
	}
}

// The web client's Mount and Focuser controls: tracking, focuser
// calibration, and autofocus over the rough range when chosen.
func TestMountFocuserControls(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	if err := a.SetTracking(false); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Mount", "EnableTracking"); m["Value"] != false {
		t.Errorf("tracking %v", m)
	}
	if err := a.CalibrateFocuser(); err != nil {
		t.Fatal(err)
	}
	lastSent(t, s, "TaskController", "CalibrateFocuser")
	if err := a.AutoFocus(); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RunInfinityAutoFocus"); m["UseFineFocus"] != true {
		t.Errorf("fine by default: %v", m)
	}
	setSettings(t, a, func(st *Settings) { st.RoughFocus = true })
	if err := a.AutoFocus(); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RunInfinityAutoFocus"); m["UseFineFocus"] != false {
		t.Errorf("rough: %v", m)
	}
	// A maximum at or under the minimum is refused.
	if a.SaveSettings(Settings{LatDeg: 45, LonDeg: 16, SlewLimitMinDeg: 30, SlewLimitMaxDeg: 20}) == nil {
		t.Error("max under min taken")
	}
}
