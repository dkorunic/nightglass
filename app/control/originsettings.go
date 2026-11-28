// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dkorunic/nightglass/origin"
)

// OriginSettings are the app's Origin advanced settings
// (SettingsOriginAdvancedFragment) as the telescope reports them; nil is
// not reported, and the app disables that control. Known is a known task
// state and Idle an idle one: the app enables the controls with them.
type OriginSettings struct {
	OnWedge *bool // Mount/GetMountConfig

	LedRing, MountLed *float64 // LedRing, LampController brightness, 0..1

	OtaFan, CpuFan *bool // Environment/GetStatus
	Recalibrating  *bool

	TempAutofocus  *bool    // Focuser/GetFocuserAdvancedSettings
	TempThresholdC *float64 // 1..10 °C
	GotoAutofocus  *bool

	AutoUpdates, Internet *bool

	DewMode string // DewHeater/GetStatus: "Auto", "Manual" or "" (unknown)

	// FinalFormat is the file the Origin saves its final image as
	// (ImageServer/GetFsmFormat): "tif", "png", "jp2" or "" (unknown).
	FinalFormat string

	// Version & Updates (ScopeOriginVersionFragment); "" is unknown.
	Model, Version, Serial string
	Channel                string  // the update channel, "stable" by default
	DewAggression          int     // 1..10, 10 when unreported
	DewPower               float64 // 0..1, 1 when unreported

	ElPanelLevel int // 1..100

	Known, Idle bool

	ElPanelConnected, ElPanelOn bool // ElPanel/GetStatus: the GIOTTO flat generator

	FakeInitialized bool // demo mode is on

	HasCooling bool // Camera/GetCameraInfo: a cooled camera, so Camera Cooling shows
}

// settingsQueries are what updateAdvancedSettingsFromScope asks for, plus
// the flat panel and what ScopeOriginVersionFragment asks on opening
// (HasUpdateAvailable without ForceCheck).
var settingsQueries = [][2]string{
	{"Focuser", "GetFocuserAdvancedSettings"},
	{"Mount", "GetMountConfig"},
	{"LedRing", "GetBrightnessLevel"},
	{"LampController", "GetBrightnessLevel"},
	{"Environment", "GetStatus"},
	{"DewHeater", "GetStatus"},
	{"ElPanel", "GetStatus"},
	{"System", "GetVersion"},
	{"System", "GetModel"},
	{"System", "GetUpdateChannel"},
	{"System", "GetAutomaticUpdates"},
	{"System", "HasUpdateAvailable"},
	{"Network", "HasInternetConnection"},
	{"FactoryCalibrationController", "GetSerialNumber"},
	{"ImageServer", "GetFsmFormat"}, // not in the app, which never changes it
}

// OriginSettings asks the telescope for its settings and returns them. A
// module that does not answer leaves its settings unreported.
func (a *App) OriginSettings() (OriginSettings, error) {
	c, err := a.cl()
	if err != nil {
		// Disconnected, Version & Updates shows the last model and version
		// (ScopeOriginVersionFragment.updateUI).
		s := a.Settings()

		return OriginSettings{Model: s.OriginModel, Version: s.OriginVersion}, nil //nolint:nilerr // disconnected is a state here, not a failure
	}
	var wg sync.WaitGroup
	for _, q := range settingsQueries {
		wg.Go(func() { _, _ = c.Send(context.Background(), q[0], q[1], nil) })
	}
	wg.Wait()

	return readSettings(c), nil
}

// OriginSettingsCached is OriginSettings from what the Origin last reported,
// asking nothing: the app's updateAllSections, which redraws the settings
// as the Origin's statuses arrive.
func (a *App) OriginSettingsCached() (OriginSettings, error) {
	c, err := a.cl()
	if err != nil {
		return a.OriginSettings() // disconnected: the kept model and version
	}

	return readSettings(c), nil
}

func readSettings(c *origin.Client) OriginSettings {
	decode := func(key string, v any) {
		if m, ok := c.Last(key); ok {
			_ = m.Decode(v)
		}
	}
	st := c.TaskState()
	o := OriginSettings{Known: st != "", Idle: st == "IDLE", DewAggression: 10, DewPower: 1}
	var mc struct{ OnWedge *bool }
	decode("Mount/GetMountConfig", &mc)
	o.OnWedge = mc.OnWedge
	var led, lamp struct{ BrightnessLevel *float64 }
	decode("LedRing/GetBrightnessLevel", &led)
	decode("LampController/GetBrightnessLevel", &lamp)
	o.LedRing, o.MountLed = led.BrightnessLevel, lamp.BrightnessLevel
	var env struct{ OtaFanOn, CpuFanOn, Recalibrating *bool }
	decode("Environment/GetStatus", &env)
	o.OtaFan, o.CpuFan, o.Recalibrating = env.OtaFanOn, env.CpuFanOn, env.Recalibrating
	var af struct {
		TemperatureShiftAutoFocusIsEnabled, AutoFocusAfterGotoIsEnabled *bool
		ThresholdTemperatureC                                           *float64
	}
	decode("Focuser/GetFocuserAdvancedSettings", &af)
	o.TempAutofocus, o.TempThresholdC, o.GotoAutofocus = af.TemperatureShiftAutoFocusIsEnabled, af.ThresholdTemperatureC, af.AutoFocusAfterGotoIsEnabled
	var dew struct {
		Mode             string
		Aggression       *int
		ManualPowerLevel *float64
	}
	decode("DewHeater/GetStatus", &dew)
	o.DewMode = dew.Mode
	var fsm struct{ Format string }
	decode("ImageServer/GetFsmFormat", &fsm)
	o.FinalFormat = fsm.Format
	if dew.Aggression != nil {
		o.DewAggression = *dew.Aggression
	}
	if dew.ManualPowerLevel != nil {
		o.DewPower = *dew.ManualPowerLevel
	}
	var el struct {
		Connected, LightOn bool
		BrightnessLevel    int
	}
	decode("ElPanel/GetStatus", &el)
	o.ElPanelConnected, o.ElPanelOn, o.ElPanelLevel = el.Connected, el.LightOn, el.BrightnessLevel
	var ts origin.TaskStatus
	decode("TaskController/GetStatus", &ts)
	o.FakeInitialized = ts.IsFakeInitialized
	ci, _ := c.CameraInfo()
	o.HasCooling = ci.HasCooling
	var ver struct{ Number string }
	var model, channel struct{ Value string }
	var serial struct{ SerialNumber string }
	var auto struct{ Enabled *bool }
	var net struct{ Value *bool }
	decode("System/GetVersion", &ver)
	decode("System/GetModel", &model)
	decode("System/GetUpdateChannel", &channel)
	decode("System/GetAutomaticUpdates", &auto)
	decode("Network/HasInternetConnection", &net)
	decode("FactoryCalibrationController/GetSerialNumber", &serial)
	o.Version, o.Model, o.Serial, o.AutoUpdates, o.Internet = ver.Number, model.Value, serial.SerialNumber, auto.Enabled, net.Value
	o.Channel = channel.Value
	if o.Channel == "" {
		o.Channel = defaultChannel // Origin.ORIGIN_DEFAULT_UPDATE_CHANNEL
	}

	return o
}

// set sends one setting and reads back what it changed, as the app does.
func (a *App) set(dest, cmd string, p map[string]any, readBack ...string) error {
	return a.with(func(c *origin.Client) error {
		if _, err := c.Send(context.Background(), dest, cmd, p); err != nil {
			return err
		}
		for _, q := range readBack {
			_ = c.Post(context.Background(), dest, q, nil)
		}

		return nil
	})
}

// SetFinalFormat sets the file the Origin saves its final image as, "tif" or
// "png" (ImageServer/SetFsmFormat). origin-core also takes "jp2", which
// Nightglass cannot show (it keeps such a file unseen), so it is not offered.
func (a *App) SetFinalFormat(format string) error {
	if format != "tif" && format != "png" {
		return fmt.Errorf("final image format %q is not tif or png", format)
	}

	return a.set("ImageServer", "SetFsmFormat", map[string]any{"Format": format}, "GetFsmFormat")
}

func inRange(what string, v, lo, hi float64) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s %v outside %v–%v", what, v, lo, hi)
	}

	return nil
}

// NotOnEquatorialMount is the polar prompt's "Not On An Equatorial Mount":
// SetMountConfig off, with no read-back (SkySafariActivityImaging$1$1).
func (a *App) NotOnEquatorialMount() error {
	return a.set("Mount", "SetMountConfig", map[string]any{"IsEquatorial": false, "OnWedge": false})
}

// SetWedge is "Using Equatorial Wedge".
func (a *App) SetWedge(on bool) error {
	return a.set("Mount", "SetMountConfig", map[string]any{"IsEquatorial": on, "OnWedge": on}, "GetMountConfig")
}

// SetLedRing is the "LED Ring" brightness, 0..1.
func (a *App) SetLedRing(level float64) error {
	if err := inRange("LED ring brightness", level, 0, 1); err != nil {
		return err
	}

	return a.set("LedRing", "SetBrightnessLevel", map[string]any{"BrightnessLevel": level}, "GetBrightnessLevel")
}

// SetMountLed is the "Mount LED" brightness, 0..1.
func (a *App) SetMountLed(level float64) error {
	if err := inRange("mount LED brightness", level, 0, 1); err != nil {
		return err
	}

	return a.set("LampController", "SetBrightnessLevel", map[string]any{"BrightnessLevel": level}, "GetBrightnessLevel")
}

// SetFan switches the "Telescope" or "CPU" fan.
func (a *App) SetFan(fan string, on bool) error {
	key := map[string]string{"Telescope": "OtaFanOn", "CPU": "CpuFanOn"}[fan]
	if key == "" {
		return fmt.Errorf("unknown fan %q", fan)
	}

	// No read-back: the app waits for the Origin's own status (otaFanSwitchToggled).
	return a.set("Environment", "SetFans", map[string]any{key: on})
}

// SetGotoAutofocus is autofocus "After Goto Any Object".
func (a *App) SetGotoAutofocus(on bool) error {
	return a.set("Focuser", "AutoFocusAfterGoto", map[string]any{"Value": on}, "GetFocuserAdvancedSettings")
}

// SetTempAutofocus is autofocus "On Temperature Change"; the switch sends no
// threshold (thresholdC below 0), the "Change After" slider (1–10 °C, as the
// app's) sends one.
func (a *App) SetTempAutofocus(on bool, thresholdC float64) error {
	p := map[string]any{"EnableTemperatureShiftAutoFocus": on}
	if on && thresholdC >= 0 {
		if err := inRange("temperature change", thresholdC, 1, 10); err != nil {
			return err
		}
		p["TemperatureThresholdC"] = thresholdC
	}

	return a.set("Focuser", "AutoFocusOnTemperatureChange", p, "GetFocuserAdvancedSettings")
}

// SetDewAuto puts the dew heater on Auto with an aggressiveness of 1–10.
func (a *App) SetDewAuto(aggression int) error {
	if err := inRange("dew heater aggressiveness", float64(aggression), 1, 10); err != nil {
		return err
	}

	return a.set("DewHeater", "EnableAuto", map[string]any{"Aggression": aggression}, "GetStatus")
}

// SetDewManual puts the dew heater on Manual at a power of 0..1.
func (a *App) SetDewManual(power float64) error {
	if err := inRange("dew heater power", power, 0, 1); err != nil {
		return err
	}

	return a.set("DewHeater", "EnableManual", map[string]any{"PowerLevel": power}, "GetStatus")
}

// SetFlatPanel switches the GIOTTO flat generator's light.
func (a *App) SetFlatPanel(on bool) error {
	cmd := "TurnOffLight"
	if on {
		cmd = "TurnOnLight"
	}

	return a.set("ElPanel", cmd, nil) // no read-back, as giottoChanged
}

// SetFlatPanelLevel is the "Giotto Brightness", 1–100.
func (a *App) SetFlatPanelLevel(level int) error {
	if err := inRange("Giotto brightness", float64(level), 1, 100); err != nil {
		return err
	}

	return a.set("ElPanel", "SetLightLevel", map[string]any{"LightLevel": level}, "GetStatus")
}

// RecalibrateEnvironmentSensor is "Recalibrate Environment Sensor", only
// while idle (the frontend asks first).
func (a *App) RecalibrateEnvironmentSensor() error {
	return a.idleSet("Environment", "RecalibrateEnvironmentSensor")
}

// errPowerOffBusy is the app's refusal (powerOffScopeNowButtonTapped).
var errPowerOffBusy = errors.New("alert:Origin Is Busy: Origin cannot be powered down unless it is idle.")

// PowerOff powers the Origin down, only while idle (the frontend asks
// first; turning it back on needs its power switch). Once the Origin
// answers, the app disconnects instead of waiting for it to come back.
func (a *App) PowerOff() error {
	if err := a.idleSet("System", "PowerDownOrigin"); errors.Is(err, errBusy) {
		return errPowerOffBusy
	} else if err != nil {
		return err
	}
	a.Disconnect()

	return nil
}

// Reboot restarts the Origin (System/Reboot, which the app never sends;
// Origin Live View does), only while idle (the frontend asks first). Once
// it answers, Nightglass disconnects and reconnects after the Origin's
// restart delay, counting down as after an update; "Attempt To Connect"
// ends the wait early.
func (a *App) Reboot() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if err := a.idleSet("System", "Reboot"); err != nil {
		return err
	}
	a.reconnectAfter(c, a.restartDelay, true)

	return nil
}

func (a *App) idleSet(dest, cmd string) error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		_, err := c.Send(context.Background(), dest, cmd, nil)

		return err
	})
}

// storeModelVersion keeps the model and version the Origin reports.
func (a *App) storeModelVersion(m origin.Message) {
	var v struct{ Value, Number string }
	if m.Decode(&v) != nil {
		return
	}
	s := a.Settings()
	switch {
	case m.Command == "GetModel" && v.Value != "" && v.Value != s.OriginModel:
		a.update(func(s *Settings) { s.OriginModel = v.Value })
	case m.Command == "GetVersion" && v.Number != "" && v.Number != s.OriginVersion:
		a.update(func(s *Settings) { s.OriginVersion = v.Number })
	}
}

// statusScreenQueries are what ScopeStatusFragment asks as it opens.
var statusScreenQueries = [][2]string{
	{"Autoguider", "GetStatus"},
	{"Focuser", "GetPositionLimits"},
	{"Focuser", "GetFocuserAdvancedSettings"},
	{"Mount", "GetMountConfig"},
	{"LedRing", "GetBrightnessLevel"},
	{"LampController", "GetBrightnessLevel"},
	{"Environment", "GetStatus"},
	{"DewHeater", "GetStatus"},
	{"System", "GetUpdateChannel"},
	{"Camera", "GetCameraInfo"},
}

// RefreshStatus asks again for what the status screen shows, as the app
// does each time its status screen opens.
func (a *App) RefreshStatus() error {
	return a.with(func(c *origin.Client) error {
		for _, q := range statusScreenQueries {
			_ = c.Post(context.Background(), q[0], q[1], nil)
		}

		return nil
	})
}
