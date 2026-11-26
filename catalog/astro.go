// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"time"
)

// The functions below are copied from github.com/dkorunic/astro-recommender
// internal/astro (MIT), where they are checked against IAU SOFA.

const deg = math.Pi / 180

// AltAz returns the geometric altitude and the azimuth (from north through
// east) in degrees of a J2000 position (degrees) for an observer at lat, lon
// (degrees, east positive) at t. It precesses to the mean equator of date
// first; nutation, aberration and refraction are left out (all below 0.1°
// except refraction near the horizon).
func AltAz(raJ2000, decJ2000 float64, t time.Time, lat, lon float64) (float64, float64) {
	ra, dec := precess(raJ2000, decJ2000, daysJ2000(t)/36525)
	sinLat, cosLat := math.Sincos(lat * deg)
	sinDec, cosDec := math.Sincos(dec * deg)
	ha := lst(t, lon) - ra
	sinH, cosH := math.Sincos(ha * deg)
	// Clamped: rounding can push the sine a hair past 1 at the zenith.
	alt := math.Asin(max(-1, min(1, sinLat*sinDec+cosLat*cosDec*cosH))) / deg
	az := math.Atan2(-sinH*cosDec, sinDec*cosLat-cosDec*sinLat*cosH) / deg

	return alt, math.Mod(az+360, 360)
}

// precess moves ra, dec (degrees) from J2000 by t Julian centuries (IAU 1976,
// Lieske 1977).
func precess(ra, dec, t float64) (float64, float64) {
	zeta := (2306.2181*t + 0.30188*t*t + 0.017998*t*t*t) / 3600 * deg
	z := (2306.2181*t + 1.09468*t*t + 0.018203*t*t*t) / 3600 * deg
	theta := (2004.3109*t - 0.42665*t*t - 0.041833*t*t*t) / 3600 * deg
	sinA, cosA := math.Sincos(ra*deg + zeta)
	sinD, cosD := math.Sincos(dec * deg)
	sinT, cosT := math.Sincos(theta)
	a := cosD * sinA
	b := cosT*cosD*cosA - sinT*sinD
	// Within rounding of the pole c lands above 1 and Asin would be NaN.
	c := max(-1, min(1, sinT*cosD*cosA+cosT*sinD))

	return math.Mod((math.Atan2(a, b)+z)/deg+360, 360), math.Asin(c) / deg
}

// daysJ2000 returns days since J2000.0 (UT, ignoring ΔT).
func daysJ2000(t time.Time) float64 {
	return float64(t.Unix())/86400 - 10957.5
}

// lst returns the local mean sidereal time in degrees (not normalized).
func lst(t time.Time, lon float64) float64 {
	return 280.46061837 + 360.98564736629*daysJ2000(t) + lon
}
