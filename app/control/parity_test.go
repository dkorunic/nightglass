// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

func runImagings(s *fake.Server) []fake.Msg {
	var out []fake.Msg
	for _, m := range s.Received() {
		if fake.Is("TaskController", "RunImaging")(m) {
			out = append(out, m)
		}
	}

	return out
}

// setMountAlt makes the fake mount report altitude deg and waits until the
// app's client has it.
func setMountAlt(t *testing.T, a *App, s *fake.Server, deg float64) {
	t.Helper()
	s.SetStatus("Mount", fake.Msg{"Alt": deg * math.Pi / 180})
	s.NotifyStatus("Mount")
	c, err := a.cl()
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		var ms origin.MountStatus
		if m, ok := c.Last("Mount/GetStatus"); ok && m.Decode(&ms) == nil && math.Abs(ms.Alt-deg*math.Pi/180) < 1e-9 {
			return
		}
	}
	t.Fatal("mount altitude not cached")
}

// pointAt makes the fake mount point at a J2000 position (degrees) at
// altitude alt, and waits until the app's client has it.
func pointAt(t *testing.T, a *App, s *fake.Server, raDeg, decDeg, alt float64) {
	t.Helper()
	r := math.Pi / 180
	s.SetStatus("Mount", fake.Msg{"Ra": raDeg * r, "Dec": decDeg * r, "Alt": alt * r, "IsAligned": true})
	s.NotifyStatus("Mount")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		var ms origin.MountStatus
		if m, ok := c.Last("Mount/GetStatus"); ok && m.Decode(&ms) == nil && ms.Ra == raDeg*r && ms.Dec == decDeg*r && ms.Alt == alt*r && ms.IsAligned {
			return
		}
	}
	t.Fatal("mount position not cached")
}

// messier is a Messier list entry.
func messier(t *testing.T, a *App, name string) catalog.Target {
	t.Helper()
	for _, x := range a.cat.Get("Messier") {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("%s not in Messier", name)

	return catalog.Target{}
}

// An unaligned mount's coordinates name nothing (native ReadCelestronOrigin
// reads the position only while aligned).
func TestImageUnalignedUnnamed(t *testing.T) {
	a, _, s := connected(t)
	m31 := messier(t, a, "M 31")
	pointAt(t, a, s, m31.RADeg, m31.DecDeg, 30)
	s.SetStatus("Mount", fake.Msg{"IsAligned": false})
	s.NotifyStatus("Mount")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if m, ok := c.Last("Mount/GetStatus"); ok && m.Fields()["IsAligned"] == false {
			break
		}
	}
	if err := a.Image(ImageRequest{Catalog: "Messier", Name: "M 31"}); err != nil {
		t.Fatal(err)
	}
	if m := runImagings(s)[0]; m["Name"] != "" {
		t.Errorf("unaligned run named %v", m["Name"])
	}
}

// The run is named as the app's setupImagingTargetObject: the selection
// only when the telescope points within about 1° of it, else what is
// there (a showpiece before any other object), else nothing.
func TestImageNamesWhatTheTelescopePointsAt(t *testing.T) {
	a, _, s := connected(t)
	list, m31 := "Messier", messier(t, a, "M 31")
	// M 42 selected, the telescope on M 31: the run is M 31's.
	pointAt(t, a, s, m31.RADeg+0.2, m31.DecDeg, 30)
	if err := a.Image(ImageRequest{Catalog: list, Name: "M 42"}); err != nil {
		t.Fatal(err)
	}
	m := runImagings(s)[0]
	if m["Name"] != "M 31" || !near(m["ObjectSize"], 2*m31.Size/60*math.Pi/180) {
		t.Errorf("pointing at M 31: %v", m)
	}
	// Nothing selected: the same.
	reportForced(t, a, s)
	if err := a.Image(ImageRequest{}); err != nil {
		t.Fatal(err)
	}
	if m = runImagings(s)[1]; m["Name"] != "M 31" {
		t.Errorf("no selection: %v", m["Name"])
	}
	// Nothing within 1°: unnamed, with no magnitude or size.
	empty := [2]float64{-1, 0}
	for ra := 0.0; ra < 360 && empty[0] < 0; ra += 2 {
		for dec := -60.0; dec <= 60; dec += 2 {
			if _, ok := a.imagingTarget(ImageRequest{}, ra, dec, evening); !ok {
				empty = [2]float64{ra, dec}

				break
			}
		}
	}
	if empty[0] < 0 {
		t.Fatal("no empty field found")
	}
	reportForced(t, a, s)
	pointAt(t, a, s, empty[0], empty[1], 30)
	if err := a.Image(ImageRequest{Catalog: list, Name: "M 42"}); err != nil {
		t.Fatal(err)
	}
	m = runImagings(s)[2]
	_, mag := m["ObjectMagnitude"]
	_, size := m["ObjectSize"]
	if m["Name"] != "" || mag || size {
		t.Errorf("empty sky: %v", m)
	}
}

func near(v any, want float64) bool {
	f, ok := v.(float64)

	return ok && math.Abs(f-want) < 1e-9
}

// A planet run carries the app's TargetTrack: 16 places one second apart
// from 30 s on, and the planet's (possibly negative) magnitude.
func TestImagePlanetTrack(t *testing.T) {
	a, _, s := connected(t)
	st := a.Settings()
	ra, dec, mag, _ := catalog.Body("Jupiter", evening, st.LatDeg, st.LonDeg)
	pointAt(t, a, s, ra, dec, 30)
	if err := a.Image(ImageRequest{Catalog: catalog.SolarSystem, Name: "Jupiter"}); err != nil {
		t.Fatal(err)
	}
	m := runImagings(s)[0]
	tr, _ := m["TargetTrack"].([]any)
	if m["Name"] != "Jupiter" || m["ObjectMagnitude"] != mag || len(tr) != 16 {
		t.Fatalf("planet run %v", m)
	}
	if _, ok := m["ObjectSize"]; ok {
		t.Error("ObjectSize sent for a planet; the app sends it for deep-sky objects only")
	}
	jd0 := catalog.JulianDate(evening.Add(30 * time.Second))
	for i, p := range tr {
		pt := p.(map[string]any)
		if jd := pt["JulianDate"].(float64); math.Abs(jd-(jd0+float64(i)/86400)) > 1e-6 {
			t.Errorf("point %d at JD %v, want %v", i, jd, jd0+float64(i)/86400)
		}
	}
	p0 := tr[0].(map[string]any)
	if separation(ra, dec, p0["Ra"].(float64)*180/math.Pi, p0["Dec"].(float64)*180/math.Pi) > 1e-4 {
		t.Errorf("track starts at %v, Jupiter is at %v %v", p0, ra, dec)
	}
}

// Image works like the app's startImagingObject: it images where the
// telescope points, refuses below 0°, warns near the zenith off a wedge, and
// lets the telescope choose exposure unless manual imaging is on.
func TestImageLikeApp(t *testing.T) {
	a, rec, s := connected(t)
	list, m42 := "Messier", messier(t, a, "M 42")
	pointAt(t, a, s, m42.RADeg, m42.DecDeg, 30)
	if err := a.Image(ImageRequest{Catalog: list, Name: "M 42"}); err != nil {
		t.Fatal(err)
	}
	m := runImagings(s)[0]
	if _, ok := m["Ra"]; ok {
		t.Error("Ra sent: the app images the current position")
	}
	if _, ok := m["ExposureTime"]; ok {
		t.Error("ExposureTime sent in automatic mode")
	}
	if m["Name"] != "M 42" || m["Uuid"] == nil || m["ObjectMagnitude"] != m42.Mag || m["ContinueImagingIfStackingFails"] != false {
		t.Errorf("payload %v", m)
	}
	if _, ok := m["TargetTrack"]; ok {
		t.Error("TargetTrack sent for a deep-sky object")
	}

	reportForced(t, a, s)
	setMountAlt(t, a, s, -2)
	if err := a.Image(ImageRequest{Name: "Low"}); err == nil || !strings.Contains(err.Error(), "between 0° and 85°") {
		t.Errorf("below the horizon: %v", err)
	}
	if n := len(runImagings(s)); n != 1 {
		t.Errorf("%d RunImaging sent, want 1", n)
	}

	setMountAlt(t, a, s, 88)
	if err := a.Image(ImageRequest{Name: "High"}); err != nil {
		t.Fatalf("near the zenith the app warns, not refuses: %v", err)
	}
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Title == "Target Close to Zenith" })
	if n := len(runImagings(s)); n != 2 {
		t.Errorf("%d RunImaging sent, want 2", n)
	}

	st := a.Settings()
	st.ManualImaging, st.ExposureSec, st.ISO = true, 60, 400
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	reportForced(t, a, s)
	setMountAlt(t, a, s, 30)
	if err := a.Image(ImageRequest{Name: "Manual"}); err != nil {
		t.Fatal(err)
	}
	m = runImagings(s)[2]
	if m["ExposureTime"] != 30.0 || m["ISO"] != 200.0 {
		t.Errorf("manual: ExposureTime %v ISO %v, want 30 (off-wedge limit) and 200 (400 unsupported)", m["ExposureTime"], m["ISO"])
	}
}

// Go-to refuses only below the minimum slew altitude (default 0°); there is
// no upper limit.
func TestGotoSlewLimit(t *testing.T) {
	a, _, s := connected(t)
	st := a.Settings()
	st.SlewLimitMinDeg = 40
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := a.Goto(83.85, -5.45); err == nil || !strings.Contains(err.Error(), "slew limits") {
		t.Errorf("M 42 at 38.7° with a 40° limit: %v", err)
	}
	st.SlewLimitMinDeg = 0
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	ra := -1.0
	for r := 0.0; r < 360; r++ {
		if alt, _ := catalog.AltAz(r, 45.815, evening, 45.815, 15.982); alt > 86 {
			ra = r

			break
		}
	}
	if err := a.Goto(ra, 45.815); err != nil {
		t.Errorf("near-zenith go-to refused: %v", err)
	}
	if _, ok := s.Wait(fake.Is("Mount", "GotoRaDec"), time.Second); !ok {
		t.Error("no GotoRaDec sent")
	}
}

func TestSlewRefusedWhileImaging(t *testing.T) {
	a, _, s := connected(t)
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT"})
	s.NotifyStatus("TaskController")
	c, _ := a.cl()
	for c.TaskState() != "IMAGING_OBJECT" {
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.Slew(4, 0); !errors.Is(err, errShown) { // silent, as native
		t.Errorf("slew while imaging: %v", err)
	}
	a.mu.Lock()
	owed := a.slewing
	a.mu.Unlock()
	if owed {
		t.Error("a refused slew is not owed a stop")
	}
}

func TestWarningNotificationsLikeApp(t *testing.T) {
	_, rec, s := connected(t)
	s.Notify("Disk", "LowSpaceWarning", fake.Msg{"FreeBytes": 1.5e9, "Capacity": 64e9})
	rec.wait(t, "alert", func(d any) bool {
		al := d.(Alert)

		return al.Title == "Very low disk space on your Origin device!" && strings.HasSuffix(al.Message, "\n\n1.50 GB remaining.")
	})
	s.Notify("System", "CriticalError", fake.Msg{"CriticalErrorMessage": "camera fault"})
	rec.wait(t, "critical", func(d any) bool { return d.(Critical).Message == "camera fault" })
	s.Notify("Error", "Error", fake.Msg{"ErrorCode": -9})
	rec.wait(t, "alert", func(d any) bool {
		return d.(Alert).Title == "Origin Error" && strings.Contains(d.(Alert).Message, "Failed to communicate with mount")
	})
}

// On connect the capture mode is applied like the app's
// updateScopeWithManualCaptureParamsIfNecessary, with an unsupported ISO
// replaced by the camera default.
func TestCaptureAppliedOnConnect(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	st := a.Settings()
	st.ManualImaging, st.Snapshot, st.SnapshotExposureSec, st.ISO = true, true, 0.5, 400
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
	m, ok := s.Wait(fake.Is("Camera", "SetCaptureParameters"), 3*time.Second)
	if !ok || m["Exposure"] != 0.5 || m["ISO"] != 200.0 {
		t.Errorf("capture parameters %v", m)
	}
	if _, ok := s.Wait(fake.Is("LiveStream", "SetEnableManual"), time.Second); !ok {
		t.Error("no SetEnableManual for manual snapshot mode")
	}
}

func TestNewSettingsDefaultsAndValidation(t *testing.T) {
	// The defaults of Celestron's web client (firmware 1.5.6286): automatic
	// exposure, 10 s and ISO 1000 when manual, raw images kept on the Origin,
	// stop when stacking fails, no dithering (every 5 frames, 10 pixels when
	// on), the fastest slew, a cooled camera's cooler on at +10 °C.
	d := DefaultSettings()
	if d.ManualImaging || d.Snapshot || d.ExposureSec != 10 || d.ISO != 1000 || d.SnapshotExposureSec != 10 ||
		d.SlewLimitMinDeg != 0 || !d.SaveRaw || !d.StopOnPlateSolveFail || d.Dither || d.DitherDistance != 10 ||
		d.DitherInterval != 5 || d.SlewRate != 4 || !d.Cooling || d.CoolingTargetC != 10 {
		t.Errorf("defaults %+v differ from the web client's", d)
	}
	for _, s := range []Settings{{SlewLimitMinDeg: -1}, {SlewLimitMinDeg: 90}, {SnapshotExposureSec: -1}, {DitherDistance: -1}} {
		if s.Validate() == nil {
			t.Errorf("%+v accepted", s)
		}
	}
}
