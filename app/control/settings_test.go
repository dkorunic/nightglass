// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettingsMissingCorruptAndInvalid(t *testing.T) {
	dir := t.TempDir()
	if s, err := LoadSettings(filepath.Join(dir, "none.json")); err != nil || s.ExposureSec != 10 || s.ISO != 1000 || s.HasLocation() {
		t.Errorf("missing file: %+v %v", s, err)
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o644)
	if s, err := LoadSettings(bad); err == nil || s.ISO != 1000 {
		t.Errorf("corrupt file: %+v %v, want defaults and an error", s, err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	_ = os.WriteFile(invalid, []byte(`{"LatDeg": 123}`), 0o644)
	if s, err := LoadSettings(invalid); err == nil || s.LatDeg != 0 {
		t.Errorf("invalid file: %+v %v, want defaults and an error", s, err)
	}
	// One bad value costs only itself: the rest is kept, the original backed up.
	mixed := filepath.Join(dir, "mixed.json")
	orig := []byte(`{"LatDeg": 45.8, "LonDeg": 16, "SlewSpeed": 12, "ISO": "loud", "PendingImages": ["run"], "LaunchCount": 7, "EverConnected": true}`)
	_ = os.WriteFile(mixed, orig, 0o644)
	s, err := LoadSettings(mixed)
	if err == nil || s.LatDeg != 45.8 || s.SlewSpeed != 9 || s.ISO != 1000 || len(s.PendingImages) != 1 || s.LaunchCount != 7 || !s.EverConnected {
		t.Errorf("one bad value: %+v %v, want only it reset", s, err)
	}
	if b, _ := os.ReadFile(mixed + ".bak"); string(b) != string(orig) {
		t.Errorf("backup %q", b)
	}
	if s := DefaultSettings(); s.SlewSpeed != 9 { // the web client's rate slider starts at 9
		t.Errorf("default slew speed %d, want 9", s.SlewSpeed)
	}
	for _, s := range []Settings{{LatDeg: 91}, {LonDeg: -181}, {TimeZone: "Not/AZone"}, {ISO: -1}, {SlewSpeed: 10}, {SlewSpeed: -1}} {
		if s.Validate() == nil {
			t.Errorf("%+v accepted", s)
		}
	}
}

func TestWriteSettingsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "settings.json")
	want := DefaultSettings()
	want.LatDeg, want.LonDeg, want.TimeZone, want.Host = 45.815, 15.982, "Europe/Zagreb", "10.0.0.5"
	if err := writeSettings(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(p)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("round trip: %+v %v, want %+v", got, err, want)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("temporary file left behind")
	}
}
