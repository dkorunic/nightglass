// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"testing"
	"time"
)

// separation is the angle in degrees between two positions (degrees).
func separation(ra1, dec1, ra2, dec2 float64) float64 {
	c := math.Sin(dec1*deg)*math.Sin(dec2*deg) + math.Cos(dec1*deg)*math.Cos(dec2*deg)*math.Cos((ra1-ra2)*deg)

	return math.Acos(max(-1, min(1, c))) / deg
}

func TestPrecessAgainstSOFA(t *testing.T) {
	// SOFA iauPmat06 (mean of date) at 2026-10-05 22:00 UTC, the vectors
	// astro-recommender's TestPrecess uses.
	tc := daysJ2000(time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)) / 36525
	for _, c := range [][4]float64{
		{0, 0, 0.34288, 0.14897},
		{83.8221, -5.3911, 84.15102, -5.37550},
		{37.95, 89.26, 46.69231, 89.37044},
	} {
		ra, dec := precess(c[0], c[1], tc)
		if d := separation(ra, dec, c[2], c[3]) * 3600; d > 1 {
			t.Errorf("precess(%v, %v) = %v, %v: %.2f\" from SOFA", c[0], c[1], ra, dec, d)
		}
	}
	// Within rounding of the precessed pole: clamped, not NaN.
	pole := daysJ2000(time.Date(2021, 5, 15, 0, 0, 0, 0, time.UTC)) / 36525
	if _, dec := precess(23.99087414049274*15, 89.88104100680276, pole); !(dec > 89.99 && dec <= 90) {
		t.Errorf("precess at the pole: dec %v, want 90", dec)
	}
}

func TestAltAzM42(t *testing.T) {
	// astro-recommender AltAz(Precess(...)) for M 42 from Zagreb. Without
	// precession the azimuth is 183.131°, so 0.02° catches a missing step.
	alt, az := AltAz(83.85, -5.45, time.Date(2026, 1, 15, 21, 0, 0, 0, time.UTC), 45.815, 15.982)
	if math.Abs(alt-38.715) > 0.02 || math.Abs(az-182.724) > 0.02 {
		t.Errorf("M 42 alt/az = %.3f, %.3f; want 38.715, 182.724", alt, az)
	}
}
