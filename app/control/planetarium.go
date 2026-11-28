// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"github.com/dkorunic/nightglass/catalog"
)

// The Planetarium of Celestron's web client: the sky above the observer
// with the stars, the constellations, the target lists and the telescope.
// Go places every point (catalog.AltAz, as the target table's); the
// frontend projects and draws them.

// SkyChart is what the Planetarium draws, sent once: per star its J2000
// place, V magnitude and B−V colour; the brightest stars' names by index;
// and the constellations, each a name and the vertex counts of its lines,
// whose vertices follow the stars in SkyPositions.
type SkyChart struct {
	Names          map[int]string
	Stars          []float32 // RA, Dec (degrees, J2000), V, B−V per star
	Constellations []SkyFigure
}

// SkyFigure is a constellation in SkyChart.
type SkyFigure struct {
	Name  string
	Lines []int // the vertex count of each polyline
}

// SkyChart returns the Planetarium's stars and constellations.
func (*App) SkyChart() (SkyChart, error) {
	s, err := catalog.Sky()
	if err != nil {
		return SkyChart{}, err
	}
	c := SkyChart{Names: map[int]string{}, Stars: make([]float32, 0, 4*len(s.Stars))}
	for i, st := range s.Stars {
		c.Stars = append(c.Stars, st.RA, st.Dec, st.Mag, st.BV)
		if n := catalog.StarNames[st.HR]; n != "" {
			c.Names[i] = n
		}
	}
	for _, k := range s.Constellations {
		f := SkyFigure{Name: k.Name, Lines: []int{}} // Serpens's second part has none: [], not null
		for _, l := range k.Lines {
			f.Lines = append(f.Lines, len(l)/2)
		}
		c.Constellations = append(c.Constellations, f)
	}

	return c, nil
}

// SkyPositions is where the chart's points are now, altitude and azimuth
// in degrees interleaved: the stars, then every constellation's line
// vertices in order, then each constellation's name. Without a location,
// none.
func (a *App) SkyPositions() []float32 {
	st := a.Settings()
	s, err := catalog.Sky()
	if err != nil || !st.HasLocation() {
		return nil
	}
	now := a.now()
	out := make([]float32, 0, 2*(len(s.Stars)+2000))
	add := func(ra, dec float32) {
		alt, az := catalog.AltAz(float64(ra), float64(dec), now, st.LatDeg, st.LonDeg)
		out = append(out, float32(alt), float32(az))
	}
	for _, x := range s.Stars {
		add(x.RA, x.Dec)
	}
	for _, k := range s.Constellations {
		for _, l := range k.Lines {
			for i := 0; i+1 < len(l); i += 2 {
				add(l[i], l[i+1])
			}
		}
	}
	for _, k := range s.Constellations {
		add(k.RA, k.Dec)
	}

	return out
}

// SkyBodies is the Moon and planets for the Planetarium, with their places
// now (as Positions), without making Solar System the chosen list as
// Targets does.
func (a *App) SkyBodies() SkyBodyList {
	return SkyBodyList{Rows: a.rows(catalog.SolarSystem), Places: a.Positions(catalog.SolarSystem)}
}

// SkyBodyList is SkyBodies.
type SkyBodyList struct {
	Rows   []Row
	Places []float32
}

// AltAzOf is where a J2000 place is now, altitude and azimuth in degrees
// (the Planetarium centres typed coordinates and stars on it).
func (a *App) AltAzOf(raDeg, decDeg float64) []float64 {
	st := a.Settings()
	if !st.HasLocation() {
		return nil
	}
	alt, az := catalog.AltAz(raDeg, decDeg, a.now(), st.LatDeg, st.LonDeg)

	return []float64{alt, az}
}
