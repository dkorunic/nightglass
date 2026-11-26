// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import "math"

type zoneCity struct {
	Country  string
	Lat, Lon float64
}

// CountryCode is the ISO 3166 alpha-2 code for a position in degrees, as
// the native FillCelestronOriginCountryCode finds it for
// Network/SetRegulatoryDomain: the country of the nearest known city.
// ponytail: the cities are tzdata's ~400 zone cities, coarser than the
// app's city database, so a site near a border can get the neighbour.
func CountryCode(latDeg, lonDeg float64) string {
	best, code := math.Inf(1), ""
	for _, z := range zoneCities {
		if d := angle(latDeg, lonDeg, z.Lat, z.Lon); d < best {
			best, code = d, z.Country
		}
	}

	return code
}

// angle is the great-circle separation of two positions, in radians.
func angle(lat1, lon1, lat2, lon2 float64) float64 {
	r := math.Pi / 180
	p1, p2, dl := lat1*r, lat2*r, (lon2-lon1)*r
	c := math.Sin(p1)*math.Sin(p2) + math.Cos(p1)*math.Cos(p2)*math.Cos(dl)

	return math.Acos(max(-1, min(1, c)))
}
