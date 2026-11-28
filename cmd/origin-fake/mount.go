// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin/fake"
)

// gotoSpeed is the simulated go-to speed in degrees per second.
const gotoSpeed = 6.0

// slewSpeeds are the manual speeds for the Origin's rates 1–9 in degrees
// per second, after the usual Celestron hand-control scale (2× to 32×
// sidereal, then 0.5, 1, 2 and 4°/s): the Origin's own are not known.
var slewSpeeds = map[int]float64{
	1: 2 * sidereal, 2: 4 * sidereal, 3: 8 * sidereal, 4: 16 * sidereal, 5: 32 * sidereal,
	6: 0.5, 7: 1, 8: 2, 9: 4,
}

const sidereal = 360.0 / 86164.0905 // degrees per second

// mount simulates the mount: go-tos at gotoSpeed, manual slews by axis
// rate, and the turning sky: while tracking RA/Dec stay and Alt/Az follow,
// otherwise Alt/Az stay and the sky drifts past.
type mount struct {
	at               time.Time // when the position was last advanced
	now              func() time.Time
	s                *fake.Server
	target           *[2]float64 // go-to RA/Dec (J2000 degrees), nil when none
	ra, dec          float64     // J2000 degrees
	alt, az          float64     // degrees, azimuth from north through east
	lat, lon         float64     // the observer, degrees east positive
	altRate, azmRate float64     // manual slew, degrees per second
	mu               sync.Mutex
	tracking         bool
}

// newMount points the mount south at 45° for an observer at lat, lon.
func newMount(s *fake.Server, lat, lon float64, now func() time.Time) *mount {
	m := &mount{s: s, lat: lat, lon: lon, now: now, alt: 45, az: 180, at: now()}
	m.ra, m.dec = equ(m.alt, m.az, m.at, lat, lon)
	m.publish(false)

	return m
}

// equ is the RA/Dec of date (degrees) at alt, az: close enough to J2000
// for offsets, which is all the mount takes from it.
func equ(alt, az float64, t time.Time, lat, lon float64) (float64, float64) {
	const d = math.Pi / 180
	sinAlt, cosAlt := math.Sincos(alt * d)
	sinAz, cosAz := math.Sincos(az * d)
	sinLat, cosLat := math.Sincos(lat * d)
	dec := math.Asin(max(-1, min(1, sinAlt*sinLat+cosAlt*cosLat*cosAz)))
	ha := math.Atan2(-sinAz*cosAlt, sinAlt*cosLat-cosAlt*sinLat*cosAz)
	lst := 280.46061837 + 360.98564736629*(float64(t.Unix())/86400-10957.5) + lon

	return math.Mod(math.Mod(lst-ha/d, 360)+360, 360), dec / d
}

// moveTo shifts RA/Dec by how far the sky at alt, az moved: from the old
// horizon position at the last step to the new one now.
func (m *mount) moveTo(alt, az float64, now time.Time) {
	r0, d0 := equ(m.alt, m.az, m.at, m.lat, m.lon)
	r1, d1 := equ(alt, az, now, m.lat, m.lon)
	m.ra = math.Mod(m.ra+wrap(r1-r0)+360, 360)
	m.dec = max(-90, min(90, m.dec+d1-d0))
	m.alt, m.az = alt, az
}

// wrap brings an angle difference into (-180, 180].
func wrap(d float64) float64 { return d - 360*math.Round(d/360) }

// step advances the mount to now.
func (m *mount) step() {
	m.mu.Lock()
	now := m.now()
	dt := now.Sub(m.at).Seconds()
	arrived := false
	switch {
	case m.target != nil:
		dra, ddec := wrap(m.target[0]-m.ra)*math.Cos(m.dec*math.Pi/180), m.target[1]-m.dec
		if dist := math.Hypot(dra, ddec); dist <= gotoSpeed*dt {
			m.ra, m.dec, m.target, arrived = m.target[0], m.target[1], nil, true
		} else {
			f := gotoSpeed * dt / dist
			m.ra = math.Mod(m.ra+f*wrap(m.target[0]-m.ra)+360, 360)
			m.dec += f * ddec
		}
		m.alt, m.az = catalog.AltAz(m.ra, m.dec, now, m.lat, m.lon)
	case m.altRate != 0 || m.azmRate != 0:
		m.moveTo(max(-10, min(90, m.alt+m.altRate*dt)), math.Mod(m.az+m.azmRate*dt+360, 360), now)
	case m.tracking:
		m.alt, m.az = catalog.AltAz(m.ra, m.dec, now, m.lat, m.lon)
	default:
		m.moveTo(m.alt, m.az, now)
	}
	m.at = now
	m.mu.Unlock()
	m.publish(arrived)
}

// publish sets the Mount status, notifying when a go-to ends. Date and Time
// are the mount's clock in the observer's time zone, in the format
// RunInitialize sends them.
func (m *mount) publish(notify bool) {
	const d = math.Pi / 180
	loc, err := time.LoadLocation(fmt.Sprint(m.s.Status("Mount")["TimeZone"]))
	if err != nil {
		loc = time.UTC
	}
	m.mu.Lock()
	now := m.now().In(loc)
	st := fake.Msg{
		"Ra": m.ra * d, "Dec": m.dec * d, "Alt": m.alt * d, "Azm": m.az * d,
		"IsGotoOver": m.target == nil, "IsTracking": m.tracking,
		"Date": now.Format("02 01 2006"), "Time": now.Format("15:04:05"),
	}
	m.mu.Unlock()
	m.s.SetStatus("Mount", st)
	if notify {
		m.s.NotifyStatus("Mount")
	}
}

// run advances the mount every iv until ctx ends.
func (m *mount) run(ctx context.Context, iv time.Duration) {
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.step()
		}
	}
}

// gotoRaDec starts a go-to (Mount/GotoRaDec, radians).
func (m *mount) gotoRaDec(raRad, decRad float64) {
	m.step()
	m.mu.Lock()
	m.target = &[2]float64{math.Mod(raRad*180/math.Pi+360, 360), decRad * 180 / math.Pi}
	m.altRate, m.azmRate = 0, 0
	m.mu.Unlock()
	m.publish(true)
}

// slew sets the manual rates (Mount/Slew); 0, 0 stops. Origin firmware
// 1.5.6286 has no Mount/AbortAxisMovement, so Slew is the only stop.
func (m *mount) slew(altRate, azmRate int) {
	sign := func(r int) float64 {
		if r < 0 {
			return -slewSpeeds[-r]
		}

		return slewSpeeds[r]
	}
	m.step()
	m.mu.Lock()
	m.altRate, m.azmRate = sign(altRate), sign(azmRate)
	if altRate != 0 || azmRate != 0 {
		m.target = nil
	}
	m.mu.Unlock()
	m.publish(true)
}

// site takes RunInitialize's observer (radians).
func (m *mount) site(latRad, lonRad any) {
	m.step()
	m.mu.Lock()
	if v, ok := latRad.(float64); ok {
		m.lat = v * 180 / math.Pi
	}
	if v, ok := lonRad.(float64); ok {
		m.lon = v * 180 / math.Pi
	}
	m.mu.Unlock()
}

// setTracking starts tracking (as an initialized Origin does, or
// Mount/EnableTracking) or stops it.
func (m *mount) setTracking(on bool) {
	m.step()
	m.mu.Lock()
	m.tracking = on
	m.mu.Unlock()
	m.publish(true)
}

// moving is whether a go-to is under way.
func (m *mount) moving() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.target != nil
}

// pointing is where the mount points, J2000 degrees.
func (m *mount) pointing() (float64, float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ra, m.dec
}
