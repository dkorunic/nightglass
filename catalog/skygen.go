// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

//go:build ignore

// skygen packs the Planetarium's sky into catalog/sky.gob:
//
//	go run catalog/skygen.go -bsc bsc5.dat -lines constellations.lines.json -names constellations.json \
//		-bounds constellations.bounds.json
//
// Sources (see catalog/SKY-NOTICE.md): the Yale Bright Star Catalogue, 5th
// revised edition (Hoffleit & Warren 1991, NASA ADC / CDS V/50; public
// domain), e.g. http://tdc-www.harvard.edu/catalogs/bsc5.dat.gz; and the
// constellation lines, name positions and boundaries of d3-celestial (Olaf Frohn, BSD
// 3-Clause, after the IAU's constellation charts),
// https://github.com/ofrohn/d3-celestial/tree/master/data.
package main

import (
	"bufio"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
)

// The packed sky, as catalog/sky.go decodes it.
type skyStar struct {
	RA, Dec, Mag, BV float32
	HR               uint16
}
type skyConstellation struct {
	Abbr, Name string
	RA, Dec    float32
	Lines      [][]float32 // RA, Dec pairs in degrees, J2000
	Bounds     [][]float32 // its boundary, closed, likewise
}
type sky struct {
	Stars          []skyStar
	Constellations []skyConstellation
}

func main() {
	bsc := flag.String("bsc", "bsc5.dat", "Yale Bright Star Catalogue, bsc5.dat")
	lines := flag.String("lines", "constellations.lines.json", "d3-celestial constellations.lines.json")
	names := flag.String("names", "constellations.json", "d3-celestial constellations.json")
	bounds := flag.String("bounds", "constellations.bounds.json", "d3-celestial constellations.bounds.json")
	out := flag.String("out", "catalog/sky.gob", "output")
	flag.Parse()

	var s sky
	var err error
	if s.Stars, err = readBSC(*bsc); err != nil {
		log.Fatal(err)
	}
	if s.Constellations, err = readConstellations(*lines, *names, *bounds); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(s); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d stars, %d constellations → %s\n", len(s.Stars), len(s.Constellations), *out)
}

// readBSC reads the catalogue's fixed columns (its ReadMe, bytes 1-based):
// HR 1–4, J2000 RA 76–83 (hhmmss.s), Dec 84–90 (±ddmmss), V 103–107,
// B−V 110–114. Records without a position (novae and other removed
// objects) are skipped.
func readBSC(path string) ([]skyStar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	col := func(l string, a, b int) string {
		if len(l) < b {
			return ""
		}

		return strings.TrimSpace(l[a-1 : b])
	}
	num := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(s, 64)

		return v, err == nil
	}
	var stars []skyStar
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		hr, err := strconv.Atoi(col(l, 1, 4))
		if err != nil {
			return nil, fmt.Errorf("bad HR in %q", l)
		}
		rh, ok1 := num(col(l, 76, 77))
		rm, ok2 := num(col(l, 78, 79))
		rs, ok3 := num(col(l, 80, 83))
		dd, ok4 := num(col(l, 85, 86))
		dm, ok5 := num(col(l, 87, 88))
		ds, ok6 := num(col(l, 89, 90))
		v, ok7 := num(col(l, 103, 107))
		if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) {
			continue
		}
		dec := dd + dm/60 + ds/3600
		if col(l, 84, 84) == "-" {
			dec = -dec
		}
		bv, ok := num(col(l, 110, 114))
		if !ok {
			bv = 0.6 // unknown colour: a sunlike white
		}
		stars = append(stars, skyStar{
			RA: float32((rh + rm/60 + rs/3600) * 15), Dec: float32(dec), Mag: float32(v), BV: float32(bv), HR: uint16(hr),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// The columns are right if a few known stars come out where they are.
	for _, k := range []struct {
		hr           uint16
		ra, dec, mag float64
	}{{2491, 101.287, -16.716, -1.46}, {7001, 279.234, 38.784, 0.03}, {424, 37.946, 89.264, 2.02}} {
		ok := false
		for _, s := range stars {
			if s.HR == k.hr {
				ok = math.Abs(float64(s.RA)-k.ra) < 0.05 && math.Abs(float64(s.Dec)-k.dec) < 0.05 && math.Abs(float64(s.Mag)-k.mag) < 0.05
			}
		}
		if !ok {
			return nil, fmt.Errorf("HR %d not where it should be: the columns are wrong", k.hr)
		}
	}

	return stars, nil
}

// readConstellations joins the lines (MultiLineString) and the names and
// label positions (Point), keyed by abbreviation. GeoJSON longitude is RA
// folded to −180…180°.
func readConstellations(linesPath, namesPath, boundsPath string) ([]skyConstellation, error) {
	type feature struct {
		ID         string
		Properties struct{ Name string }
		Geometry   struct {
			Type        string
			Coordinates json.RawMessage
		}
	}
	read := func(path string) ([]feature, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var fc struct{ Features []feature }

		return fc.Features, json.Unmarshal(b, &fc)
	}
	ra := func(lon float64) float32 {
		if lon < 0 {
			lon += 360
		}

		return float32(lon)
	}
	names, err := read(namesPath)
	if err != nil {
		return nil, err
	}
	lines, err := read(linesPath)
	if err != nil {
		return nil, err
	}
	bounds, err := read(boundsPath)
	if err != nil {
		return nil, err
	}
	// Boundaries run along parallels and meridians: points every 2° keep a
	// long edge on its arc once projected.
	dense := func(ring [][2]float64) []float32 {
		var out []float32
		for i, p := range ring {
			out = append(out, ra(p[0]), float32(p[1]))
			if i+1 == len(ring) {
				break
			}
			q := ring[i+1]
			dl := q[0] - p[0]
			if dl > 180 {
				dl -= 360
			} else if dl < -180 {
				dl += 360
			}
			n := int(math.Max(math.Abs(dl), math.Abs(q[1]-p[1])) / 2)
			for k := 1; k < n; k++ {
				f := float64(k) / float64(n)
				out = append(out, ra(math.Mod(p[0]+f*dl+540, 360)-180), float32(p[1]+f*(q[1]-p[1])))
			}
		}

		return out
	}
	boundsByID := map[string][][]float32{}
	for _, f := range bounds {
		var poly [][][2]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &poly); err != nil {
			return nil, fmt.Errorf("%s bounds: %w", f.ID, err)
		}
		for _, ring := range poly {
			boundsByID[f.ID] = append(boundsByID[f.ID], dense(ring))
		}
	}
	byID := map[string][][][2]float64{}
	for _, f := range lines {
		var ml [][][2]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &ml); err != nil {
			return nil, fmt.Errorf("%s lines: %w", f.ID, err)
		}
		byID[f.ID] = append(byID[f.ID], ml...) // Serpens comes in two parts
	}
	var out []skyConstellation
	seen := map[string]bool{}
	for _, f := range names {
		var pt [2]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &pt); err != nil {
			return nil, fmt.Errorf("%s label: %w", f.ID, err)
		}
		c := skyConstellation{Abbr: f.ID, Name: f.Properties.Name, RA: ra(pt[0]), Dec: float32(pt[1])}
		// Serpens's second part: its own label, the lines already given.
		var ls [][][2]float64
		if !seen[f.ID] {
			ls = byID[f.ID]
			c.Bounds = boundsByID[f.ID]
		}
		seen[f.ID] = true
		for _, l := range ls {
			var pl []float32
			for _, p := range l {
				pl = append(pl, ra(p[0]), float32(p[1]))
			}
			c.Lines = append(c.Lines, pl)
		}
		out = append(out, c)
	}
	if len(seen) != 88 {
		return nil, fmt.Errorf("%d constellations, want 88", len(seen))
	}

	return out, nil
}
