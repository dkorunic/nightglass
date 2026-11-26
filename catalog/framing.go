// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"strings"
)

// framingParts lists the catalogued parts of large nebulae whose own
// designation marks just one part of them (a lobe, a rim, a cluster). The
// app aims the Origin at a framing point of its own for such objects, not
// at the catalogue position; Nightglass computes its framing points from
// these parts instead (public cross-identifications, looked up in the
// embedded lists).
var framingParts = map[string][]string{
	"NGC 2237": {"NGC 2244"},            // the Rosette Nebula surrounds its cluster
	"IC 5067":  {"IC 5067", "IC 5070"},  // the Pelican Nebula
	"IC 1396":  {"IC 1396", "LBN 452"},  // with the Elephant's Trunk
	"IC 405":   {"IC 405", "LBN 795"},   // the Flaming Star Nebula
	"IC 5146":  {"IC 5146", "LBN 424"},  // the Cocoon Nebula
	"NGC 7023": {"NGC 7023", "LBN 487"}, // the Iris Nebula
}

// Framing is where to aim the Origin at t: for the nebulae above, the middle
// of their catalogued parts (the mean of their directions; the Rosette's is
// its central cluster); ok is false for any other target, which is aimed at
// its own position. A target matches by its name or by a designation in its
// description (Caldwell's C 49 is NGC 2237).
func (s *Set) Framing(t Target) (raDeg, decDeg float64, ok bool) {
	parts := framingParts[framedName(t)]
	if parts == nil {
		return 0, 0, false
	}
	var x, y, z float64
	n := 0
	for _, p := range parts {
		pt, _, found := s.Find(p)
		if !found {
			continue
		}
		ra, dec := pt.RADeg*math.Pi/180, pt.DecDeg*math.Pi/180
		x += math.Cos(dec) * math.Cos(ra)
		y += math.Cos(dec) * math.Sin(ra)
		z += math.Sin(dec)
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	raDeg = math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
	decDeg = math.Atan2(z, math.Hypot(x, y)) * 180 / math.Pi

	return raDeg, decDeg, true
}

// framedName is the framed designation t is, "" for none.
func framedName(t Target) string {
	names := append([]string{t.Name}, strings.Split(t.Description, ",")...)
	for _, n := range names {
		for k := range framingParts {
			if Key(n) == Key(k) {
				return k
			}
		}
	}

	return ""
}
