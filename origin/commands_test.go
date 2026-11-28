// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// last is the last dest/cmd the fake received, waiting up to a second for
// one: posted commands arrive just after the call.
func last(t *testing.T, s *fake.Server, dest, cmd string) fake.Msg {
	t.Helper()
	var found fake.Msg
	for end := time.Now().Add(time.Second); found == nil && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		for _, m := range s.Received() {
			if fake.Is(dest, cmd)(m) {
				found = m
			}
		}
	}
	if found == nil {
		t.Fatalf("no %s/%s received", dest, cmd)
	}

	return found
}

func near(a any, b float64) bool { f, ok := a.(float64); return ok && math.Abs(f-b) < 1e-12 }

func TestInitializePayload(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	at := time.Date(2026, 1, 15, 21, 0, 0, 0, time.UTC)
	if err := c.Initialize(context.Background(), Site{LatDeg: 45.815, LonDeg: 15.982, Zone: "Europe/Zagreb"}, at); err != nil {
		t.Fatal(err)
	}
	m := last(t, s, "TaskController", "RunInitialize")
	if m["Date"] != "15 01 2026" || m["Time"] != "22:00:00" || m["TimeZone"] != "Europe/Zagreb" || m["FakeInitialize"] != false {
		t.Errorf("date/time/zone: %v", m)
	}
	if !near(m["Latitude"], 45.815*math.Pi/180) || !near(m["Longitude"], 15.982*math.Pi/180) {
		t.Errorf("lat/lon not radians: %v %v", m["Latitude"], m["Longitude"])
	}
}

// An unset location or zone fails before anything is sent.
func TestInitializeRefusesUnsetSite(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	if err := c.Initialize(context.Background(), Site{LatDeg: 0, LonDeg: 0, Zone: "UTC"}, time.Now()); !errors.Is(err, ErrNoLocation) {
		t.Errorf("0,0: err %v, want ErrNoLocation", err)
	}
	if err := c.Initialize(context.Background(), Site{LatDeg: 45, LonDeg: 15, Zone: "Not/AZone"}, time.Now()); err == nil {
		t.Error("bad zone: want error")
	}
	if err := c.Initialize(context.Background(), Site{LatDeg: 45, LonDeg: 15, Zone: ""}, time.Now()); err == nil {
		t.Error("empty zone: want error")
	}
	if _, ok := s.Wait(fake.Is("TaskController", "RunInitialize"), 50*time.Millisecond); ok {
		t.Error("RunInitialize sent for an invalid site")
	}
}

// The Origin takes J2000: the payload is the catalog position in radians,
// never precessed (spec §3.4).
func TestGotoSendsJ2000Radians(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	if err := c.GotoRaDec(context.Background(), 83.85, -5.45); err != nil {
		t.Fatal(err)
	}
	m := last(t, s, "Mount", "GotoRaDec")
	if !near(m["Ra"], 83.85*math.Pi/180) || !near(m["Dec"], -5.45*math.Pi/180) {
		t.Errorf("Ra/Dec %v %v, want exact J2000 radians", m["Ra"], m["Dec"])
	}
}

func TestSlewRates(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	if err := c.Slew(context.Background(), 4, -9); err != nil {
		t.Fatal(err)
	}
	m := last(t, s, "Mount", "Slew")
	if m["AltRate"] != 4.0 || m["AzmRate"] != -9.0 {
		t.Errorf("payload %v", m)
	}
	// The Origin's nine rates, as Celestron's web client's 1–9 slider.
	for r := 1; r <= 9; r++ {
		if err := c.Slew(context.Background(), r, -r); err != nil {
			t.Errorf("rate %d refused: %v", r, err)
		}
	}
	if err := c.Slew(context.Background(), 10, 0); err == nil {
		t.Error("rate 10 accepted")
	}
}

func TestStopSlewHaltsCentering(t *testing.T) {
	s := newFake(t)
	s.SetStatus("TaskController", fake.Msg{"State": "CENTERING_TARGET"})
	c := connect(t, s, nil)
	for c.TaskState() != "CENTERING_TARGET" {
		time.Sleep(5 * time.Millisecond) // the on-connect status query
	}
	if err := c.StopSlew(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Wait(fake.Is("Mount", "Slew"), time.Second) // posted, as the native library
	var order []string
	for _, m := range s.Received() {
		if cmd := m["Command"]; cmd == "HaltTasks" || cmd == "Slew" {
			order = append(order, cmd.(string))
		}
	}
	if len(order) != 2 || order[0] != "HaltTasks" || order[1] != "Slew" {
		t.Errorf("sent %v, want [HaltTasks Slew]", order)
	}
	if m := last(t, s, "Mount", "Slew"); m["AltRate"] != 0.0 || m["AzmRate"] != 0.0 {
		t.Errorf("stop payload %v", m)
	}
}

func TestFocusByClamps(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	if err := c.FocusBy(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	if m := last(t, s, "Focuser", "MoveTo"); m["Value"] != 30500.0 {
		t.Errorf("MoveTo %v, want 30500", m["Value"])
	}
	if err := c.FocusBy(context.Background(), 99999); err != nil {
		t.Fatal(err)
	}
	if m := last(t, s, "Focuser", "MoveTo"); m["Value"] != 60000.0 {
		t.Errorf("MoveTo %v, want clamped 60000", m["Value"])
	}
	if err := c.FocusTo(context.Background(), -1); err == nil {
		t.Error("FocusTo(-1) accepted")
	}
}

// RunImaging as the app builds it: a fresh Uuid (its only required field),
// no Ra/Dec (it images where the telescope points), exposure and ISO only in
// manual mode, twice the size in radians (ARCSEC_TO_RAD(size1) * 2), and
// stop-on-plate-solve-failure by default.
func TestRunImagingLikeApp(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	mag := 4.0
	im := Imaging{Name: "M 42", Mag: &mag, SizeArcmin: 85, StopOnPlateSolveFail: true}
	if err := c.RunImaging(context.Background(), im); err != nil {
		t.Fatal(err)
	}
	m := last(t, s, "TaskController", "RunImaging")
	uuid, _ := m["Uuid"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(uuid) {
		t.Errorf("Uuid %q, want a random UUID", uuid)
	}
	for _, k := range []string{"Ra", "Dec", "ExposureTime", "ISO", "DitherDistance", "DitherInterval"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s sent; the app leaves it out here", k)
		}
	}
	if m["Name"] != "M 42" || m["ObjectMagnitude"] != 4.0 || !near(m["ObjectSize"], 2*85.0/60*math.Pi/180) ||
		m["ContinueImagingIfStackingFails"] != false || m["SaveRawImage"] != false {
		t.Errorf("payload %v", m)
	}

	// The Origin makes the run's folder of Name: a catalogue name with path
	// characters (the Rosette's) is sent without them; spaces stay.
	if err := c.RunImaging(context.Background(), Imaging{Name: "NGC 2237/2238/2239/2246"}); err != nil {
		t.Fatal(err)
	}
	if m := last(t, s, "TaskController", "RunImaging"); m["Name"] != "NGC 2237_2238_2239_2246" {
		t.Errorf("Name %q", m["Name"])
	}

	im = Imaging{Name: "X", Manual: true, ExposureSec: 15, ISO: 500, SaveRaw: true, Dither: true, DitherDistance: 15, DitherInterval: 3}
	if err := c.RunImaging(context.Background(), im); err != nil {
		t.Fatal(err)
	}
	m = last(t, s, "TaskController", "RunImaging")
	if m["ExposureTime"] != 15.0 || m["ISO"] != 500.0 || m["SaveRawImage"] != true || m["ContinueImagingIfStackingFails"] != true ||
		m["DitherDistance"] != 15.0 || m["DitherInterval"] != 3.0 || m["Uuid"] == uuid {
		t.Errorf("manual payload %v", m)
	}
	for _, k := range []string{"ObjectMagnitude", "ObjectSize", "TargetTrack"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s sent though unknown", k)
		}
	}

	// A planet: a negative magnitude is sent, and its track in radians.
	mag = -2.5
	im = Imaging{Name: "Jupiter", Mag: &mag, Track: []TrackPoint{{90, 23, 2461000.5}, {90.001, 23, 2461000.50001}}}
	if err := c.RunImaging(context.Background(), im); err != nil {
		t.Fatal(err)
	}
	m = last(t, s, "TaskController", "RunImaging")
	tr, _ := m["TargetTrack"].([]any)
	p0, _ := tr[0].(map[string]any)
	if m["ObjectMagnitude"] != -2.5 || len(tr) != 2 || !near(p0["Ra"], math.Pi/2) || !near(p0["Dec"], 23*math.Pi/180) || p0["JulianDate"] != 2461000.5 {
		t.Errorf("planet payload %v", m)
	}
}

// ApplyCapture follows the app: manual live view only in manual + snapshot
// mode (SetEnableManual, then the parameters); otherwise SetEnableAuto;
// always followed by GetEnableManual.
func TestApplyCaptureLikeApp(t *testing.T) {
	cases := []struct {
		cap  Capture
		want []string
	}{
		{Capture{Manual: true, Snapshot: true, ExposureSec: 0.5, ISO: 500}, []string{"LiveStream/SetEnableManual", "Camera/SetCaptureParameters", "LiveStream/GetEnableManual"}},
		{Capture{Manual: true, ExposureSec: 10, ISO: 200}, []string{"LiveStream/SetEnableAuto", "LiveStream/GetEnableManual"}},
		{Capture{}, []string{"LiveStream/SetEnableAuto", "LiveStream/GetEnableManual"}},
	}
	for _, tc := range cases {
		s := newFake(t)
		c := connect(t, s, nil)
		// Let the on-connect queries (which include GetEnableManual) land first.
		if _, ok := s.Wait(fake.Is("LiveStream", "GetEnableManual"), time.Second); !ok {
			t.Fatal("no on-connect GetEnableManual")
		}
		time.Sleep(20 * time.Millisecond)
		before := len(s.Received())
		if err := c.ApplyCapture(context.Background(), tc.cap); err != nil {
			t.Fatal(err)
		}
		var sent []string
		for _, m := range s.Received()[before:] {
			if k := m["Destination"].(string) + "/" + m["Command"].(string); strings.HasPrefix(k, "LiveStream/") || k == "Camera/SetCaptureParameters" {
				sent = append(sent, k)
			}
		}
		if !slices.Equal(sent, tc.want) {
			t.Errorf("%+v sent %v, want %v", tc.cap, sent, tc.want)
		}
		if tc.cap.Snapshot {
			m := last(t, s, "Camera", "SetCaptureParameters")
			if m["Exposure"] != 0.5 || m["ISO"] != 500.0 {
				t.Errorf("capture parameters %v", m)
			}
		}
	}
}

func TestSupportedISOsAndExposureLimits(t *testing.T) {
	all := []int{100, 200, 500, 1000, 2000, 5000, 10000, 20000}
	if got := SupportedISOs(CameraInfo{}); !slices.Equal(got, all) {
		t.Errorf("no camera info: %v", got)
	}
	if got := SupportedISOs(CameraInfo{MinIso: 200, MaxIso: 5000}); !slices.Equal(got, []int{200, 500, 1000, 2000, 5000}) {
		t.Errorf("200..5000: %v", got)
	}
	ci := CameraInfo{DefaultIso: 200, MinIso: 100, MaxIso: 20000}
	if ClampISO(400, ci) != 200 || ClampISO(1000, ci) != 1000 || ClampISO(400, CameraInfo{}) != 200 {
		t.Error("ClampISO: unsupported ISO must become the camera default (200 without info)")
	}
	yes, no := true, false
	for _, c := range []struct {
		ci       CameraInfo
		wedge    *bool
		snapshot bool
		min, max float64
	}{
		{CameraInfo{}, nil, false, 1, 30},
		{CameraInfo{MinExposure: 0.001, MaxExposure: 60}, &no, false, 1, 30},
		{CameraInfo{MinExposure: 0.001, MaxExposure: 60}, &no, true, 0.001, 60},
		{CameraInfo{MinExposure: 0.001, MaxExposure: 60}, &yes, false, 1, 43200},
		{CameraInfo{MinExposure: 0.001, MaxExposure: 60}, nil, false, 1, 60},
	} {
		lo, hi := ExposureLimits(c.ci, c.wedge, c.snapshot)
		if lo != c.min || hi != c.max {
			t.Errorf("%+v wedge=%v snapshot=%v: %v..%v, want %v..%v", c.ci, c.wedge, c.snapshot, lo, hi, c.min, c.max)
		}
	}
}

// Like the native app (DVC_BUSY_ERROR), go-to and slew are refused while the
// telescope is imaging, and nothing is sent.
func TestMotionRefusedWhileImaging(t *testing.T) {
	for _, st := range []string{"CAPTURING_MOSAIC", "IMAGING_LIST", "IMAGING_OBJECT", "RECAPTURE_OBJECT", "RECAPTURE_MOSAIC_TILE"} {
		if !IsImagingState(st) {
			t.Errorf("%s is an imaging state", st)
		}
	}
	if IsImagingState("SAMPLE_CAPTURE") || IsImagingState("IDLE") {
		t.Error("SAMPLE_CAPTURE and IDLE are not imaging states")
	}
	s := newFake(t)
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT"})
	c := connect(t, s, nil)
	for c.TaskState() != "IMAGING_OBJECT" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.GotoRaDec(context.Background(), 1, 2); !errors.Is(err, ErrImaging) {
		t.Errorf("goto: %v, want ErrImaging", err)
	}
	if err := c.Slew(context.Background(), 4, 0); !errors.Is(err, ErrImaging) {
		t.Errorf("slew: %v, want ErrImaging", err)
	}
	for _, m := range s.Received() {
		if m["Destination"] == "Mount" && m["Command"] != "GetStatus" && m["Command"] != "GetMountConfig" {
			t.Errorf("sent %v while imaging", m)
		}
	}
	// A stop always goes out (spec §9.10).
	if err := c.StopSlew(context.Background()); err != nil {
		t.Errorf("stop while imaging: %v", err)
	}
	if m, ok := s.Wait(fake.Is("Mount", "Slew"), time.Second); !ok || m["AltRate"] != 0.0 || m["AzmRate"] != 0.0 {
		t.Errorf("stop not sent while imaging: %v", m)
	}
}

// Finishing is the app's finishImagingAndRequestFinalStackedMaster:
// HaltTasks, then GetFinalStackedMaster for the run's ImageUuid (from the
// task status, else the given one).
func TestFinishImagingLikeApp(t *testing.T) {
	s := newFake(t)
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": "run-1"}})
	c := connect(t, s, nil)
	for c.TaskState() != "IMAGING_OBJECT" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.FinishImaging(context.Background(), "fallback"); err != nil {
		t.Fatal(err)
	}
	s.Wait(fake.Is("ImageServer", "GetFinalStackedMaster"), time.Second) // posted, not waited for
	var order []string
	for _, m := range s.Received() {
		if k := m["Destination"].(string) + "/" + m["Command"].(string); k == "TaskController/HaltTasks" || k == "ImageServer/GetFinalStackedMaster" {
			order = append(order, k)
		}
	}
	if !slices.Equal(order, []string{"TaskController/HaltTasks", "ImageServer/GetFinalStackedMaster"}) {
		t.Errorf("sent %v", order)
	}
	if m := last(t, s, "ImageServer", "GetFinalStackedMaster"); m["ImageUuid"] != "run-1" {
		t.Errorf("ImageUuid %v, want the task status's run-1", m["ImageUuid"])
	}
	if err := c.GetFinalStackedMaster(context.Background(), "old-run"); err != nil {
		t.Fatal(err)
	}
	if m := last(t, s, "ImageServer", "GetFinalStackedMaster"); m["ImageUuid"] != "old-run" {
		t.Errorf("ImageUuid %v", m["ImageUuid"])
	}
}

// The app's runAutofocusButtonTapped: terrestrial autofocus in snapshot mode,
// else infinity autofocus from the current focus position.
func TestAutoFocusLikeApp(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	if err := c.AutoFocus(context.Background(), false, 31234); err != nil {
		t.Fatal(err)
	}
	// Fine focus, as Celestron's web client always asks for it.
	if m := last(t, s, "TaskController", "RunInfinityAutoFocus"); m["StartingPosition"] != 31234.0 || m["UseFineFocus"] != true {
		t.Errorf("payload %v", m)
	}
	if err := c.AutoFocus(context.Background(), true, 0); err != nil {
		t.Fatal(err)
	}
	last(t, s, "TaskController", "RunTerrestrialAutoFocus")
}

func TestAvailableStackedMastersAndFilter(t *testing.T) {
	s := newFake(t)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		r := s.DefaultHandle(req)
		if fake.Is("ImageServer", "GetAvailableStackedMasters")(req) {
			r["AvailableStackedMasters"] = fake.Msg{"a": "Images/a"}
		}

		return r
	})
	c := connect(t, s, nil)
	got, err := c.AvailableStackedMasters(context.Background(), []string{"a", "b"})
	if err != nil || !slices.Equal(got, []string{"a"}) {
		t.Errorf("available %v, %v", got, err)
	}
	if m := last(t, s, "ImageServer", "GetAvailableStackedMasters"); fmt.Sprint(m["UuidList"]) != "[a b]" {
		t.Errorf("UuidList %v", m["UuidList"])
	}
	if f, err := c.Filter(context.Background()); err != nil || f != "Clear" {
		t.Errorf("filter %q, %v", f, err)
	}
	if err := c.SetFilter(context.Background(), "Clear"); err != nil {
		t.Fatal(err)
	}
	if m := last(t, s, "Camera", "SetFilter"); m["Filter"] != "Clear" {
		t.Errorf("SetFilter payload %v", m)
	}
}

func TestFolderName(t *testing.T) {
	for in, want := range map[string]string{
		"M 42":                  "M 42",
		"NGC 1973/1975(NEBULA)": "NGC 1973_1975(NEBULA)",
		`a\b:c*d?e"f<g>h|i`:     "a_b_c_d_e_f_g_h_i",
		"tab\tnewline\n":        "tab_newline_",
		"..":                    "_",
		"":                      "",
	} {
		if got := FolderName(in); got != want {
			t.Errorf("FolderName(%q) = %q, want %q", in, got, want)
		}
	}
}
