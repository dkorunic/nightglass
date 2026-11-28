// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

func quickSim(t *testing.T) (*sim, *fake.Server) {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	m := newSim(s, time.Millisecond)
	m.step = 20 * time.Millisecond

	return m, s
}

// until waits up to 5 s for cond.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestFocuserTravels(t *testing.T) {
	m, s := quickSim(t)
	s.SetStatus("Focuser", fake.Msg{"Position": bestFocus})
	m.handle(fake.Msg{"Destination": "Focuser", "Command": "MoveTo", "Value": bestFocus + 800})
	until(t, "the move to start", func() bool { over, _ := s.Status("Focuser")["IsMoveToOver"].(bool); return !over })
	if m.blur() == 0 {
		t.Error("no blur away from best focus")
	}
	until(t, "the move to end", func() bool {
		st := s.Status("Focuser")
		over, _ := st["IsMoveToOver"].(bool)

		return over && st["Position"] == bestFocus+800
	})
	if v := s.Status("Focuser")["Velocity"]; v != 0.0 {
		t.Errorf("velocity %v at rest", v)
	}
}

func TestAutofocus(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		best float64
	}{{"RunInfinityAutoFocus", bestFocus}, {"RunTerrestrialAutoFocus", bestFocus + terrestrialShift}} {
		m, s := quickSim(t)
		s.SetStatus("Focuser", fake.Msg{"Position": bestFocus + 1000})
		m.handle(fake.Msg{"Destination": "TaskController", "Command": c.cmd, "StartingPosition": bestFocus + 1000})
		if st := s.Status("TaskController"); st["State"] != "FOCUSING" || st["FocusInfo"] == nil {
			t.Errorf("%s: status %v", c.cmd, st)
		}
		until(t, c.cmd+" to end", func() bool { return s.Status("TaskController")["State"] == "IDLE" })
		if p := m.focusPos(); p != c.best {
			t.Errorf("%s: focused at %v, want %v", c.cmd, p, c.best)
		}
		if pct := s.Status("TaskController")["FocusInfo"].(fake.Msg)["PercentageComplete"]; pct != 100 {
			t.Errorf("%s: %v%% complete", c.cmd, pct)
		}
	}
	// HaltTasks stops it where it is.
	m, s := quickSim(t)
	s.SetStatus("Focuser", fake.Msg{"Position": bestFocus + 3000})
	m.handle(fake.Msg{"Destination": "TaskController", "Command": "RunInfinityAutoFocus"})
	time.Sleep(100 * time.Millisecond)
	m.handle(fake.Msg{"Destination": "TaskController", "Command": "HaltTasks"})
	until(t, "the halt", func() bool { return s.Status("TaskController")["State"] == "IDLE" })
	if over, _ := s.Status("Focuser")["IsMoveToOver"].(bool); !over || m.focusPos() == bestFocus {
		t.Errorf("after the halt: focuser %v", s.Status("Focuser"))
	}
}

func TestPowerCycle(t *testing.T) {
	m, s := quickSim(t)
	m.rebootTime = 300 * time.Millisecond
	m.handle(fake.Msg{"Destination": "System", "Command": "Reboot"})
	until(t, "the reboot", s.Down)
	if st := s.Status("TaskController"); st["IsReady"] != false {
		t.Errorf("ready after a reboot: %v", st)
	}
	until(t, "the restart", func() bool { return !s.Down() })
	m.handle(fake.Msg{"Destination": "System", "Command": "PowerDownOrigin"})
	until(t, "the power down", s.Down)
	time.Sleep(500 * time.Millisecond)
	if !s.Down() {
		t.Error("came back after a power down")
	}
}

func TestFlatPanelAndRecalibration(t *testing.T) {
	m, s := quickSim(t)
	// A missing panel is reported in an Error notification
	// (EL_PANEL_NOT_CONNECTED), not by refusing the command, and stays off.
	m.handle(fake.Msg{"Destination": "ElPanel", "Command": "TurnOnLight"})
	if st := s.Status("ElPanel"); st["LightOn"] == true {
		t.Error("a missing panel turned on")
	}
	s.SetStatus("ElPanel", fake.Msg{"Connected": true, "LightOn": false, "BrightnessLevel": 50})
	m.handle(fake.Msg{"Destination": "ElPanel", "Command": "TurnOnLight"})
	m.handle(fake.Msg{"Destination": "ElPanel", "Command": "SetLightLevel", "LightLevel": 80.0})
	if st := s.Status("ElPanel"); st["LightOn"] != true || st["BrightnessLevel"] != 80.0 {
		t.Errorf("panel %v", st)
	}
	m.handle(fake.Msg{"Destination": "Environment", "Command": "RecalibrateEnvironmentSensor"})
	if s.Status("Environment")["Recalibrating"] != true {
		t.Error("not recalibrating")
	}
	until(t, "the recalibration", func() bool { return s.Status("Environment")["Recalibrating"] == false })
}
