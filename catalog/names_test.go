// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import "testing"

func TestCommonName(t *testing.T) {
	for desc, want := range map[string]string{
		"NGC 224 Andromeda Galaxy":                    "Andromeda Galaxy",
		"NGC 7023, Iris Nebula":                       "Iris Nebula",
		"Flaming Star Nebula, IC 405, vdB 34, AE Aur": "Flaming Star Nebula",
		"NGC 7089":         "",
		"Sh2-275, LBN 948": "",
		"":                 "",
		"Andromeda Galaxy": "Andromeda Galaxy",
	} {
		if got := CommonName(Target{Description: desc}); got != want {
			t.Errorf("CommonName(%q) = %q, want %q", desc, got, want)
		}
	}
	if got := StyledName(Target{Name: "M 31", Description: "NGC 224 Andromeda Galaxy"}); got != "Andromeda Galaxy - M 31" {
		t.Errorf("StyledName = %q", got)
	}
	if got := StyledName(Target{Name: "M 2", Description: "NGC 7089"}); got != "M 2" {
		t.Errorf("StyledName = %q", got)
	}
}
