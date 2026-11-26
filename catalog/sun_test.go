// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"math"
	"testing"
	"time"
)

func TestSunRADec(t *testing.T) {
	// The March equinox of 2026 (20 March, 14:46 UTC): RA and Dec about 0.
	ra, dec := SunRADec(time.Date(2026, 3, 20, 14, 46, 0, 0, time.UTC))
	if math.Abs(math.Remainder(ra, 360)) > 0.05 || math.Abs(dec) > 0.05 {
		t.Errorf("equinox sun at %v %v", ra, dec)
	}
	// The June solstice: Dec +23.44.
	if _, dec := SunRADec(time.Date(2026, 6, 21, 8, 24, 0, 0, time.UTC)); math.Abs(dec-23.44) > 0.02 {
		t.Errorf("solstice declination %v", dec)
	}
}

func TestImagingStart(t *testing.T) {
	zagreb, lat, lon := time.FixedZone("CET", 3600), 45.815, 15.982
	noon := time.Date(2026, 1, 15, 12, 0, 0, 0, zagreb)
	// M 42 in January: up in the evening, from sunset (about 16:40) on.
	w := ImagingStart(83.82, -5.39, 10, noon, lat, lon)
	if !w.Visible || w.Min.Hour() < 16 || w.Min.Hour() > 19 || w.Dawn.Hour() != 7 || !w.Dawn.After(w.Min) {
		t.Errorf("M 42: %v", w)
	}
	if alt, _ := AltAz(83.82, -5.39, w.Min, lat, lon); alt < 10 {
		t.Errorf("M 42 at %v at the minimum start", alt)
	}
	// Polaris never rises or sets here: from sunset.
	if w := ImagingStart(37.95, 89.26, 10, noon, lat, lon); !w.Visible || w.Min.Hour() != 16 {
		t.Errorf("Polaris: %v", w)
	}
	// Far south, never 10° up from Zagreb.
	if w := ImagingStart(0, -80, 10, noon, lat, lon); w.Visible || !w.Min.Equal(noon) {
		t.Errorf("far south: %v", w)
	}
}
