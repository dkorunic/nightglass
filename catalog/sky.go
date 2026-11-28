// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"bytes"
	_ "embed"
	"encoding/gob"
	"sync"
)

// The Planetarium's sky: the stars of the Yale Bright Star Catalogue and
// the constellations' lines and names, packed by skygen.go (sources and
// licences in SKY-NOTICE.md).

//go:embed sky.gob
var skyGob []byte

//go:embed SKY-NOTICE.md
var skyNotice string

// SkyStar is a star to naked-eye limit: J2000 degrees, V magnitude, B−V
// colour index, Bright Star Catalogue number.
type SkyStar struct {
	RA, Dec, Mag, BV float32
	HR               uint16
}

// SkyConstellation is a constellation's name, where its name is written,
// its stick figure and its boundary as polylines of J2000 RA, Dec pairs in
// degrees.
type SkyConstellation struct {
	Abbr, Name string
	Lines      [][]float32
	Bounds     [][]float32 // its boundary, closed, likewise
	RA, Dec    float32
}

// SkyData is the packed sky.
type SkyData struct {
	Stars          []SkyStar
	Constellations []SkyConstellation
}

var sky = sync.OnceValues(func() (*SkyData, error) {
	var s SkyData
	if err := gob.NewDecoder(bytes.NewReader(skyGob)).Decode(&s); err != nil {
		return nil, err
	}

	return &s, nil
})

// Sky is the Planetarium's stars and constellations.
func Sky() (*SkyData, error) { return sky() }

// StarNames are the common names of the brightest stars, by Bright Star
// Catalogue number (the IAU's names).
var StarNames = map[uint16]string{
	2491: "Sirius", 2326: "Canopus", 5340: "Arcturus", 7001: "Vega", 1708: "Capella", 1713: "Rigel",
	2943: "Procyon", 2061: "Betelgeuse", 472: "Achernar", 7557: "Altair", 1457: "Aldebaran", 6134: "Antares",
	5056: "Spica", 2990: "Pollux", 8728: "Fomalhaut", 7924: "Deneb", 3982: "Regulus", 2891: "Castor",
	424: "Polaris", 1790: "Bellatrix", 1903: "Alnilam", 1017: "Mirfak", 4301: "Dubhe", 5191: "Alkaid",
	4905: "Alioth", 936: "Algol", 5267: "Hadar", 4730: "Acrux", 4853: "Mimosa", 5459: "Rigil Kentaurus",
	6527: "Shaula", 1791: "Elnath", 1948: "Alnitak", 1852: "Mintaka", 4534: "Denebola", 15: "Alpheratz",
	168: "Schedar", 8308: "Enif", 8775: "Scheat", 39: "Algenib", 617: "Hamal", 6705: "Eltanin",
	5793: "Alphecca", 6556: "Rasalhague", 7121: "Nunki", 3748: "Alphard", 1165: "Alcyone", 4057: "Algieba",
	2421: "Alhena", 3485: "Avior", 3307: "Miaplacidus", 2618: "Adhara", 2693: "Wezen",
}

// SkyNotice is the sources and licences of the Planetarium's sky.
func SkyNotice() string { return skyNotice }
