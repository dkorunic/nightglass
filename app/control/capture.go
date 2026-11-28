// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// The app's refusals, title and message as its alerts show them.
var (
	errMoving    = errors.New("alert:Telescope in Motion: Please wait for the telescope to stop moving before capturing an image.")
	errBusy      = errors.New("Origin Is Busy: This feature can only be used while Origin is idle")
	errFocusBusy = errors.New("Origin Is Busy: Focus cannot be set unless Origin is idle.")
)

// goingTo reports a go-to in progress (Mount/GetStatus IsGotoOver false).
func goingTo(c *origin.Client) bool {
	var ms struct{ IsGotoOver *bool }
	m, ok := c.Last("Mount/GetStatus")

	return ok && m.Decode(&ms) == nil && ms.IsGotoOver != nil && !*ms.IsGotoOver
}

// NotMoving is the app's first check on its capture button
// (captureImageButtonTouched): while the telescope moves, "Telescope in
// Motion" comes before any end-imaging question.
func (a *App) NotMoving() error {
	return a.with(func(c *origin.Client) error {
		if a.moving(c) {
			return errMoving
		}

		return nil
	})
}

// moving is the native Telescope.isTelescopeSlewing: a go-to or a manual slew.
func (a *App) moving(c *origin.Client) bool {
	a.mu.Lock()
	slewing := a.slewing
	a.mu.Unlock()

	return slewing || goingTo(c)
}

func idle(c *origin.Client) error {
	if c.TaskState() != "IDLE" {
		return errBusy
	}

	return nil
}

// ready reports TaskController IsReady (initialized).
func ready(c *origin.Client) bool {
	var ts origin.TaskStatus
	m, ok := c.Last("TaskController/GetStatus")

	return ok && m.Decode(&ts) == nil && ts.IsReady
}

// snapshot takes a snapshot, as the app's captureSnapShotImage: exposure
// and ISO only in manual mode. The image arrives as SAMPLE_CAPTURE.
func (a *App) snapshot(c *origin.Client) error {
	var p map[string]any
	if a.Settings().ManualImaging {
		_, exp, iso := a.capture(c)
		p = map[string]any{"ExposureTime": exp, "ISO": iso}
	}
	_, err := c.Send(context.Background(), "TaskController", "RunSampleCapture", p)

	return err
}

// SaveShown saves the image shown (the live view, or the stacked image of
// a run) into the save folder, with the site's location, as Origin Live
// View can; the app saves only snapshots and final images.
func (a *App) SaveShown() (string, error) {
	kind := a.Activity().Image
	a.mu.Lock()
	f, ok := a.frames[kind]
	a.mu.Unlock()
	if !ok || kind == "" {
		return "", errors.New("no image to save")
	}
	s := a.Settings()
	name := f.Info.ObjectName
	if name == "" {
		name = map[string]string{kindLive: "Live", kindStacked: "Stacked", kindFinal: "Final", kindSnapshot: "Snapshot"}[kind]
	}

	return a.saveFrame(f, origin.Metadata{LatDeg: s.LatDeg, LonDeg: s.LonDeg, HasLocation: s.HasLocation()}, name)
}

// SaveSnapshot saves the snapshot shown (the app's Save) as a PNG carrying
// only the site's location, and stops showing it, saved or not; a failure
// is the app's "Error Saving Image" alert (SaveDeleteImageFragment).
func (a *App) SaveSnapshot() (string, error) {
	a.mu.Lock()
	f, ok := a.frames[kindSnapshot]
	a.mu.Unlock()
	if !ok {
		return "", errors.New("no snapshot to save")
	}
	s := a.Settings()
	p, err := a.saveFrame(f, origin.Metadata{LatDeg: s.LatDeg, LonDeg: s.LonDeg, HasLocation: s.HasLocation()}, "Snapshot")
	a.DiscardSnapshot()
	if err != nil {
		slog.Warn("could not save the snapshot", "err", err)

		return "", errors.New("alert:Error Saving Image: Could not save image.")
	}

	return p, nil
}

// DiscardSnapshot stops showing the snapshot (the app's Delete).
func (a *App) DiscardSnapshot() {
	a.mu.Lock()
	delete(a.frames, kindSnapshot)
	a.mu.Unlock()
	a.refresh()
}

// SetCooling switches a cooled camera's cooler (Camera/SetCooling, as
// Celestron's web app; the app's cameras have none): on, it cools to
// targetC. The choice itself is a preference the frontend saves.
func (a *App) SetCooling(on bool, targetC float64) error {
	if err := inRange("cooling target", targetC, coolingMin, coolingMax); err != nil {
		return err
	}

	return a.with(func(c *origin.Client) error {
		if ci, _ := c.CameraInfo(); !ci.HasCooling {
			return errors.New("this camera has no cooler")
		}
		p := map[string]any{"CoolingEnabled": on}
		if on {
			p["TemperatureTarget"] = targetC
		}
		_, err := c.Send(context.Background(), "Camera", "SetCooling", p)

		return err
	})
}

// coolOnConnect sends the saved cooling choice once the camera reports a
// cooler, as the web app does on GetCameraInfo (the Origin keeps none of
// its own across restarts).
func (a *App) coolOnConnect(c *origin.Client) {
	for range 100 {
		if ci, ok := c.CameraInfo(); ok {
			if s := a.Settings(); ci.HasCooling && s.Cooling {
				_ = a.SetCooling(true, s.CoolingTargetC)
			}

			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// CenterHere recenters the telescope on a point of the image, x and y in
// 0..1 from the left and top (the app's reframe crosshair): only while
// idle and initialized, and not in snapshot mode.
func (a *App) CenterHere(x, y float64) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if err := idle(c); err != nil {
		return err
	}
	if !ready(c) || a.Settings().Snapshot || a.Activity().MasterBusy {
		return errShown // the app's reframe button is disabled then
	}
	x = min(max(x, 0), 1)
	// With MirrorFlip the Origin flips images left-right before sending them,
	// but OffsetX is in the unflipped sensor frame (Celestron's web app,
	// liveview.js; not in the app, which never reads Optics).
	var optics struct{ MirrorFlip bool }
	if m, ok := c.Last("Optics/GetConfiguration"); ok && m.Decode(&optics) == nil && optics.MirrorFlip {
		x = 1 - x
	}
	if _, err := c.Send(context.Background(), "TaskController", "RunCenterTarget", map[string]any{
		"OffsetX": x, "OffsetY": min(max(y, 0), 1),
	}); err != nil {
		return err
	}
	// The app blanks the image until frames of the new place arrive
	// (recenterCameraHereButtonTapped).
	a.mu.Lock()
	delete(a.frames, kindLive)
	delete(a.frames, kindFinal)
	a.mu.Unlock()
	a.refresh()

	return nil
}

// StretchValues are ImageServer/GetStretch, 0..1.
type StretchValues struct {
	Background, Strength float64
}

// Stretch is the Origin's image stretch, 0.5 each when unknown as in the app.
func (a *App) Stretch() StretchValues {
	st := StretchValues{0.5, 0.5}
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c == nil {
		return st
	}
	var v struct{ Background, Strength *float64 }
	if m, ok := c.Last("ImageServer/GetStretch"); ok && m.Decode(&v) == nil {
		if v.Background != nil {
			st.Background = *v.Background
		}
		if v.Strength != nil {
			st.Strength = *v.Strength
		}
	}

	return st
}

// SetStretch sets one stretch value ("Background" or "Strength", 0..1) and
// reads the stretch back, as the app's sliders do.
func (a *App) SetStretch(which string, v float64) error {
	if which != "Background" && which != "Strength" || v < 0 || v > 1 {
		return fmt.Errorf("stretch %s %v: want Background or Strength in 0..1", which, v)
	}

	return a.with(func(c *origin.Client) error {
		if _, err := c.Send(context.Background(), "ImageServer", "SetStretch", map[string]any{which: v}); err != nil {
			return err
		}
		_, err := c.Send(context.Background(), "ImageServer", "GetStretch", nil)

		return err
	})
}

// SetFilter tells the Origin which filter is now installed ("Clear" or
// "Nebula"), after the app's "Install … Filter Now" prompt. The nebula
// filter cannot be used in snapshot mode.
func (a *App) SetFilter(name string) error {
	if name != "Clear" && name != "Nebula" {
		return fmt.Errorf("unknown filter %q", name)
	}
	if name == "Nebula" && a.Settings().Snapshot {
		return errors.New("SnapShot Mode: Cannot use Nebula Filter in Snapshot mode. Please remove it if it is installed.")
	}

	return a.with(func(c *origin.Client) error {
		if c.Imaging() { // the app hides the filter button while imaging
			return origin.ErrImaging
		}
		if err := c.SetFilter(context.Background(), name); err != nil {
			return err
		}
		_, err := c.Filter(context.Background())

		return err
	})
}

// AutoFocusAfterFilterChange is the app's "Autofocus Now" after a filter
// change: infinity autofocus from where the Origin chooses.
func (a *App) AutoFocusAfterFilterChange() error {
	return a.with(func(c *origin.Client) error {
		if c.Imaging() {
			return origin.ErrImaging
		}

		return c.AutoFocus(context.Background(), false, 0)
	})
}

// Limits are the focuser's travel.
type Limits struct {
	Min, Max int
}

// FocusLimits are Focuser/GetPositionLimits, else the app's 1000–35000.
func (a *App) FocusLimits() Limits {
	l := Limits{1000, 35000}
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c == nil {
		return l
	}
	var v struct{ Min, Max *int }
	if m, ok := c.Last("Focuser/GetPositionLimits"); ok && m.Decode(&v) == nil {
		if v.Min != nil {
			l.Min = *v.Min
		}
		if v.Max != nil {
			l.Max = *v.Max
		}
	}

	return l
}

// FocusTo moves the focuser to pos within its limits, only while idle
// (FocusOptionsFragment).
func (a *App) FocusTo(pos int) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if c.TaskState() != "IDLE" {
		return errFocusBusy
	}
	l := a.FocusLimits()
	pos = min(max(pos, l.Min), l.Max)

	return c.FocusTo(context.Background(), min(max(pos, origin.FocuserMin), origin.FocuserMax))
}
