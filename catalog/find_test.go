// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"strings"
	"testing"
)

// findRef is the list scan Find replaced, kept as its oracle.
func findRef(s *Set, name string) (Target, string, bool) {
	k := Key(name)
	for _, l := range s.names {
		for _, t := range s.byName[l] {
			if Key(t.Name) == k {
				return t, l, true
			}
		}
	}

	return Target{}, "", false
}

// Find answers as the scan does for every name in the lists (including
// names in several lists, where the first list wins), spelling variants and
// misses.
func TestFindMatchesReference(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"", " ", "no such object", "M42", "m 42", " M  31 ", "NGC224", "ngc 7000", "Sh2-275", "IC 1396A"}
	// The scan takes milliseconds per name: every name in more than one
	// list (where list order decides) and a spread of the rest.
	lists := map[string]int{}
	for _, l := range s.Lists() {
		for _, x := range s.Get(l) {
			lists[Key(x.Name)]++
		}
	}
	i, dups := 0, 0
	for _, l := range s.Lists() {
		for _, x := range s.Get(l) {
			if i++; (lists[Key(x.Name)] > 1 && dups < 100) || i%1500 == 0 {
				dups += min(lists[Key(x.Name)]-1, 1)
				names = append(names, x.Name, strings.ToLower(x.Name), strings.ReplaceAll(x.Name, " ", ""), x.Name+"x")
			}
		}
	}
	for _, n := range names {
		gt, gl, gok := s.Find(n)
		wt, wl, wok := findRef(s, n)
		if gt != wt || gl != wl || gok != wok {
			t.Fatalf("Find(%q) = %v %q %v, reference %v %q %v", n, gt, gl, gok, wt, wl, wok)
		}
	}
}
