// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"testing"
	"time"
)

// Meeus, Astronomical Algorithms (2nd ed.): geocentric apparent places of
// date. Precessing our J2000 result to the date leaves nutation and
// aberration (<40") plus the method's error.
func TestGeocentricAgainstMeeus(t *testing.T) {
	for _, c := range []struct {
		body    string
		t       time.Time
		ra, dec float64 // degrees, of date
		tol     float64
	}{
		// Example 47.a, 1992 April 12 0h TD.
		{"Moon", time.Date(1992, 4, 12, 0, 0, 0, 0, time.UTC), 134.688470, 13.768368, 0.05},
		// Example 33.a, 1992 December 20 0h TD.
		{"Venus", time.Date(1992, 12, 20, 0, 0, 0, 0, time.UTC), 316.172725, -18.888011, 0.01},
	} {
		d := daysJ2000(c.t) + 1.5
		lon, lat, _, _ := geocentric(c.body, d)
		ra, dec := radec(toJ2000(lon, lat, d))
		ra, dec = precess(ra, dec, daysJ2000(c.t)/36525)
		if s := separation(ra, dec, c.ra, c.dec); s > c.tol {
			t.Errorf("%s: %.4f, %.4f is %.3f° from Meeus %.4f, %.4f", c.body, ra, dec, s, c.ra, c.dec)
		}
	}
}

// The Moon's parallax: none at the sub-lunar point, about 0.95° on the
// horizon, where it lowers the Moon towards the horizon.
func TestMoonParallax(t *testing.T) {
	at := time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
	d := daysJ2000(at) + 1.5
	lon, lat, dist, _ := geocentric("Moon", d)
	gra, gdec := radec(toJ2000(lon, lat, d))
	if dist < 55 || dist > 64 {
		t.Fatalf("Moon distance %.1f Earth radii", dist)
	}
	// Sub-lunar point: latitude = Dec, east longitude = RA - Greenwich
	// sidereal time (the of-date/J2000 difference is negligible here).
	sub := math.Mod(gra-lst(at, 0)+720, 360)
	ra, dec, _, _ := Body("Moon", at, gdec, sub)
	if s := separation(ra, dec, gra, gdec); s > 0.01 {
		t.Errorf("sub-lunar parallax %.3f°, want 0", s)
	}
	// 90° of longitude away the Moon is on the horizon.
	ra, dec, _, _ = Body("Moon", at, 0, sub+90)
	if s := separation(ra, dec, gra, gdec); s < 0.85 || s > 1.05 {
		t.Errorf("horizon parallax %.3f°, want about 0.95", s)
	}
	if alt, _ := AltAz(ra, dec, at, 0, sub+90); alt > 0 {
		t.Errorf("parallax should lower the Moon below the geocentric horizon, alt %.2f", alt)
	}
}

func TestBody(t *testing.T) {
	at := time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
	for _, b := range Bodies {
		ra, dec, mag, ok := Body(b, at, 45.8, 16)
		if !ok || ra < 0 || ra >= 360 || math.Abs(dec) > 30 || math.IsNaN(mag) || mag < -13 || mag > 9 {
			t.Errorf("%s: %v %.3f %.3f mag %.2f", b, ok, ra, dec, mag)
		}
	}
	if _, _, _, ok := Body("Sun", at, 0, 0); ok {
		t.Error("the Sun is not a target")
	}
	if jd := JulianDate(time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)); jd != 2451545.0 {
		t.Errorf("JD %v", jd)
	}
}
