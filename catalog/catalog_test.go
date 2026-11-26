// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"bytes"
	"encoding/gob"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestLoadAllLists(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.Lists()); n != 34 {
		t.Fatalf("got %d lists, want 34", n)
	}
	total := 0
	for _, l := range s.Lists() {
		total += len(s.Get(l))
	}
	if total != 44020 {
		t.Errorf("got %d entries, want 44020", total)
	}
	if s.Get("NoSuchList") != nil {
		t.Error("unknown list should be nil")
	}
	if !strings.Contains(Notice(), "MIT License") {
		t.Error("NOTICE.md not embedded")
	}
}

func TestMessierValues(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Target{}
	for _, tg := range s.Get("Messier") {
		byName[tg.Name] = tg
	}
	m1 := byName["M 1"]
	if m1.Description != "NGC 1952 Crab Nebula" || m1.Type != "Supernova Remnant" || m1.Constellation != "Taurus" {
		t.Errorf("M 1 strings misaligned: %+v", m1)
	}
	if math.Abs(m1.RADeg-(5+34.0/60+30.0/3600)*15) > 1e-9 || math.Abs(m1.DecDeg-(22+1.0/60)) > 1e-9 {
		t.Errorf("M 1 position %v %v", m1.RADeg, m1.DecDeg)
	}
	if m2 := byName["M 2"]; math.Abs(m2.DecDeg-(-49.0/60)) > 1e-9 {
		t.Errorf("M 2 dec %v, want -0.8167 (sign of -0 49 0 lost)", m2.DecDeg)
	}
}

func TestFind(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tg, _, ok := s.Find("m42")
	if !ok || tg.Name != "M 42" || math.Abs(tg.RADeg-83.82) > 0.5 || math.Abs(tg.DecDeg+5.39) > 0.5 {
		t.Errorf("Find(m42) = %+v %v", tg, ok)
	}
	if _, _, ok := s.Find("No Such Object"); ok {
		t.Error("found a nonexistent target")
	}
	if Key(" NGC  1976 ") != "ngc1976" {
		t.Errorf("Key = %q", Key(" NGC  1976 "))
	}
}

func TestDecodeRejectsBadLengths(t *testing.T) {
	var buf bytes.Buffer
	p := packed{Strings: "M 1Crab", Targets: []packedTarget{{Name: 3, Description: 5}}}
	if err := gob.NewEncoder(&buf).Encode(p); err != nil {
		t.Fatal(err)
	}
	if _, err := decode(&buf); !errors.Is(err, errPacked) {
		t.Fatalf("decode with lengths 8 for a 7-byte blob: err %v, want errPacked", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []Target{
		{Name: "M 42", Description: "NGC 1976 Great Orion Nebula", Type: "Nebula", RADeg: 83.85, DecDeg: -5.45, Size: 85, Mag: 4},
		{Name: "X", RADeg: 1, DecDeg: 2},
	}
	var buf bytes.Buffer
	if err := encode(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("round trip %d: %+v != %+v", i, out[i], in[i])
		}
	}
}
