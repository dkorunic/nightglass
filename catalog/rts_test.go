// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Zagreb's DST day, wherever the test runs
)

func TestRiseTransitSet(t *testing.T) {
	zagreb, lat, lon := time.FixedZone("CET", 3600), 45.815, 15.982
	day := time.Date(2026, 1, 15, 15, 30, 0, 0, zagreb)
	start := time.Date(2026, 1, 15, 0, 0, 0, 0, zagreb)
	near := func(name string, got, want time.Time) {
		t.Helper()
		if want.IsZero() || got.IsZero() || got.Sub(want).Abs() > 2*time.Minute {
			t.Errorf("%s: %v, brute force %v", name, got, want)
		}
	}
	// Vega and M 42 rise and set from Zagreb; checked against a
	// minute-by-minute scan of AltAz over the day.
	for _, c := range []struct {
		name    string
		ra, dec float64
	}{{"Vega", 279.2347, 38.7837}, {"M 42", 83.82, -5.39}} {
		alt := func(t time.Time) float64 {
			a, _ := AltAz(c.ra, c.dec, t, lat, lon)

			return a
		}
		r := RiseTransitSet(c.ra, c.dec, lat, lon, day)
		if r.Circumpolar || r.NeverRises {
			t.Errorf("%s: %+v", c.name, r)
		}
		near(c.name+" rise", r.Rise, crossing(alt, starHorizon, start, true))
		near(c.name+" set", r.Set, crossing(alt, starHorizon, start, false))
		top, best := start, -90.0
		for m := start; m.Before(start.AddDate(0, 0, 1)); m = m.Add(time.Minute) {
			if a := alt(m); a > best {
				top, best = m, a
			}
		}
		near(c.name+" transit", r.Transit, top)
	}
}

func TestRiseTransitSetNoRiseOrSet(t *testing.T) {
	day := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if r := RiseTransitSet(37.95, 89, 45, 16, day); !r.Circumpolar || r.NeverRises || !r.Rise.IsZero() || !r.Set.IsZero() || r.Transit.IsZero() {
		t.Errorf("dec +89°: %+v", r)
	}
	if r := RiseTransitSet(0, -80, 45, 16, day); r.Circumpolar || !r.NeverRises || !r.Rise.IsZero() || !r.Set.IsZero() {
		t.Errorf("dec -80°: %+v", r)
	}
}

func TestRiseTransitSetOutsideDay(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Zagreb")
	if err != nil {
		t.Fatal(err)
	}
	// 29 March 2026 is 23 hours long. A target due to transit 23.5 h after
	// midnight (its previous transit about 26 minutes before midnight) does
	// not transit that local day.
	start := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	ra := math.Mod(lst(start.Add(23*time.Hour+30*time.Minute), 16), 360)
	if r := RiseTransitSet(ra, 0, 45, 16, start.Add(12*time.Hour)); !r.Transit.IsZero() || r.Rise.IsZero() || r.Set.IsZero() {
		t.Errorf("DST day: %+v", r)
	}
}
