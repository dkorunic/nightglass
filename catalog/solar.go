// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// SolarSystem is the pseudo-list of the Moon and planets, whose positions
// are computed for a time instead of read from a catalog.
const SolarSystem = "Solar System"

// Bodies are the SolarSystem targets, in the order they are listed. The Sun
// is left out: the Origin must never point at it.
var Bodies = []string{"Moon", "Mercury", "Venus", "Mars", "Jupiter", "Saturn", "Uranus", "Neptune"}

// elements are mean orbital elements at d days from 2000 Jan 0.0 UT, in
// degrees, AU (Earth radii for the Moon), after Paul Schlyter's "How to
// compute planetary positions": arc-minute accuracy, plenty for a telescope
// that plate-solves and centers.
type elements struct{ N, i, w, a, e, M float64 }

func orbit(body string, d float64) elements {
	switch body {
	case "Sun":
		return elements{0, 0, 282.9404 + 4.70935e-5*d, 1, 0.016709 - 1.151e-9*d, 356.0470 + 0.9856002585*d}
	case "Moon":
		return elements{125.1228 - 0.0529538083*d, 5.1454, 318.0634 + 0.1643573223*d, 60.2666, 0.054900, 115.3654 + 13.0649929509*d}
	case "Mercury":
		return elements{48.3313 + 3.24587e-5*d, 7.0047 + 5.00e-8*d, 29.1241 + 1.01444e-5*d, 0.387098, 0.205635 + 5.59e-10*d, 168.6562 + 4.0923344368*d}
	case "Venus":
		return elements{76.6799 + 2.46590e-5*d, 3.3946 + 2.75e-8*d, 54.8910 + 1.38374e-5*d, 0.723330, 0.006773 - 1.302e-9*d, 48.0052 + 1.6021302244*d}
	case "Mars":
		return elements{49.5574 + 2.11081e-5*d, 1.8497 - 1.78e-8*d, 286.5016 + 2.92961e-5*d, 1.523688, 0.093405 + 2.516e-9*d, 18.6021 + 0.5240207766*d}
	case "Jupiter":
		return elements{100.4542 + 2.76854e-5*d, 1.3030 - 1.557e-7*d, 273.8777 + 1.64505e-5*d, 5.20256, 0.048498 + 4.469e-9*d, 19.8950 + 0.0830853001*d}
	case "Saturn":
		return elements{113.6634 + 2.38980e-5*d, 2.4886 - 1.081e-7*d, 339.3939 + 2.97661e-5*d, 9.55475, 0.055546 - 9.499e-9*d, 316.9670 + 0.0334442282*d}
	case "Uranus":
		return elements{74.0005 + 1.3978e-5*d, 0.7733 + 1.9e-8*d, 96.6612 + 3.0565e-5*d, 19.18171 - 1.55e-8*d, 0.047318 + 7.45e-9*d, 142.5905 + 0.011725806*d}
	case "Neptune":
		return elements{131.7806 + 3.0173e-5*d, 1.7700 - 2.55e-7*d, 272.8461 - 6.027e-6*d, 30.05826 + 3.313e-8*d, 0.008606 + 2.15e-9*d, 260.2471 + 0.005995147*d}
	}

	return elements{}
}

// ecliptic returns the body's position in its orbit's frame: ecliptic
// longitude and latitude (degrees, equinox of date) and distance from the
// centre of that orbit.
func (o elements) ecliptic() (lon, lat, r float64) {
	M := o.M * deg
	E := M + o.e*math.Sin(M)*(1+o.e*math.Cos(M))
	for range 10 { // Kepler's equation; the Moon's and Mercury's e need a few rounds
		E -= (E - o.e*math.Sin(E) - M) / (1 - o.e*math.Cos(E))
	}
	xv, yv := o.a*(math.Cos(E)-o.e), o.a*math.Sqrt(1-o.e*o.e)*math.Sin(E)
	v, r := math.Atan2(yv, xv), math.Hypot(xv, yv)
	sinN, cosN := math.Sincos(o.N * deg)
	sinVW, cosVW := math.Sincos(v + o.w*deg)
	sinI, cosI := math.Sincos(o.i * deg)
	x := r * (cosN*cosVW - sinN*sinVW*cosI)
	y := r * (sinN*cosVW + cosN*sinVW*cosI)
	z := r * sinVW * sinI

	return math.Atan2(y, x) / deg, math.Atan2(z, math.Hypot(x, y)) / deg, r
}

func sind(x float64) float64 { return math.Sin(x * deg) }
func cosd(x float64) float64 { return math.Cos(x * deg) }

// geocentric returns a body's geocentric ecliptic longitude and latitude
// (degrees, mean equinox of date), its distance (AU; Earth radii for the
// Moon) and V magnitude, d days from 2000 Jan 0.0 UT.
func geocentric(body string, d float64) (lon, lat, dist, mag float64) {
	sun := orbit("Sun", d)
	slon, _, rs := sun.ecliptic()
	if body == "Moon" {
		m := orbit("Moon", d)
		lon, lat, dist = m.ecliptic()
		Ms, Mm := sun.M, m.M
		Ls, Lm := sun.M+sun.w, m.M+m.w+m.N
		D, F := Lm-Ls, Lm-m.N
		lon += -1.274*sind(Mm-2*D) + 0.658*sind(2*D) - 0.186*sind(Ms) - 0.059*sind(2*Mm-2*D) -
			0.057*sind(Mm-2*D+Ms) + 0.053*sind(Mm+2*D) + 0.046*sind(2*D-Ms) + 0.041*sind(Mm-Ms) -
			0.035*sind(D) - 0.031*sind(Mm+Ms) - 0.015*sind(2*F-2*D) + 0.011*sind(Mm-4*D)
		lat += -0.173*sind(F-2*D) - 0.055*sind(Mm-F-2*D) - 0.046*sind(Mm+F-2*D) + 0.033*sind(F+2*D) + 0.017*sind(2*Mm+F)
		dist += -0.58*cosd(Mm-2*D) - 0.46*cosd(2*D)
		// Phase angle from the elongation; the Sun is as far from the Moon
		// as from the Earth. R in AU for the magnitude formula.
		fv := 180 - math.Acos(cosd(slon-lon)*cosd(lat))/deg
		R := dist * 6378.14 / 149597870.7
		mag = 0.23 + 5*math.Log10(rs*R) + 0.026*fv + 4.0e-9*math.Pow(fv, 4)

		return math.Mod(lon+360, 360), lat, dist, mag
	}
	hlon, hlat, r := orbit(body, d).ecliptic()
	// The large mutual perturbations of the outer planets.
	Mj, Ms, Mu := orbit("Jupiter", d).M, orbit("Saturn", d).M, orbit("Uranus", d).M
	switch body {
	case "Jupiter":
		hlon += -0.332*sind(2*Mj-5*Ms-67.6) - 0.056*sind(2*Mj-2*Ms+21) + 0.042*sind(3*Mj-5*Ms+21) -
			0.036*sind(Mj-2*Ms) + 0.022*cosd(Mj-Ms) + 0.023*sind(2*Mj-3*Ms+52) - 0.016*sind(Mj-5*Ms-69)
	case "Saturn":
		hlon += 0.812*sind(2*Mj-5*Ms-67.6) - 0.229*cosd(2*Mj-4*Ms-2) + 0.119*sind(Mj-2*Ms-3) +
			0.046*sind(2*Mj-6*Ms-69) + 0.014*sind(Mj-3*Ms+32)
		hlat += -0.020*cosd(2*Mj-4*Ms-2) + 0.018*sind(2*Mj-6*Ms-49)
	case "Uranus":
		hlon += 0.040*sind(Ms-2*Mu+6) + 0.035*sind(Ms-3*Mu+33) - 0.015*sind(Mj-Mu+20)
	}
	x := r*cosd(hlon)*cosd(hlat) + rs*cosd(slon)
	y := r*sind(hlon)*cosd(hlat) + rs*sind(slon)
	z := r * sind(hlat)
	lon, lat, dist = math.Atan2(y, x)/deg, math.Atan2(z, math.Hypot(x, y))/deg, math.Sqrt(x*x+y*y+z*z)
	fv := math.Acos(max(-1, min(1, (r*r+dist*dist-rs*rs)/(2*r*dist)))) / deg
	base := 5 * math.Log10(r*dist)
	switch body {
	case "Mercury":
		mag = -0.36 + base + 0.027*fv + 2.2e-13*math.Pow(fv, 6)
	case "Venus":
		mag = -4.34 + base + 0.013*fv + 4.2e-7*fv*fv*fv
	case "Mars":
		mag = -1.51 + base + 0.016*fv
	case "Jupiter":
		mag = -9.25 + base + 0.014*fv
	case "Saturn":
		// The rings: their tilt towards the Earth brightens Saturn.
		ir, nr := 28.06, 169.51+3.82e-5*d
		b := math.Asin(sind(lat)*cosd(ir)-cosd(lat)*sind(ir)*sind(lon-nr)) / deg
		mag = -9.0 + base + 0.044*fv - 2.6*math.Abs(sind(b)) + 1.2*sind(b)*sind(b)
	case "Uranus":
		mag = -7.15 + base + 0.001*fv
	case "Neptune":
		mag = -6.90 + base + 0.001*fv
	}

	return math.Mod(lon+360, 360), lat, dist, mag
}

// toJ2000 turns ecliptic coordinates of the equinox of date d (degrees)
// into a J2000 equatorial unit vector: precession in longitude, then the
// J2000 obliquity.
func toJ2000(lon, lat, d float64) [3]float64 {
	lon -= 3.82394e-5 * d
	const ecl = 23.4393
	x, y, z := cosd(lon)*cosd(lat), sind(lon)*cosd(lat), sind(lat)

	return [3]float64{x, y*cosd(ecl) - z*sind(ecl), y*sind(ecl) + z*cosd(ecl)}
}

func radec(v [3]float64) (float64, float64) {
	return math.Mod(math.Atan2(v[1], v[0])/deg+360, 360), math.Atan2(v[2], math.Hypot(v[0], v[1])) / deg
}

// Body returns a SolarSystem body's J2000 RA and Dec (degrees) and V
// magnitude at t, seen from lat, lon (degrees, east positive): the Moon is
// corrected for parallax, up to 1° for an observer away from the sub-lunar
// point. ok is false for a name not in Bodies.
func Body(name string, t time.Time, lat, lon float64) (ra, dec, mag float64, ok bool) {
	known := false
	for _, b := range Bodies {
		known = known || b == name
	}
	if !known {
		return 0, 0, 0, false
	}
	d := daysJ2000(t) + 1.5 // 2000 Jan 0.0 is 1.5 days before J2000.0
	elon, elat, dist, mag := geocentric(name, d)
	v := toJ2000(elon, elat, d)
	if name == "Moon" {
		// Subtract the observer's place on a spherical Earth (Earth radii),
		// from the sidereal time; flattening changes the parallax by <0.3 %.
		st := lst(t, lon)
		for k, o := range [3]float64{cosd(lat) * cosd(st), cosd(lat) * sind(st), sind(lat)} {
			v[k] = v[k]*dist - o
		}
	}
	ra, dec = radec(v)

	return ra, dec, mag, true
}

// JulianDate is t's Julian date (UT).
func JulianDate(t time.Time) float64 { return daysJ2000(t) + 2451545.0 }

// Track is the app's TargetTrack for imaging a Moon or planet from lat, lon:
// 16 places one second apart, starting 30 s after now.
func Track(name string, now time.Time, lat, lon float64) []origin.TrackPoint {
	return TrackSpan(name, now, lat, lon, 16, time.Second)
}

// TrackSpan is a TargetTrack of n places step apart, starting 30 s after now.
func TrackSpan(name string, now time.Time, lat, lon float64, n int, step time.Duration) []origin.TrackPoint {
	out := make([]origin.TrackPoint, n)
	for i := range out {
		t := now.Add(30*time.Second + time.Duration(i)*step)
		ra, dec, _, _ := Body(name, t, lat, lon)
		out[i] = origin.TrackPoint{RADeg: ra, DecDeg: dec, JD: JulianDate(t)}
	}

	return out
}
