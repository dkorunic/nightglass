// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"regexp"
	"strings"
)

// designation is a catalogue designation at the start of a text: letters,
// then a number ("NGC 224", "M31", "Sh2-275", "vdB 34", "IC 1396A").
var designation = regexp.MustCompile(`^[A-Za-z]{1,8}\d?[ -]?\d[\w.+-]*\s*`)

// CommonName is a target's common name from its description, "" when it
// has none: the first comma-separated part left once its leading
// designations are taken off ("NGC 224 Andromeda Galaxy" and "NGC 7023,
// Iris Nebula" give "Andromeda Galaxy" and "Iris Nebula").
func CommonName(t Target) string {
	for part := range strings.SplitSeq(t.Description, ",") {
		p := strings.TrimSpace(part)
		for {
			loc := designation.FindStringIndex(p)
			if loc == nil {
				break
			}
			p = strings.TrimSpace(p[loc[1]:])
		}
		if strings.ContainsFunc(p, func(r rune) bool { return r >= 'A' && r <= 'z' }) {
			return p
		}
	}

	return ""
}

// StyledName is the app's name style "common - catalogue"
// (kObjectNameStyleCommonThenCatalog): "Andromeda Galaxy - M 31", or the
// designation alone when there is no common name.
func StyledName(t Target) string {
	if c := CommonName(t); c != "" && c != t.Name {
		return c + " - " + t.Name
	}

	return t.Name
}
