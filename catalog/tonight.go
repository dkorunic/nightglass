// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"time"
)

// Night is the app's Tonight → Sun & Moon card (TonightListRowSunMoonVH)
// for the night after the most recent local noon: when the Sun sets,
// astronomical twilight ends and begins, the Sun rises, and when the Moon
// rises and sets. A zero time is an event that does not happen in the 24
// hours from that noon ("Does not rise", or none under the midnight sun).
// MoonIllum is the illuminated fraction (0..1) and MoonAge the days since
// the last new moon, both at now.
type Night struct {
	SunSet, AstroDusk, AstroDawn, SunRise time.Time
	MoonRise, MoonSet                     time.Time
	MoonPhase                             string
	MoonIllum, MoonAge                    float64
}

// horizon is where the Sun's and the Moon's upper limb touches the
// horizon, refraction included, for the centre's altitude; the Moon's
// position is topocentric, so the same value holds for it.
const horizon = -0.833

// phases are the app's moon phase names (its native library's), each 45°
// of elongation wide around new, first quarter, full and last quarter.
var phases = [8]string{
	"New Moon", "Waxing Crescent", "First Quarter", "Waxing Gibbous",
	"Full Moon", "Waning Gibbous", "Last Quarter", "Waning Crescent",
}

// Tonight returns the Night for an observer at lat, lon (degrees, east
// positive), its noon taken in now's location.
// ponytail: the app has these from its sky chart (getBasicPlanetInfo);
// this minute-by-minute search is within a minute or two of it.
func Tonight(now time.Time, lat, lon float64) Night {
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	if now.Before(noon) {
		noon = noon.AddDate(0, 0, -1)
	}
	sunAlt := func(t time.Time) float64 {
		ra, dec := sunJ2000(t)
		alt, _ := AltAz(ra, dec, t, lat, lon)

		return alt
	}
	moonAlt := func(t time.Time) float64 {
		ra, dec, _, _ := Body("Moon", t, lat, lon)
		alt, _ := AltAz(ra, dec, t, lat, lon)

		return alt
	}
	n := Night{
		SunSet:    crossing(sunAlt, horizon, noon, false),
		AstroDusk: crossing(sunAlt, -18, noon, false),
		AstroDawn: crossing(sunAlt, -18, noon, true),
		SunRise:   crossing(sunAlt, horizon, noon, true),
		MoonRise:  crossing(moonAlt, horizon, noon, true),
		MoonSet:   crossing(moonAlt, horizon, noon, false),
	}
	e := elongation(now)
	mlon, mlat := moonEcliptic(now)
	n.MoonIllum = (1 - cosd(mlat)*cosd(mlon-sunLon(now))) / 2
	n.MoonPhase = phases[int(math.Mod(e+22.5, 360)/45)]
	n.MoonAge = now.Sub(lastNewMoon(now)).Hours() / 24

	return n
}

// crossing is the first minute within a day from start that alt(t) goes
// above limit (rising) or below it (setting), else the zero time.
func crossing(alt func(time.Time) float64, limit float64, start time.Time, rising bool) time.Time {
	above := alt(start) > limit
	for t := start.Add(time.Minute); t.Before(start.Add(24 * time.Hour)); t = t.Add(time.Minute) {
		now := alt(t) > limit
		if now != above && now == rising {
			return t
		}
		above = now
	}

	return time.Time{}
}

// sunLon is the Sun's geocentric ecliptic longitude of date (degrees).
func sunLon(t time.Time) float64 {
	lon, _, _ := orbit("Sun", daysJ2000(t)+1.5).ecliptic()

	return math.Mod(lon+360, 360)
}

// sunJ2000 is the Sun's J2000 RA and Dec (degrees), for AltAz.
func sunJ2000(t time.Time) (float64, float64) {
	d := daysJ2000(t) + 1.5

	return radec(toJ2000(sunLon(t), 0, d))
}

// moonEcliptic is the Moon's geocentric ecliptic longitude and latitude
// of date (degrees).
func moonEcliptic(t time.Time) (lon, lat float64) {
	lon, lat, _, _ = geocentric("Moon", daysJ2000(t)+1.5)

	return lon, lat
}

// elongation is the Moon's ecliptic longitude east of the Sun's, 0..360°:
// 0 at new moon, 180 at full.
func elongation(t time.Time) float64 {
	mlon, _ := moonEcliptic(t)

	return math.Mod(mlon-sunLon(t)+360, 360)
}

// lastNewMoon is the new moon before t, to the minute: back in 6-hour
// steps until the elongation wraps past 0, then halving.
func lastNewMoon(t time.Time) time.Time {
	hi := t
	lo := hi.Add(-6 * time.Hour)
	for elongation(lo) < elongation(hi) && t.Sub(lo) < 31*24*time.Hour {
		hi, lo = lo, lo.Add(-6*time.Hour)
	}
	for hi.Sub(lo) > time.Minute {
		mid := lo.Add(hi.Sub(lo) / 2)
		if elongation(mid) < elongation(hi) {
			hi = mid
		} else {
			lo = mid
		}
	}

	return hi
}
