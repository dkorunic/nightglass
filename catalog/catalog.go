// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Package catalog serves the astro-recommender target lists. They ship as
// packed gob files generated from targets/*.yaml by TestEmbeddedLists
// (gen_test.go); the YAML and its parser never reach the binary.
package catalog

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"sync"
)

// Target is one catalog entry. RADeg and DecDeg are J2000 degrees. Size and
// Minor are arc minutes, Mag the V magnitude; zero or negative means unknown
// (the lists use -9999 for some).
type Target struct {
	Name, Description, Type, Constellation string
	RADeg, DecDeg, Size, Minor, Mag        float64
}

// packed is the gob form of one list: every target's strings in one blob, in
// field order, each target holding their lengths. Gob gives every decoded
// string its own allocation; one blob per list makes that one allocation.
type packed struct {
	Strings string
	Targets []packedTarget
}

type packedTarget struct {
	Name, Description, Type, Constellation uint32
	RADeg, DecDeg, Size, Minor, Mag        float64
}

var errPacked = errors.New("string lengths do not cover the blob")

// Set is every embedded list, decoded.
type Set struct {
	byName map[string][]Target
	// byKey is each Key's first target in Lists order: Find scanned every
	// list instead (3.8 ms and 131 thousand allocations for a miss).
	byKey map[string]found
	names []string
}

// found is where a target is: its list and index there.
type found struct {
	list string
	i    int
}

// Lists returns the list names, case-insensitively sorted.
func (s *Set) Lists() []string { return s.names }

// Get returns the targets of list name, nil for an unknown name.
func (s *Set) Get(name string) []Target { return s.byName[name] }

// Key normalizes a designation for matching, so "M 42", "m42" and "M42"
// agree.
func Key(name string) string { return strings.ToLower(strings.Join(strings.Fields(name), "")) }

// Find returns the first target named name (compared by Key), searching the
// lists in Lists order, and the list it came from.
func (s *Set) Find(name string) (Target, string, bool) {
	f, ok := s.byKey[Key(name)]
	if !ok {
		return Target{}, "", false
	}

	return s.byName[f.list][f.i], f.list, true
}

// Load decodes the embedded lists once; later calls return the same Set.
func Load() (*Set, error) { return loaded() }

var loaded = sync.OnceValues(func() (*Set, error) {
	entries, err := files.ReadDir("targets")
	if err != nil {
		return nil, err
	}
	s := &Set{byName: map[string][]Target{}}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".gob")
		if !ok {
			continue
		}
		f, err := files.Open(path.Join("targets", e.Name()))
		if err != nil {
			return nil, err
		}
		ts, err := decode(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("catalog %s: %w", name, err)
		}
		s.names = append(s.names, name)
		s.byName[name] = ts
	}
	slices.SortFunc(s.names, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	s.byKey = map[string]found{}
	for _, l := range s.names {
		for i, t := range s.byName[l] {
			if _, dup := s.byKey[Key(t.Name)]; !dup {
				s.byKey[Key(t.Name)] = found{l, i}
			}
		}
	}

	return s, nil
})

// Notice returns targets/NOTICE.md: the source commit, license and credits.
func Notice() string {
	b, _ := files.ReadFile("targets/NOTICE.md")

	return string(b)
}

// decode reads one packed list. The lengths must cover the blob exactly:
// walking it with wrong lengths would misalign every name after the first
// error instead of failing.
func decode(r io.Reader) ([]Target, error) {
	var p packed
	if err := gob.NewDecoder(r).Decode(&p); err != nil {
		return nil, err
	}
	var total uint64
	for _, pt := range p.Targets {
		total += uint64(pt.Name) + uint64(pt.Description) + uint64(pt.Type) + uint64(pt.Constellation)
	}
	if total != uint64(len(p.Strings)) {
		return nil, fmt.Errorf("%w: %d of %d bytes", errPacked, total, len(p.Strings))
	}
	strs := p.Strings
	next := func(n uint32) string {
		s := strs[:n]
		strs = strs[n:]

		return s
	}
	ts := make([]Target, len(p.Targets))
	for i, pt := range p.Targets {
		ts[i] = Target{
			Name: next(pt.Name), Description: next(pt.Description),
			Type: next(pt.Type), Constellation: next(pt.Constellation),
			RADeg: pt.RADeg, DecDeg: pt.DecDeg, Size: pt.Size, Minor: pt.Minor, Mag: pt.Mag,
		}
	}

	return ts, nil
}

// KnownMag reports whether a list magnitude is known: the lists write 0 or
// -9999 for unknown, while bright stars are rightly negative (the app sends
// any finite magnitude).
func KnownMag(m float64) bool { return m != 0 && m > -100 }
