// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"maps"
	"testing"

	"github.com/dkorunic/nightglass/origin/fake"
)

// Behaviour of Origin firmware 1.5.6286 the simulator follows (from the
// firmware and its own web app): busy refusals, a halted task in the status,
// progress blocks, moving-target tracking, cooling, the mount's clock.
func TestFirmwareBehaviour(t *testing.T) {
	m, s := quickSim(t)
	cmd := func(dest, c string, extra fake.Msg) fake.Msg {
		req := fake.Msg{"Destination": dest, "Command": c}
		maps.Copy(req, extra)

		return m.handle(req)
	}
	state := func() any { return s.Status("TaskController")["State"] }

	// A task refuses another, and a halt is reported until the next task.
	cmd("TaskController", "RunGenerateNewFlat", nil)
	if r := cmd("TaskController", "RunGenerateNewDarks", nil); r["ErrorCode"] != -18 {
		t.Errorf("a second task while flats run: %v", r["ErrorCode"])
	}
	if pct := s.Status("TaskController")["FlatGenerationInfo"]; pct == nil {
		t.Error("flats report no FlatGenerationInfo")
	}
	cmd("TaskController", "HaltTasks", nil)
	until(t, "the halt", func() bool { return state() == "IDLE" })
	if st := s.Status("TaskController"); st["ErrorCode"] != -7 {
		t.Errorf("halted task's status %v, want INTERRUPTED", st["ErrorCode"])
	}
	cmd("TaskController", "RunGenerateNewDarks", nil)
	if st := s.Status("TaskController"); st["ErrorCode"] != 0 || st["State"] != "GENERATING_DARKS" {
		t.Errorf("next task: %v %v", st["State"], st["ErrorCode"])
	}
	until(t, "the darks", func() bool { return state() == "IDLE" })

	// A ZIP is made as a task, with ZipInfo.
	cmd("TaskController", "ZipDirectory", fake.Msg{"DirectoryName": "M 42"})
	if state() != "ZIPPING_DIRECTORY" || s.Status("TaskController")["ZipInfo"] == nil {
		t.Errorf("zipping: %v", s.Status("TaskController"))
	}
	until(t, "the ZIP", func() bool { return state() == "IDLE" })

	// Moving-target tracking needs an aligned mount, then goes to the track.
	track := fake.Msg{"TargetTrack": []any{map[string]any{"Ra": 1.0, "Dec": 0.3, "JulianDate": 2461000.5}}}
	if r := cmd("Mount", "TrackMovingTarget", track); r["ErrorCode"] != -5 {
		t.Errorf("tracking an unaligned mount: %v", r["ErrorCode"])
	}
	s.SetStatus("Mount", fake.Msg{"IsAligned": true})
	if r := cmd("Mount", "TrackMovingTarget", track); r["ErrorCode"] != 0 || !m.mount.moving() {
		t.Errorf("tracking: %v, moving %v", r["ErrorCode"], m.mount.moving())
	}

	// A cooled camera cools to its target and reports its power.
	cmd("Camera", "SetCooling", fake.Msg{"CoolingEnabled": true, "TemperatureTarget": -5.0})
	if st := s.Status("Camera"); st["Temperature"] != -5.0 || st["PowerLevel"].(float64) <= 0 {
		t.Errorf("cooled camera %v", st)
	}

	// The mount reports its clock in the observer's zone.
	cmd("TaskController", "RunInitialize", fake.Msg{"TimeZone": "Europe/Zagreb", "Latitude": 0.8, "Longitude": 0.28})
	m.mount.step()
	if st := s.Status("Mount"); len(st["Date"].(string)) != 10 || len(st["Time"].(string)) != 8 {
		t.Errorf("mount clock %v %v", st["Date"], st["Time"])
	}
}
