// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"math"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin/fake"
)

func testMount(t *testing.T) (*mount, *time.Time) {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	now := time.Date(2026, 10, 22, 21, 0, 0, 0, time.UTC)

	return newMount(s, 45.8, 16, func() time.Time { return now }), &now
}

func near(a, b, tol float64) bool { return math.Abs(wrap(a-b)) <= tol }

func TestMountSkyTurns(t *testing.T) {
	m, now := testMount(t)
	alt0, az0 := m.alt, m.az
	ra0, _ := m.pointing()
	// Not tracking: the mount stays put and the sky drifts west past it.
	*now = now.Add(time.Hour)
	m.step()
	ra1, _ := m.pointing()
	if m.alt != alt0 || m.az != az0 || !near(ra1-ra0, 15.04, 0.05) {
		t.Errorf("untracked hour: alt/az %v/%v → %v/%v, RA moved %.3f°, want 15.04°", alt0, az0, m.alt, m.az, wrap(ra1-ra0))
	}
	// Tracking: RA/Dec stay, Alt/Az follow the sky.
	m.setTracking(true)
	ra, dec := m.pointing()
	*now = now.Add(time.Hour)
	m.step()
	if r, d := m.pointing(); r != ra || d != dec {
		t.Errorf("tracked RA/Dec moved: %v/%v → %v/%v", ra, dec, r, d)
	}
	if alt, az := catalog.AltAz(ra, dec, *now, 45.8, 16); m.alt != alt || m.az != az {
		t.Errorf("tracked alt/az %v/%v, want %v/%v", m.alt, m.az, alt, az)
	}
}

func TestMountGoto(t *testing.T) {
	m, now := testMount(t)
	m.setTracking(true)
	ra0, dec0 := m.pointing()
	target := [2]float64{math.Mod(ra0+30, 360), dec0 - 10}
	m.gotoRaDec(target[0]*math.Pi/180, target[1]*math.Pi/180)
	*now = now.Add(time.Second)
	m.step()
	if m.target == nil {
		t.Fatal("arrived after 1 s")
	}
	if over, _ := m.s.Status("Mount")["IsGotoOver"].(bool); over {
		t.Error("IsGotoOver while slewing")
	}
	*now = now.Add(10 * time.Second)
	m.step()
	ra, dec := m.pointing()
	if m.target != nil || ra != target[0] || dec != target[1] {
		t.Errorf("at %v/%v, want %v", ra, dec, target)
	}
	st := m.s.Status("Mount")
	if over, _ := st["IsGotoOver"].(bool); !over || !near(st["Ra"].(float64)*180/math.Pi, target[0], 1e-9) {
		t.Errorf("status after the go-to %v", st)
	}
}

func TestMountSlew(t *testing.T) {
	m, now := testMount(t)
	m.setTracking(true)
	m.step() // the tracked position (precessed) first
	alt0 := m.alt
	ra0, dec0 := m.pointing()
	m.slew(9, 0, false) // up at 4°/s
	*now = now.Add(2 * time.Second)
	m.step()
	if !near(m.alt, alt0+8, 0.01) {
		t.Errorf("alt %v after 2 s up at rate 9, want %v", m.alt, alt0+8)
	}
	// The new pointing is what the Alt/Az now is (8° north of south at 45°).
	if ra, dec := m.pointing(); math.Hypot(wrap(ra-ra0)*math.Cos(dec*math.Pi/180), dec-dec0) < 7.9 {
		t.Errorf("RA/Dec moved %v/%v for an 8° slew", wrap(ra-ra0), dec-dec0)
	}
	m.slew(0, 0, false)
	alt := m.alt
	*now = now.Add(2 * time.Second)
	m.step()
	if !near(m.alt, alt, 0.01) { // tracking now: only the sky's turn
		t.Errorf("alt %v after stopping, want about %v", m.alt, alt)
	}
	// Down never goes below -10°.
	m.slew(-9, 0, false)
	*now = now.Add(time.Minute)
	m.step()
	if m.alt != -10 {
		t.Errorf("alt %v, want the -10° stop", m.alt)
	}
	m.slew(0, 0, true)
	if m.altRate != 0 {
		t.Error("AbortAxisMovement did not stop")
	}
}
