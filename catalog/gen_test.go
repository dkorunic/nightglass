// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package catalog

import (
	"bytes"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

var update = flag.Bool("update", false, "regenerate targets/*.gob from targets/*.yaml")

// TestEmbeddedLists regenerates every list from its YAML and fails when the
// committed gob differs; -update writes them instead. CI runs it without
// -update, so a YAML re-copy without regenerating cannot ship.
func TestEmbeddedLists(t *testing.T) {
	yamls, err := filepath.Glob("targets/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(yamls) != 34 {
		t.Fatalf("found %d YAML lists, want 34", len(yamls))
	}
	for _, y := range yamls {
		ts, err := loadYAML(y)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := encode(&buf, ts); err != nil {
			t.Fatal(err)
		}
		g := strings.TrimSuffix(y, ".yaml") + ".gob"
		if *update {
			if err := os.WriteFile(g, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}

			continue
		}
		got, err := os.ReadFile(g)
		if err != nil || !bytes.Equal(got, buf.Bytes()) {
			t.Errorf("%s is stale or missing; run: go test ./catalog -run TestEmbeddedLists -args -update", g)
		}
	}
}

// yamlTarget holds the YAML keys the app keeps; the others (bmag, surfbr,
// bsurfbr, haflux, rating) are dropped here.
type yamlTarget struct {
	Name          string  `yaml:"name"`
	Description   string  `yaml:"description"`
	Type          string  `yaml:"type"`
	Constellation string  `yaml:"constellation"`
	RA            string  `yaml:"ra"`
	Dec           string  `yaml:"dec"`
	Size          float64 `yaml:"size"`
	Minor         float64 `yaml:"minor"`
	Mag           float64 `yaml:"mag"`
}

// loadYAML reads one list. A row with a bad coordinate fails the whole
// generation, naming the file and entry.
func loadYAML(file string) ([]Target, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var rows []yamlTarget
	if err := yaml.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	ts := make([]Target, len(rows))
	for i, r := range rows {
		ra, err := sexagesimal(r.RA)
		if err == nil && (ra < 0 || ra >= 24) {
			err = errors.New("RA must be 0 to 24h")
		}
		if err != nil {
			return nil, fmt.Errorf("%s entry %d (%s): ra: %w", file, i, r.Name, err)
		}
		dec, err := sexagesimal(r.Dec)
		if err == nil && math.Abs(dec) > 90 {
			err = errors.New("Dec must be within ±90°")
		}
		if err != nil {
			return nil, fmt.Errorf("%s entry %d (%s): dec: %w", file, i, r.Name, err)
		}
		ts[i] = Target{
			Name: r.Name, Description: r.Description, Type: r.Type, Constellation: r.Constellation,
			RADeg: ra * 15, DecDeg: dec, Size: r.Size, Minor: r.Minor, Mag: r.Mag,
		}
	}

	return ts, nil
}

// encode is decode's inverse.
func encode(w *bytes.Buffer, ts []Target) error {
	var blob strings.Builder
	p := packed{Targets: make([]packedTarget, len(ts))}
	for i, t := range ts {
		blob.WriteString(t.Name)
		blob.WriteString(t.Description)
		blob.WriteString(t.Type)
		blob.WriteString(t.Constellation)
		p.Targets[i] = packedTarget{
			Name: uint32(len(t.Name)), Description: uint32(len(t.Description)),
			Type: uint32(len(t.Type)), Constellation: uint32(len(t.Constellation)),
			RADeg: t.RADeg, DecDeg: t.DecDeg, Size: t.Size, Minor: t.Minor, Mag: t.Mag,
		}
	}
	p.Strings = blob.String()

	return gob.NewEncoder(w).Encode(p)
}

var (
	errFields = errors.New("want 1 to 3 fields")
	errRange  = errors.New("fields must not be negative, minutes and seconds below 60")
	errSign   = errors.New("more than one sign")
	errDigits = errors.New("fields must be decimal numbers, a fraction only in the last")
)

// sexagesimal parses "[+-]a b c" into a + b/60 + c/3600 with the sign applied.
// The sign comes from the text, so "-0 49 0" is -0.8167, not +0.8167.
// Copied from github.com/dkorunic/astro-recommender internal/astro.Sexagesimal (MIT).
func sexagesimal(text string) (float64, error) {
	s := strings.TrimSpace(text)
	sign := 1.0
	if strings.HasPrefix(s, "-") {
		sign = -1
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, fmt.Errorf("%w: %q", errSign, text)
	}
	var fields [3]string
	n := 0
	for f := range strings.FieldsSeq(s) {
		if n == len(fields) {
			return 0, fmt.Errorf("%w: %q", errFields, text)
		}
		fields[n] = f
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: %q", errFields, text)
	}
	var v float64
	for i, f := range fields[:n] {
		if f[0] == '-' || f[0] == '+' {
			return 0, fmt.Errorf("%w: %q", errSign, text)
		}
		if strings.Trim(f, "0123456789.") != "" || strings.Count(f, ".") > 1 || i < n-1 && strings.Contains(f, ".") {
			return 0, fmt.Errorf("%w: %q", errDigits, text)
		}
		x, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return 0, fmt.Errorf("%q: %w", text, err)
		}
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 || i > 0 && x >= 60 {
			return 0, fmt.Errorf("%w: %q", errRange, text)
		}
		v += x / math.Pow(60, float64(i))
	}

	return sign * v, nil
}

func TestSexagesimal(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
	}{
		{"5 34 30", 5 + 34.0/60 + 30.0/3600},
		{"21 33 30.4", 21 + 33.0/60 + 30.4/3600},
		{"0 42.7", 42.7 / 60},
		{"+22 1 0", 22 + 1.0/60},
		{"-0 49 0", -(49.0 / 60)},
		{"41 16 9", 41 + 16.0/60 + 9.0/3600},
	} {
		got, err := sexagesimal(c.in)
		if err != nil || math.Abs(got-c.want) > 1e-12 {
			t.Errorf("sexagesimal(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "1 2 3 4", "+-1 0", "12.5 30", "1 60", "1e2 0", "+ -0 30"} {
		if _, err := sexagesimal(bad); err == nil {
			t.Errorf("sexagesimal(%q): want error", bad)
		}
	}
}
