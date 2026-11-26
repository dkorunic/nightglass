// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"testing"
)

// The framing points come from the parts in the embedded lists: each lies
// near the designation's own position, among its parts, and an entry of
// another list for the same object (Caldwell's C 49) frames the same.
func TestFraming(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name := range framingParts {
		own, _, ok := s.Find(name)
		if !ok {
			t.Fatalf("%s not in the lists", name)
		}
		ra, dec, ok := s.Framing(own)
		if !ok {
			t.Errorf("%s: no framing point", name)

			continue
		}
		d := sep(ra, dec, own.RADeg, own.DecDeg)
		if d == 0 || d > 1 {
			t.Errorf("%s framed %.2f° from its position, want within the nebula", name, d)
		}
		t.Logf("%-8s %8.4f h %+8.4f° (%.1f′ from %8.4f h %+8.4f°)", name, ra/15, dec, d*60, own.RADeg/15, own.DecDeg)
	}
	c49, _, _ := s.Find("C 49")
	ra, dec, ok := s.Framing(c49)
	ngc, _, _ := s.Find("NGC 2237")
	ra2, dec2, _ := s.Framing(ngc)
	if !ok || ra != ra2 || dec != dec2 {
		t.Errorf("C 49 framed at %v %v, NGC 2237 at %v %v", ra, dec, ra2, dec2)
	}
	m42, _, _ := s.Find("M 42")
	if _, _, ok := s.Framing(m42); ok {
		t.Error("M 42 has a framing point")
	}
}

func sep(ra1, dec1, ra2, dec2 float64) float64 {
	r := math.Pi / 180
	c := math.Sin(dec1*r)*math.Sin(dec2*r) + math.Cos(dec1*r)*math.Cos(dec2*r)*math.Cos((ra1-ra2)*r)

	return math.Acos(min(1, c)) / r
}
