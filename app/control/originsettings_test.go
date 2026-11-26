// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// sentCount is how many dest/cmd the fake received, waiting up to a second
// for want of them (posted commands arrive just after the call).
func sentCount(s *fake.Server, dest, cmd string, want int) int {
	n := len(received(s, dest, cmd))
	for end := time.Now().Add(time.Second); n < want && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		n = len(received(s, dest, cmd))
	}

	return n
}

// lastSent is the last dest/cmd the fake received, waiting up to a second
// for one: commands the native library posts arrive just after the call.
func lastSent(t *testing.T, s *fake.Server, dest, cmd string) fake.Msg {
	t.Helper()
	m := received(s, dest, cmd)
	for end := time.Now().Add(time.Second); len(m) == 0 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		m = received(s, dest, cmd)
	}
	if len(m) == 0 {
		t.Fatalf("no %s/%s sent", dest, cmd)
	}

	return m[len(m)-1]
}

// Y: the app's Origin advanced settings read the telescope's values.
func TestOriginSettingsRead(t *testing.T) {
	a, _, s := connected(t)
	s.SetInfo("LedRing/GetBrightnessLevel", fake.Msg{"BrightnessLevel": 0.3})
	s.SetInfo("LampController/GetBrightnessLevel", fake.Msg{"BrightnessLevel": 0.8})
	s.SetInfo("Focuser/GetFocuserAdvancedSettings", fake.Msg{"AutoFocusAfterGotoIsEnabled": true, "TemperatureShiftAutoFocusIsEnabled": false, "ThresholdTemperatureC": 3.0})
	s.SetInfo("Mount/GetMountConfig", fake.Msg{"OnWedge": true})
	s.SetStatus("Environment", fake.Msg{"OtaFanOn": true, "CpuFanOn": false, "Recalibrating": false})
	s.SetStatus("DewHeater", fake.Msg{"Mode": "Manual", "ManualPowerLevel": 0.25, "Aggression": 4})
	s.SetStatus("ElPanel", fake.Msg{"Connected": true, "LightOn": true, "BrightnessLevel": 70})
	// The connect's own check first, so the Version screen's is the last.
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if got := received(s, "System", "HasUpdateAvailable"); len(got) > 0 && got[len(got)-1]["ForceCheck"] == true {
			break
		}
	}
	o, err := a.OriginSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !o.Known || !o.Idle || o.OnWedge == nil || !*o.OnWedge || o.LedRing == nil || *o.LedRing != 0.3 || o.MountLed == nil || *o.MountLed != 0.8 {
		t.Errorf("mount/lighting %+v", o)
	}
	if o.OtaFan == nil || !*o.OtaFan || o.CpuFan == nil || *o.CpuFan || o.Recalibrating == nil || *o.Recalibrating {
		t.Errorf("fans/environment %+v", o)
	}
	if o.GotoAutofocus == nil || !*o.GotoAutofocus || o.TempAutofocus == nil || *o.TempAutofocus || o.TempThresholdC == nil || *o.TempThresholdC != 3 {
		t.Errorf("autofocus %+v", o)
	}
	if o.DewMode != "Manual" || o.DewAggression != 4 || o.DewPower != 0.25 {
		t.Errorf("dew heater %+v", o)
	}
	if !o.ElPanelConnected || !o.ElPanelOn || o.ElPanelLevel != 70 {
		t.Errorf("flat panel %+v", o)
	}
	// The Version screen's update check carries no ForceCheck (the connect's does).
	if got := received(s, "System", "HasUpdateAvailable"); len(got) < 2 || got[len(got)-1]["ForceCheck"] != nil {
		t.Errorf("HasUpdateAvailable %v", got)
	}
	// The app's defaults for what the Origin does not report.
	s.SetStatus("DewHeater", fake.Msg{"Mode": "Auto", "Aggression": nil, "ManualPowerLevel": nil})
	if o, _ := a.OriginSettings(); o.DewAggression != 10 || o.DewPower != 1 {
		t.Errorf("dew defaults %+v, want aggression 10, power 1", o)
	}
}

// Y: each setting sends what the app sends.
func TestOriginSettingsWrite(t *testing.T) {
	a, _, s := connected(t)
	for _, c := range []struct {
		do        func() error
		dest, cmd string
		want      fake.Msg
	}{
		{func() error { return a.SetWedge(true) }, "Mount", "SetMountConfig", fake.Msg{"IsEquatorial": true, "OnWedge": true}},
		{func() error { return a.SetLedRing(0.42) }, "LedRing", "SetBrightnessLevel", fake.Msg{"BrightnessLevel": 0.42}},
		{func() error { return a.SetMountLed(0.1) }, "LampController", "SetBrightnessLevel", fake.Msg{"BrightnessLevel": 0.1}},
		{func() error { return a.SetFan("Telescope", false) }, "Environment", "SetFans", fake.Msg{"OtaFanOn": false}},
		{func() error { return a.SetFan("CPU", true) }, "Environment", "SetFans", fake.Msg{"CpuFanOn": true}},
		{func() error { return a.SetGotoAutofocus(true) }, "Focuser", "AutoFocusAfterGoto", fake.Msg{"Value": true}},
		{func() error { return a.SetTempAutofocus(false, -1) }, "Focuser", "AutoFocusOnTemperatureChange", fake.Msg{"EnableTemperatureShiftAutoFocus": false}},
		{func() error { return a.SetTempAutofocus(true, 6) }, "Focuser", "AutoFocusOnTemperatureChange", fake.Msg{"EnableTemperatureShiftAutoFocus": true, "TemperatureThresholdC": 6.0}},
		{func() error { return a.SetDewAuto(7) }, "DewHeater", "EnableAuto", fake.Msg{"Aggression": 7.0}},
		{func() error { return a.SetDewManual(0.55) }, "DewHeater", "EnableManual", fake.Msg{"PowerLevel": 0.55}},
		{func() error { return a.SetFlatPanel(true) }, "ElPanel", "TurnOnLight", fake.Msg{}},
		{func() error { return a.SetFlatPanel(false) }, "ElPanel", "TurnOffLight", fake.Msg{}},
		{func() error { return a.SetFlatPanelLevel(80) }, "ElPanel", "SetLightLevel", fake.Msg{"LightLevel": 80.0}},
		{a.RecalibrateEnvironmentSensor, "Environment", "RecalibrateEnvironmentSensor", fake.Msg{}},
		{a.PowerOff, "System", "PowerDownOrigin", fake.Msg{}},
	} {
		n := len(received(s, c.dest, c.cmd))
		if err := c.do(); err != nil {
			t.Errorf("%s/%s: %v", c.dest, c.cmd, err)

			continue
		}
		if len(received(s, c.dest, c.cmd)) != n+1 {
			t.Errorf("%s/%s not sent", c.dest, c.cmd)

			continue
		}
		m := lastSent(t, s, c.dest, c.cmd)
		for k, v := range c.want {
			if m[k] != v {
				t.Errorf("%s/%s %s = %v, want %v", c.dest, c.cmd, k, m[k], v)
			}
		}
		for _, k := range []string{"OtaFanOn", "CpuFanOn", "TemperatureThresholdC", "Force"} {
			if _, want := c.want[k]; !want && m[k] != nil {
				t.Errorf("%s/%s also sent %s", c.dest, c.cmd, k)
			}
		}
	}
	// Each change is read back.
	if _, ok := s.Wait(fake.Is("Mount", "GetMountConfig"), time.Second); !ok {
		t.Error("mount config not read back")
	}
	for _, bad := range []func() error{
		func() error { return a.SetLedRing(1.5) },
		func() error { return a.SetDewAuto(0) },
		func() error { return a.SetDewAuto(11) },
		func() error { return a.SetTempAutofocus(true, 11) },
		func() error { return a.SetFlatPanelLevel(0) },
		func() error { return a.SetFan("Side", true) },
	} {
		if bad() == nil {
			t.Error("out-of-range setting accepted")
		}
	}
}

// Y: power off and sensor recalibration need an idle Origin.
func TestOriginSettingsNeedIdle(t *testing.T) {
	a, _, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	for _, f := range []func() error{a.PowerOff, a.RecalibrateEnvironmentSensor} {
		if err := f(); err == nil || !strings.Contains(err.Error(), "idle") {
			t.Errorf("while imaging: %v", err)
		}
	}
	if err := a.PowerOff(); !errors.Is(err, errPowerOffBusy) {
		t.Errorf("power off while imaging: %v, want the app's own refusal", err)
	}
	if o, _ := a.OriginSettings(); o.Idle {
		t.Error("Idle while imaging")
	}
}

// The last model and version reported are kept and shown while disconnected
// (storeCelestronOriginModelAndVersion, ScopeOriginVersionFragment.updateUI).
func TestModelVersionKeptOffline(t *testing.T) {
	a, _, _ := connected(t)
	for end := time.Now().Add(2 * time.Second); (a.Settings().OriginVersion == "" || a.Settings().OriginModel == "") && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	a.Disconnect()
	o, err := a.OriginSettings()
	if err != nil || o.Known || o.Model != "Origin (simulated)" || o.Version != "0.0.0-fake" {
		t.Errorf("offline settings %+v %v", o, err)
	}
}
