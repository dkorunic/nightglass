// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"time"
)

// SunRADec is the Sun's right ascension and declination of date in degrees
// (Astronomical Almanac low-precision formula, ~0.01°), copied from
// github.com/dkorunic/astro-recommender internal/astro (MIT).
func SunRADec(t time.Time) (float64, float64) {
	n := daysJ2000(t)
	g := (357.528 + 0.9856003*n) * deg
	lambda := (280.460 + 0.9856474*n + 1.915*math.Sin(g) + 0.020*math.Sin(2*g)) * deg
	eps := (23.439 - 0.0000004*n) * deg
	ra := math.Atan2(math.Cos(eps)*math.Sin(lambda), math.Cos(lambda))
	dec := math.Asin(math.Sin(eps) * math.Sin(lambda))

	return math.Mod(ra/deg+360, 360), dec / deg
}

// StartWindow is when imaging a target can start tonight, as the app's
// image settings check a manual start time: from Min, the first minute
// from now (or from sunset) the target is at least minAlt degrees up, to
// Dawn, the next sunrise. Visible is false when it does not get that high
// before sunrise; Dawn is zero when the Sun does not rise within a day.
// ponytail: the app takes these from its sky chart's visibility code
// (calculateBestVisibilityForSkyObject, computeSunDawnDusk); this
// minute-by-minute search is an approximation of it.
type StartWindow struct {
	Min, Dawn time.Time
	Visible   bool
}

// ImagingStart finds the StartWindow for raJ2000, decJ2000 (degrees) at now
// for an observer at lat, lon (degrees, east positive).
func ImagingStart(raJ2000, decJ2000, minAlt float64, now time.Time, lat, lon float64) StartWindow {
	sunUp := func(t time.Time) bool {
		ra, dec := SunRADec(t)
		alt, _ := AltAz(ra, dec, t, lat, lon)

		return alt > 0
	}
	t, limit := now.Truncate(time.Minute), now.Add(24*time.Hour)
	for t.Before(limit) && sunUp(t) { // wait for sunset
		t = t.Add(time.Minute)
	}
	w := StartWindow{}
	for ; t.Before(limit); t = t.Add(time.Minute) {
		if sunUp(t) {
			w.Dawn = t

			break
		}
		if alt, _ := AltAz(raJ2000, decJ2000, t, lat, lon); !w.Visible && alt >= minAlt {
			w.Min, w.Visible = t, true
		}
	}
	if !w.Visible {
		w.Min = now
	}

	return w
}
