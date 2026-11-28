// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"strings"
	"testing"
)

func TestSky(t *testing.T) {
	s, err := Sky()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Stars) < 9000 || len(s.Constellations) != 89 {
		t.Fatalf("%d stars, %d constellations", len(s.Stars), len(s.Constellations))
	}
	by := map[uint16]SkyStar{}
	for _, st := range s.Stars {
		by[st.HR] = st
		if st.RA < 0 || st.RA >= 360 || math.Abs(float64(st.Dec)) > 90 {
			t.Fatalf("HR %d at %v %v", st.HR, st.RA, st.Dec)
		}
	}
	// Each name is on one of the brightest stars: a wrong number would be
	// a faint one.
	for hr, name := range StarNames {
		if st, ok := by[hr]; !ok || st.Mag > 3.1 {
			t.Errorf("%s: HR %d is %+v", name, hr, st)
		}
	}
	if v := by[7001]; math.Abs(float64(v.RA)-279.23) > 0.05 || math.Abs(float64(v.Dec)-38.78) > 0.05 {
		t.Errorf("Vega at %v %v", v.RA, v.Dec)
	}
	// Orion's lines meet Betelgeuse.
	for _, c := range s.Constellations {
		if c.Abbr != "Ori" {
			continue
		}
		near := false
		for _, l := range c.Lines {
			for i := 0; i+1 < len(l); i += 2 {
				near = near || math.Hypot(float64(l[i]-88.79), float64(l[i+1]-7.41)) < 0.1
			}
		}
		if c.Name != "Orion" || !near {
			t.Errorf("Orion %q, Betelgeuse on its lines: %v", c.Name, near)
		}
	}
	if !strings.Contains(SkyNotice(), "Olaf Frohn") {
		t.Error("d3-celestial's notice missing")
	}
}
