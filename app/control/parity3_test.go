// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// N: an uncalibrated Origin is reported once per app run, however often it
// says so and across reconnects.
func TestFactoryCalibrationAlertOnce(t *testing.T) {
	a, rec, s := connected(t)
	s.Notify("FactoryCalibrationController", "GetStatus", fake.Msg{"CurrentPhase": "COMPLETE"}) // IsCalibrated unset: no alert
	s.Notify("FactoryCalibrationController", "GetStatus", fake.Msg{"IsCalibrated": false})
	al := rec.wait(t, "alert", func(any) bool { return true }).(Alert)
	if al.Title != "Factory Calibration Incomplete" || al.Message != "Factory Calibration Incomplete" {
		t.Errorf("alert %+v", al)
	}
	s.Notify("FactoryCalibrationController", "GetStatus", fake.Msg{"IsCalibrated": false})
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
	s.Notify("FactoryCalibrationController", "GetStatus", fake.Msg{"IsCalibrated": false})
	time.Sleep(100 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := 0
	for _, e := range rec.got {
		if e.name == "alert" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d alerts, want 1", n)
	}
}

// N: on connect the Origin is asked for its factory calibration and told
// the regulatory domain of the observing site, as the native
// CelestronOriginHandleWebSocketOpened does.
func TestConnectSetsRegulatoryDomain(t *testing.T) {
	_, _, s := connected(t)
	m, ok := s.Wait(fake.Is("Network", "SetRegulatoryDomain"), 2*time.Second)
	if !ok || m["CountryCode"] != "HR" {
		t.Fatalf("SetRegulatoryDomain %v", m)
	}
	if _, forced := m["Force"]; forced {
		t.Error("Force sent; the app leaves it unset")
	}
	if _, ok := s.Wait(fake.Is("FactoryCalibrationController", "GetStatus"), time.Second); !ok {
		t.Error("no FactoryCalibrationController/GetStatus on connect")
	}
}

// M: a final image is saved as it arrives, as a PNG carrying the app's
// metadata.
func TestFinalSavedAsPNGWithMetadata(t *testing.T) {
	a, rec, s := connected(t)
	s.SetFile("final.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{
		"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "M 42",
		"Uuid": "run-1", "ExposureTime": 10.0, "ISO": 200, "StackDepth": 30, "Date": "15 01 2026", "Time": "21:30:00",
	})
	toast := rec.wait(t, "toast", func(d any) bool { return strings.HasPrefix(d.(Toast).Message, "Saved ") }).(Toast)
	p := strings.TrimPrefix(toast.Message, "Saved ")
	// Named at the time it was taken (the Origin's clock), as the app.
	if filepath.Dir(p) != a.Settings().SaveDir || filepath.Base(p) != "Orion_Nebula-20260115-213000.png" {
		t.Errorf("saved as %s", p)
	}
	// What it was of and when are remembered (SkyObjectImage).
	if rec := a.Settings().Images[filepath.Base(p)]; rec.Title != "Orion Nebula" || rec.Taken != "2026-01-15 21:30:00" || rec.StackDepth != 30 {
		t.Errorf("record %+v", rec)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	for _, want := range []string{"eXIf", "run-1", "<xapMM:DocumentID>run-1</xapMM:DocumentID>", "2026:01:15 21:30:00"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("saved PNG lacks %q", want)
		}
	}
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Image == "final" })
}
