// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"testing"
	"time"
)

// The Sun & Moon card against PyEphem 4.2.1 (no refraction model beyond the
// -0:34 horizon, upper limb; twilight by the centre at -18°), UTC.
func TestTonight(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse("2006/01/02 15:04:05", s)
		if err != nil {
			t.Fatal(err)
		}

		return v
	}
	cest := time.FixedZone("CEST", 2*3600)
	aest := time.FixedZone("AEST", 10*3600)
	for _, c := range []struct {
		name     string
		now      time.Time
		lat, lon float64
		events   [6]string // sunset, dusk, dawn, sunrise, moonrise, moonset; "" none
		illum    float64
		age      float64
		phase    string
	}{
		{
			"zagreb", time.Date(2026, 10, 22, 20, 0, 0, 0, cest), 45.81, 15.98,
			[6]string{"2026/10/22 15:58:26", "2026/10/22 17:37:51", "2026/10/23 03:43:44", "2026/10/23 05:23:24", "2026/10/22 14:11:35", "2026/10/23 02:00:50"},
			0.862, 12.09, "Waxing Gibbous",
		},
		{
			// Before noon the card is still last night's.
			"zagreb morning", time.Date(2026, 10, 23, 6, 0, 0, 0, cest), 45.81, 15.98,
			[6]string{"2026/10/22 15:58:26", "2026/10/22 17:37:51", "2026/10/23 03:43:44", "2026/10/23 05:23:24", "2026/10/22 14:11:35", "2026/10/23 02:00:50"},
			-1, -1, "Waxing Gibbous",
		},
		{
			"sydney", time.Date(2026, 6, 21, 20, 0, 0, 0, aest), -33.87, 151.21,
			[6]string{"2026/06/21 06:53:47", "2026/06/21 08:23:16", "2026/06/21 19:30:43", "2026/06/21 21:00:12", "2026/06/22 01:50:54", "2026/06/21 13:38:53"},
			0.450, 6.30, "First Quarter",
		},
		{
			// The midnight sun: no Sun events at all.
			"tromso", time.Date(2026, 6, 21, 22, 0, 0, 0, cest), 69.65, 18.96,
			[6]string{"", "", "", "", "2026/06/21 10:17:48", "2026/06/21 22:13:38"},
			0.493, 6.71, "First Quarter",
		},
	} {
		n := Tonight(c.now, c.lat, c.lon)
		got := [6]time.Time{n.SunSet, n.AstroDusk, n.AstroDawn, n.SunRise, n.MoonRise, n.MoonSet}
		for i, want := range c.events {
			if want == "" {
				if !got[i].IsZero() {
					t.Errorf("%s event %d: %v, want none", c.name, i, got[i])
				}

				continue
			}
			if d := got[i].Sub(at(want)); d.Abs() > 2*time.Minute {
				t.Errorf("%s event %d: %v, want %s UTC (off %v)", c.name, i, got[i].UTC(), want, d)
			}
		}
		if c.illum >= 0 && math.Abs(n.MoonIllum-c.illum) > 0.01 {
			t.Errorf("%s illuminated %.3f, want %.3f", c.name, n.MoonIllum, c.illum)
		}
		if c.age >= 0 && math.Abs(n.MoonAge-c.age) > 0.05 {
			t.Errorf("%s age %.2f, want %.2f", c.name, n.MoonAge, c.age)
		}
		if n.MoonPhase != c.phase {
			t.Errorf("%s phase %q, want %q", c.name, n.MoonPhase, c.phase)
		}
	}
}
