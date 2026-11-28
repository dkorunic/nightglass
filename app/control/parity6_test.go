// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

// The Moon and planets are a list of their own, placed for now, and a
// go-to aims where the body is when it is sent.
func TestSolarSystemTargets(t *testing.T) {
	a, _, s := connected(t)
	if c := a.Catalogs()[0]; c.Name != catalog.SolarSystem || c.Count != len(catalog.Bodies) {
		t.Errorf("first catalog %v", c)
	}
	rows := a.Targets(catalog.SolarSystem)
	if len(rows) != len(catalog.Bodies) || rows[0].Name != "Moon" || rows[0].Type != "Moon" || rows[1].Type != "Planet" {
		t.Fatalf("rows %v", rows)
	}
	if n := len(a.Positions(catalog.SolarSystem)); n != 2*len(rows) {
		t.Errorf("%d positions", n)
	}
	if all := a.Targets(""); all[0].Catalog != catalog.SolarSystem || len(a.Positions("")) != 2*len(all) {
		t.Error("all catalogs leave out the Moon and planets")
	}
	// A body above the horizon then, and one below.
	st := a.Settings()
	up, down := "", ""
	for _, b := range catalog.Bodies {
		ra, dec, _, _ := catalog.Body(b, evening, st.LatDeg, st.LonDeg)
		if alt, _ := catalog.AltAz(ra, dec, evening, st.LatDeg, st.LonDeg); alt > 5 && up == "" {
			up = b
		} else if alt < -5 && down == "" {
			down = b
		}
	}
	if up == "" || down == "" {
		t.Fatalf("up %q, down %q", up, down)
	}
	if err := a.GotoBody(down); err == nil {
		t.Errorf("go-to %s below the horizon accepted", down)
	}
	if err := a.GotoBody(up); err != nil {
		t.Fatal(err)
	}
	ra, dec, _, _ := catalog.Body(up, evening, st.LatDeg, st.LonDeg)
	m := lastSent(t, s, "Mount", "GotoRaDec")
	if math.Abs(m["Ra"].(float64)-ra*math.Pi/180) > 1e-9 || math.Abs(m["Dec"].(float64)-dec*math.Pi/180) > 1e-9 {
		t.Errorf("GotoRaDec %v, %s at %v %v", m, up, ra, dec)
	}
	if a.GotoBody("Sun") == nil {
		t.Error("go-to the Sun accepted")
	}
	// Unaligned, the Origin refuses moving-target tracking: none is sent.
	if len(received(s, "Mount", "TrackMovingTarget")) != 0 {
		t.Error("TrackMovingTarget sent to an unaligned mount")
	}
	// Aligned, the go-to is followed by an hour's track (361 places 10 s
	// apart from 30 s on, as Celestron's web app tracks slow targets), so
	// the Moon or a planet stays centred without imaging.
	s.SetStatus("Mount", fake.Msg{"IsAligned": true})
	s.NotifyStatus("Mount")
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		var ms origin.MountStatus
		if m, ok := c.Last("Mount/GetStatus"); ok && m.Decode(&ms) == nil && ms.IsAligned {
			break
		}
	}
	if err := a.GotoBody(up); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Wait(fake.Is("Mount", "TrackMovingTarget"), time.Second)
	track, _ := m["TargetTrack"].([]any)
	if !ok || len(track) != 361 {
		t.Fatalf("TrackMovingTarget %d places (%v)", len(track), ok)
	}
	first, last := track[0].(map[string]any), track[360].(map[string]any)
	jd0 := catalog.JulianDate(evening.Add(30 * time.Second))
	if math.Abs(first["JulianDate"].(float64)-jd0) > 1e-9 || math.Abs(last["JulianDate"].(float64)-jd0-3600.0/86400) > 1e-9 {
		t.Errorf("track from JD %v to %v, want %v + 1 h", first["JulianDate"], last["JulianDate"], jd0)
	}
	if math.Abs(first["Ra"].(float64)-m0Ra(up, st)) > 1e-3 {
		t.Errorf("track starts at RA %v", first["Ra"])
	}
}

// m0Ra is a body's RA (radians) 30 s after the test's evening.
func m0Ra(name string, st Settings) float64 {
	ra, _, _, _ := catalog.Body(name, evening.Add(30*time.Second), st.LatDeg, st.LonDeg)

	return ra * math.Pi / 180
}

// Power Off disconnects once the Origin answers, as the app's
// SettingsOriginAdvancedFragment does, instead of reconnecting forever.
func TestPowerOffDisconnects(t *testing.T) {
	a, rec, s := connected(t)
	if err := a.PowerOff(); err != nil {
		t.Fatal(err)
	}
	lastSent(t, s, "System", "PowerDownOrigin")
	rec.wait(t, "conn", func(d any) bool { return d.(Conn) == Conn{} })
	if _, err := a.cl(); err == nil {
		t.Error("still connected after Power Off")
	}
	if got := a.Activity().Text; got != "No Connection" {
		t.Errorf("status %q", got)
	}
}

// A transfer's progress belongs to the connection: after a disconnect the
// status line says so, not "Exporting Images".
func TestDisconnectForgetsTransfer(t *testing.T) {
	a, rec, s := connected(t)
	s.Notify("TaskController", "FileTransferStatus", fake.Msg{"PercentageComplete": 70})
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text == "Exporting Images: 70% Complete" })
	a.Disconnect()
	if got := a.Activity().Text; got != "No Connection" {
		t.Errorf("status %q", got)
	}
}

// Error codes as the app's onOriginMessageRelay meets them: those without a
// message of the app's own (including -5, whose reconnect is dead code)
// show nothing, and the end of an imaging list that powers the Origin down
// cancels and disconnects.
func TestErrorCodesLikeApp(t *testing.T) {
	a, rec, s := connected(t)
	for _, code := range []int{-2, -300, -301, -404} {
		s.Notify("Error", "Error", fake.Msg{"ErrorCode": code, "ErrorMessage": "x"})
	}
	s.Notify("Error", "Error", fake.Msg{"ErrorCode": -9})
	rec.wait(t, "alert", func(d any) bool {
		return d.(Alert).Title == "Origin Error" && strings.Contains(d.(Alert).Message, "Failed to communicate with mount")
	})
	if n := countEvents(rec, "alert"); n != 1 {
		t.Errorf("%d alerts, want only the -9 one", n)
	}

	// ILLEGAL_STATE_OPERATION has no message of the app's own, so its
	// reconnect branch is never reached: nothing happens.
	s.Notify("Error", "Error", fake.Msg{"ErrorCode": -5})
	time.Sleep(100 * time.Millisecond)
	if c, err := a.cl(); err != nil || !c.Connected() {
		t.Error("disconnected by ILLEGAL_STATE_OPERATION")
	}
	if n := countEvents(rec, "alert"); n != 1 {
		t.Errorf("%d alerts after -5, want still 1", n)
	}

	s.Notify("Error", "Error", fake.Msg{"ErrorCode": -69})
	rec.wait(t, "alert", func(d any) bool {
		al := d.(Alert)

		return al.Title == "" && al.Message == "Imaging list is completed and Origin is now shutting down."
	})
	lastSent(t, s, "TaskController", "CancelImaging")
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := a.cl(); err != nil {
			break
		}
		if time.Now().After(end) {
			t.Fatal("still connected after the Origin powers down")
		}
	}
}

// Origin logs: GetLogs while idle, the app's button texts while they are
// prepared and downloaded, and the LogsReady file saved into SaveDir.
func TestDownloadLogs(t *testing.T) {
	a, rec, s := connected(t)
	s.SetFile("logs/origin-logs.tar.gz", []byte("log bundle"))
	if err := a.DownloadLogs(); err != nil {
		t.Fatal(err)
	}
	lastSent(t, s, "System", "GetLogs")
	rec.wait(t, "logs", func(d any) bool { return d.(Logs).Text == "Requesting Logs…" })
	s.Notify("TaskController", "FileTransferStatus", fake.Msg{"PercentageComplete": 40})
	rec.wait(t, "logs", func(d any) bool { return d.(Logs).Text == "Preparing Logs: 40% Complete" })
	s.Notify("System", "LogsReady", fake.Msg{"DownloadLink": "logs/origin-logs.tar.gz"})
	rec.wait(t, "logs", func(d any) bool { return d.(Logs).Text == "Downloading Logs…" })
	want := filepath.Join(a.Settings().SaveDir, "origin-logs.tar.gz")
	rec.wait(t, "toast", func(d any) bool { return d.(Toast).Message == "Saved "+want })
	// The app's support email is offered with the Origin's serial number.
	rec.wait(t, "logsaved", func(d any) bool { return d.(LogsSaved).Path == want && d.(LogsSaved).Serial == "SIM-0001" })
	for _, bad := range []string{"", "../x", "a/b"} {
		if a.RevealSaved(bad) == nil {
			t.Errorf("RevealSaved(%q) accepted", bad)
		}
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "log bundle" {
		t.Errorf("saved %q, %v", b, err)
	}
	rec.wait(t, "logs", func(d any) bool { return d.(Logs).Text == "" })

	// A missing file is the app's download problem.
	if err := a.DownloadLogs(); err != nil {
		t.Fatal(err)
	}
	s.Notify("System", "LogsReady", fake.Msg{"DownloadLink": "logs/gone.tar.gz"})
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Title == "Problem with Download" })

	// Only while idle.
	setTask(t, a, s, fake.Msg{"State": "FOCUSING"})
	if a.DownloadLogs() == nil {
		t.Error("logs requested while busy")
	}
}

// Manual exposures out of range are limited as the app's exposure settings
// limit them, with its explanations.
func TestExposureLimitsExplained(t *testing.T) {
	a, rec, _ := connectedTo(t, func(s *fake.Server) {
		s.SetInfo("Camera/GetCameraInfo", fake.Msg{"MinExposure": 0.0005, "MaxExposure": 900.0})
		s.SetInfo("Mount/GetMountConfig", fake.Msg{"OnWedge": false})
	})
	c, _ := a.cl()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, okc := c.CameraInfo(); okc {
			if _, okm := c.MountConfig(); okm {
				break
			}
		}
	}
	st := a.Settings()
	st.ExposureSec, st.SnapshotExposureSec = 60, 0.0001
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Message == "Use optional wedge for exposures over 30.0s." })
	if got := a.Settings(); got.ExposureSec != 30 || got.SnapshotExposureSec != 0.0005 {
		t.Errorf("stored %v s and %v s, want 30 and 0.0005", got.ExposureSec, got.SnapshotExposureSec)
	}
	st.ExposureSec, st.SnapshotExposureSec = 0.5, 0.1
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Message == "Use SnapShot mode for exposures under 1.0s." })
	if got := a.Settings().ExposureSec; got != 1 {
		t.Errorf("stored %v s, want 1", got)
	}
	if n := countEvents(rec, "alert"); n != 2 {
		t.Errorf("%d alerts; the snapshot minimum is not explained", n)
	}
	if exposureText(0.0005) != "500.0μs" || exposureText(0.05) != "50.0ms" {
		t.Error("exposure units")
	}
}

// Names from the telescope stay inside the save folder on every OS.
func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"origin-logs.tar.gz": "origin-logs.tar.gz", `..\..\evil.exe`: "origin-logs", "C:evil": "C_evil",
		"..": "origin-logs", "": "origin-logs", "a b": "a_b",
	} {
		if got := safeName(in, "origin-logs"); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A typed address is checked as the app's doesOriginExistAtIPAddress does.
func TestCheckHost(t *testing.T) {
	a, _, s := connected(t)
	if err := a.CheckHost(s.Host()); err != nil {
		t.Errorf("fake: %v", err)
	}
	a.probeTimeout = 200 * time.Millisecond
	err := a.CheckHost("127.0.0.1:1")
	if err == nil || err.Error() != "Manual IP Address Invalid: Nightglass cannot find your Origin telescope at the IP address you have manually requested (127.0.0.1:1)." {
		t.Errorf("nothing there: %v", err)
	}
}

// "Attempt To Connect" ends a wait to reconnect at once.
func TestReconnectNow(t *testing.T) {
	a, rec, _ := connected(t)
	if a.ReconnectNow() == nil {
		t.Error("ReconnectNow without a wait")
	}
	a.retryDelay = time.Hour
	c, _ := a.cl()
	go a.reconnectAfter(c, a.retryDelay, false)
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text == "Origin Restarting…" })
	if err := a.ReconnectNow(); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
}

// A final image that cannot be saved gets the app's alert.
func TestFinalSaveFailureAlert(t *testing.T) {
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
	a.remember("run-1")
	s.SetFile("final.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "M 42", "Uuid": "run-1"})
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Title == "Could Not Save Image To Device!" })
	// Unsaved, the run is still offered on the next connect (only a saved
	// run is forgotten).
	if got := a.Settings().PendingImages; len(got) != 1 || got[0] != "run-1" {
		t.Errorf("pending after a failed save: %v", got)
	}
}

// The frontend saves its own copy of the settings, which may be older than
// what Go keeps: runs to recover, favorites, the launch count, the address
// connected to and the list shown survive it.
func TestSaveSettingsKeepsGoState(t *testing.T) {
	a, _ := newApp(t)
	a.Launched()
	old := a.Settings()
	a.update(func(s *Settings) { s.Host, s.Catalog = "10.0.0.5", "Caldwell" })
	a.remember("u1")
	if err := a.SetFavorite("M_42.png", true); err != nil {
		t.Fatal(err)
	}
	old.Night = true
	if err := a.SaveSettings(old); err != nil {
		t.Fatal(err)
	}
	got := a.Settings()
	if !got.Night || len(got.PendingImages) != 1 || len(got.Favorites) != 1 || got.LaunchCount != 1 || got.Host != "10.0.0.5" || got.Catalog != "Caldwell" {
		t.Errorf("after a stale save: %+v", got)
	}
	if !a.QuickStartDue() {
		t.Error("Quick Start not due on the first launch")
	}
	for range 3 {
		a.update(func(s *Settings) { s.LaunchCount++ })
	}
	if a.QuickStartDue() {
		t.Error("Quick Start due after three launches")
	}
}

// Nightglass's own log is kept (the newest MiB) and saved on request, as the
// app's "email app log".
func TestSaveAppLog(t *testing.T) {
	a, rec := newApp(t)
	l := &ringLog{max: 8}
	_, _ = l.Write([]byte("0123456789"))
	if string(l.bytes()) != "23456789" {
		t.Errorf("ring keeps %q, want the newest 8 bytes", l.bytes())
	}
	_, _ = AppLog.Write([]byte("hello from slog\n"))
	p, err := a.SaveAppLog()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !bytes.Contains(b, []byte("hello from slog")) || filepath.Dir(p) != a.Settings().SaveDir {
		t.Errorf("saved %s: %q", p, b)
	}
	rec.wait(t, "toast", func(d any) bool { return d.(Toast).Message == "Saved "+p })
}
