// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import "testing"

func TestCountryCode(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{
		{45.815, 15.982, "HR"}, // Zagreb
		{40.71, -74.01, "US"},  // New York
		{-33.87, 151.21, "AU"}, // Sydney
		{35.68, 139.69, "JP"},  // Tokyo
		{51.51, -0.13, "GB"},   // London
		{-22.91, -43.17, "BR"}, // Rio de Janeiro
		{64.15, -21.94, "IS"},  // Reykjavík
		{33.94, -118.41, "US"}, // Los Angeles
		{48.86, 2.35, "FR"},    // Paris
		{-41.29, 174.78, "NZ"}, // Wellington
	} {
		if got := CountryCode(c.lat, c.lon); got != c.want {
			t.Errorf("CountryCode(%v, %v) = %q, want %q", c.lat, c.lon, got, c.want)
		}
	}
}
