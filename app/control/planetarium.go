// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"math"
	"time"

	"github.com/dkorunic/nightglass/catalog"
)

// The Planetarium of Celestron's web client: the sky above the observer
// with the stars, the constellations, the target lists and the telescope,
// now or at a chosen time. Go places every point (catalog.AltAz, as the
// target table's); the frontend projects and draws them.

// SkyChart is what the Planetarium draws, sent once: per star its J2000
// place, V magnitude and B−V colour; the brightest stars' names by index;
// the constellations, each a name and the vertex counts of its lines and
// boundary; and the guide paths (ecliptic, celestial equator, equatorial
// grid). SkyPositions gives their places in this order.
type SkyChart struct {
	Names          map[int]string
	Stars          []float32 // RA, Dec (degrees, J2000), V, B−V per star
	Constellations []SkyFigure
	Paths          []SkyPath
}

// SkyFigure is a constellation in SkyChart.
type SkyFigure struct {
	Name   string
	Lines  []int // the vertex count of each polyline
	Bounds []int // likewise, its boundary
}

// SkyPath is a guide line in SkyChart: Kind "ecliptic", "equator" or
// "grid" (the equatorial grid), and its vertex count.
type SkyPath struct {
	Kind  string
	Count int
}

// skyPaths are the guide lines' J2000 RA, Dec vertices, in degrees.
var skyPaths = func() (kinds []string, paths [][]float64) {
	eps := 23.4393 * math.Pi / 180 // the J2000 obliquity
	var ecl, equ []float64
	for l := 0.0; l <= 360; l += 2 {
		lr := l * math.Pi / 180
		ra := math.Atan2(math.Sin(lr)*math.Cos(eps), math.Cos(lr)) * 180 / math.Pi
		ecl = append(ecl, math.Mod(ra+360, 360), math.Asin(math.Sin(lr)*math.Sin(eps))*180/math.Pi)
		equ = append(equ, l, 0)
	}
	kinds, paths = append(kinds, "ecliptic", "equator"), append(paths, ecl, equ)
	for ra := 0.0; ra < 360; ra += 30 { // every 2 h
		var m []float64
		for dec := -80.0; dec <= 80; dec += 5 {
			m = append(m, ra, dec)
		}
		kinds, paths = append(kinds, "grid"), append(paths, m)
	}
	for _, dec := range []float64{-60, -30, 30, 60} {
		var p []float64
		for ra := 0.0; ra <= 360; ra += 5 {
			p = append(p, ra, dec)
		}
		kinds, paths = append(kinds, "grid"), append(paths, p)
	}

	return kinds, paths
}

// SkyChart returns the Planetarium's stars, constellations and guides.
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
	// [], not null, where there are none: the frontend iterates them.
	for _, k := range s.Constellations {
		f := SkyFigure{Name: k.Name, Lines: []int{}, Bounds: []int{}}
		for _, l := range k.Lines {
			f.Lines = append(f.Lines, len(l)/2)
		}
		for _, b := range k.Bounds {
			f.Bounds = append(f.Bounds, len(b)/2)
		}
		c.Constellations = append(c.Constellations, f)
	}
	kinds, paths := skyPaths()
	for i, p := range paths {
		c.Paths = append(c.Paths, SkyPath{Kind: kinds[i], Count: len(p) / 2})
	}

	return c, nil
}

// skyTime is the Planetarium's time: at (RFC 3339), or now for "".
func (a *App) skyTime(at string) time.Time {
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		return t
	}

	return a.now()
}

// SkyPositions is where the chart's points are at the time at ("": now),
// altitude and azimuth in degrees interleaved: the stars; every
// constellation's line vertices, then its boundary's, in order; each
// constellation's name; then the guide paths. Without a location, none.
func (a *App) SkyPositions(at string) []float32 {
	st := a.Settings()
	s, err := catalog.Sky()
	if err != nil || !st.HasLocation() {
		return nil
	}
	t := a.skyTime(at)
	out := make([]float32, 0, 2*(len(s.Stars)+8000))
	add := func(ra, dec float64) {
		alt, az := catalog.AltAz(ra, dec, t, st.LatDeg, st.LonDeg)
		out = append(out, float32(alt), float32(az))
	}
	for _, x := range s.Stars {
		add(float64(x.RA), float64(x.Dec))
	}
	for _, k := range s.Constellations {
		for _, l := range append(append([][]float32{}, k.Lines...), k.Bounds...) {
			for i := 0; i+1 < len(l); i += 2 {
				add(float64(l[i]), float64(l[i+1]))
			}
		}
	}
	for _, k := range s.Constellations {
		add(float64(k.RA), float64(k.Dec))
	}
	_, paths := skyPaths()
	for _, p := range paths {
		for i := 0; i+1 < len(p); i += 2 {
			add(p[i], p[i+1])
		}
	}

	return out
}

// SkyBodies is the Sun, Moon and planets for the Planetarium at the time
// at ("": now), without making Solar System the chosen list as Targets
// does; the Sun is the last row.
func (a *App) SkyBodies(at string) SkyBodyList {
	t := a.skyTime(at)
	st := a.Settings()
	var b SkyBodyList
	for _, x := range a.targets(catalog.SolarSystem, t) {
		b.Rows = append(b.Rows, Row{catalog.SolarSystem, x.Name, x.Description, x.Type, x.Constellation, x.RADeg, x.DecDeg, x.Mag, x.Size})
	}
	ra, dec := catalog.SunRADec(t)
	b.Rows = append(b.Rows, Row{Catalog: catalog.SolarSystem, Name: "Sun", Type: "Sun", RA: ra, Dec: dec})
	if st.HasLocation() {
		for _, r := range b.Rows {
			alt, az := catalog.AltAz(r.RA, r.Dec, t, st.LatDeg, st.LonDeg)
			b.Places = append(b.Places, float32(alt), float32(az))
		}
	}

	return b
}

// SkyBodyList is SkyBodies.
type SkyBodyList struct {
	Rows   []Row
	Places []float32
}

// PositionsAt is Positions at the time at ("": now), for the Planetarium.
func (a *App) PositionsAt(list, at string) []float32 {
	s := a.Settings()
	if !s.HasLocation() {
		return nil
	}
	t := a.skyTime(at)
	var out []float32
	for _, l := range a.lists(list) {
		for _, x := range a.targets(l, t) {
			alt, az := catalog.AltAz(x.RADeg, x.DecDeg, t, s.LatDeg, s.LonDeg)
			out = append(out, float32(alt), float32(az))
		}
	}

	return out
}

// AltAzOf is where a J2000 place is at the time at ("": now), altitude
// and azimuth in degrees (the Planetarium centres typed coordinates and
// stars on it).
func (a *App) AltAzOf(raDeg, decDeg float64, at string) []float64 {
	st := a.Settings()
	if !st.HasLocation() {
		return nil
	}
	alt, az := catalog.AltAz(raDeg, decDeg, a.skyTime(at), st.LatDeg, st.LonDeg)

	return []float64{alt, az}
}
