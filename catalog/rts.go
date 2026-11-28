// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"time"
)

// RTS is when a target rises, transits and sets on the observer's local
// calendar day (the day of `day` in day.Location()); a zero time is an event
// that does not happen that day.
type RTS struct {
	Rise, Transit, Set time.Time
	Circumpolar        bool // never sets: always above the horizon
	NeverRises         bool // always below the horizon
}

// starHorizon is the altitude of a point source on the horizon: the
// standard 34′ of refraction, without the Sun's and Moon's semidiameter.
const starHorizon = -34.0 / 60

// siderealRate is how far the sidereal time advances per solar day
// (degrees), the rate in lst.
const siderealRate = 360.98564736629

// RiseTransitSet for J2000 RA/Dec in degrees and the observer's latitude and
// longitude in degrees (east positive).
//
// It uses the hour-angle method: a fixed position of date reaches hour angle
// H at a time linear in the sidereal time, so each event is solved directly.
// The position is precessed once, to the middle of the day; it moves well
// under an arcsecond in a day, so no refinement pass is needed. A sidereal
// day is about 4 minutes shorter than the calendar day, so an event can
// happen twice in it (the first is given) and, on a 23-hour DST day, not at
// all.
func RiseTransitSet(raDeg, decDeg, latDeg, lonDeg float64, day time.Time) RTS {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.AddDate(0, 0, 1)
	ra, dec := precess(raDeg, decDeg, daysJ2000(start.Add(end.Sub(start)/2))/36525)

	// at is the first time from start the hour angle is ha (degrees), or
	// zero when that is not before end.
	at := func(ha float64) time.Time {
		d := math.Mod(ha-(lst(start, lonDeg)-ra), 360)
		if d < 0 {
			d += 360
		}
		t := start.Add(time.Duration(d / siderealRate * float64(24*time.Hour)))
		if !t.Before(end) {
			return time.Time{}
		}

		return t
	}

	r := RTS{Transit: at(0)}
	// Infinite at the poles, which the comparisons below still handle.
	cosH0 := (math.Sin(starHorizon*deg) - math.Sin(latDeg*deg)*math.Sin(dec*deg)) /
		(math.Cos(latDeg*deg) * math.Cos(dec*deg))
	switch {
	case cosH0 < -1:
		r.Circumpolar = true
	case cosH0 > 1:
		r.NeverRises = true
	default:
		h0 := math.Acos(cosH0) / deg
		r.Rise, r.Set = at(-h0), at(h0)
	}

	return r
}
