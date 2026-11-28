// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"math"
	"testing"

	"github.com/dkorunic/nightglass/catalog"
)

// The chart's points come in the order SkyChart describes, each where
// catalog.AltAz (the target table's) puts it.
func TestSkyChart(t *testing.T) {
	a, _ := newApp(t)
	setSettings(t, a, func(st *Settings) { st.LatDeg, st.LonDeg = 0, 0 })
	if a.SkyPositions() != nil || a.AltAzOf(0, 0) != nil {
		t.Error("places without a location")
	}
	setSettings(t, a, func(st *Settings) { st.LatDeg, st.LonDeg = 45.8, 16 })
	c, err := a.SkyChart()
	if err != nil {
		t.Fatal(err)
	}
	stars := len(c.Stars) / 4
	vertices := 0
	for _, f := range c.Constellations {
		for _, n := range f.Lines {
			vertices += n
		}
	}
	p := a.SkyPositions()
	if len(p) != 2*(stars+vertices+len(c.Constellations)) {
		t.Fatalf("%d places for %d stars, %d vertices, %d names", len(p)/2, stars, vertices, len(c.Constellations))
	}
	vega := -1
	for i, n := range c.Names {
		if n == "Vega" {
			vega = i
		}
	}
	if vega < 0 || math.Abs(float64(c.Stars[4*vega])-279.23) > 0.05 {
		t.Fatalf("Vega %d", vega)
	}
	alt, az := catalog.AltAz(float64(c.Stars[4*vega]), float64(c.Stars[4*vega+1]), a.now(), 45.8, 16)
	if math.Abs(float64(p[2*vega])-alt) > 0.01 || math.Abs(float64(p[2*vega+1])-az) > 0.01 {
		t.Errorf("Vega at %v %v, want %v %v", p[2*vega], p[2*vega+1], alt, az)
	}
	if aa := a.AltAzOf(279.23, 38.78); math.Abs(aa[0]-alt) > 0.05 {
		t.Errorf("AltAzOf %v, want %v", aa, alt)
	}
	for _, f := range c.Constellations {
		if f.Lines == nil {
			t.Errorf("%s: lines null in JSON", f.Name) // the frontend iterates them
		}
	}
	// The Moon and planets, the chosen list unchanged.
	before := a.Settings().Catalog
	if b := a.SkyBodies(); len(b.Rows) != 8 || len(b.Places) != 16 || a.Settings().Catalog != before {
		t.Errorf("bodies %d, %d places, list %q → %q", len(b.Rows), len(b.Places), before, a.Settings().Catalog)
	}
}
