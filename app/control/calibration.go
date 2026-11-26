// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// DemoMode is the app's "Demo Mode Initialize": an initialization for
// indoor testing (FakeInitialize), only while idle and not already on.
func (a *App) DemoMode() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	var ts origin.TaskStatus
	if m, ok := c.Last("TaskController/GetStatus"); ok && m.Decode(&ts) == nil && ts.IsFakeInitialized {
		return errors.New("Demo Mode Active: Demo Mode is already active.")
	}
	if err := idle(c); err != nil {
		return err
	}
	s := a.Settings()

	return c.Initialize(context.Background(), origin.Site{LatDeg: s.LatDeg, LonDeg: s.LonDeg, Zone: s.TimeZone, Fake: true}, a.now())
}

// PolarAlign starts polar alignment (TaskController/PolarAlign), only while
// idle; the status line then shows the azimuth and altitude errors.
func (a *App) PolarAlign() error {
	return a.idleSet("TaskController", "PolarAlign")
}

// PolarAlignNow is the recommendation's "Run" (SkySafariActivityImaging$1$1):
// sent at once, without waiting for the Origin to be idle.
func (a *App) PolarAlignNow() error {
	return a.with(func(c *origin.Client) error {
		_, err := c.Send(context.Background(), "TaskController", "PolarAlign", nil)

		return err
	})
}

// EndPolarAlignment is "End Polar Alignment": it halts a running polar
// alignment, after which the Origin re-initializes.
func (a *App) EndPolarAlignment() error {
	return a.with(func(c *origin.Client) error {
		if c.TaskState() != "POLAR_ALIGNING" {
			return nil
		}

		return c.HaltTasks(context.Background())
	})
}

// checkInitialized is the app's handleOriginInitializationCompleteWithMessage:
// when an initialization completes on an equatorial Origin it recommends a
// polar alignment ("polar" event), once per completion.
func (a *App) checkInitialized(c *origin.Client, m origin.Message) {
	var ts origin.TaskStatus
	if m.Decode(&ts) != nil {
		return
	}
	done := ts.State == "INITIALIZING" && ts.Stage == "COMPLETE"
	a.mu.Lock()
	was := a.initDone
	a.initDone = done
	a.mu.Unlock()
	if mc, ok := c.MountConfig(); done && !was && ok && mc.IsEquatorial {
		a.emit("polar", struct{}{})
	}
}

// DarkSettings are the dark frame capture's fields.
type DarkSettings struct {
	ISO         int
	ExposureSec float64
	Count       int
}

// DarkDefaults are the app's: the manual ISO and exposure in manual mode,
// else the camera's default ISO and 10 s; 10 exposures.
func (a *App) DarkDefaults() DarkSettings {
	s := a.Settings()
	d := DarkSettings{ISO: 200, ExposureSec: 10, Count: 10}
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c != nil {
		if ci, ok := c.CameraInfo(); ok && ci.DefaultIso > 0 {
			d.ISO = ci.DefaultIso
		}
	}
	if s.ManualImaging {
		d.ISO, d.ExposureSec = s.ISO, s.ExposureSec
		if s.Snapshot { // manualExposureTimeInSecondsFromUserDefaults
			d.ExposureSec = s.SnapshotExposureSec
		}
	}

	return d
}

// CaptureDarks is "Capture Dark Frames": count (1–100) exposures of
// exposureSec at iso (the frontend asks first, with the time it takes).
func (a *App) CaptureDarks(iso int, exposureSec float64, count int) error {
	if count < 1 || count > 100 || exposureSec <= 0 || iso <= 0 {
		return fmt.Errorf("dark frames: %d × %v s at ISO %d: want 1–100 exposures longer than 0 s", count, exposureSec, iso)
	}

	return a.idleSend("RunGenerateNewDarks", map[string]any{"NbrSubs": count, "ExposureTime": exposureSec, "ISO": iso})
}

// RecaptureAllDarks is "Recapture Complete Set of Dark Frames": it replaces
// every dark, custom ones included (about 20 minutes).
func (a *App) RecaptureAllDarks() error {
	return a.idleSend("RunGenerateNewDarks", nil)
}

// CaptureFlats is "Capture Flat Frames".
func (a *App) CaptureFlats() error {
	return a.idleSend("RunGenerateNewFlat", nil)
}

// RestoreFactoryDarks and RestoreFactoryFlats put back the calibration
// frames the Origin left the factory with (TaskController commands in the
// app's API, not in its UI; Origin Live View offers them), only while idle.
func (a *App) RestoreFactoryDarks() error {
	return a.idleSend("RestoreDarksToFactory", nil)
}

// RestoreFactoryFlats: see RestoreFactoryDarks.
func (a *App) RestoreFactoryFlats() error {
	return a.idleSend("RestoreFlatsToFactory", nil)
}

// idleSend sends a TaskController command, only while the Origin is idle.
func (a *App) idleSend(cmd string, p map[string]any) error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		_, err := c.Send(context.Background(), "TaskController", cmd, p)

		return err
	})
}

// SetAutoUpdates is the version screen's automatic updates switch.
func (a *App) SetAutoUpdates(on bool) error {
	return a.set("System", "SetAutomaticUpdates", map[string]any{"Enabled": on}, "GetAutomaticUpdates")
}

// SetUpdateChannel is the "Core Software Channel" (the frontend warns
// before any channel but "stable").
func (a *App) SetUpdateChannel(name string) error {
	if name == "" {
		return errors.New("no update channel")
	}

	// After the change the app reads the channel back and offers an update
	// when the Origin's version differs from the new channel's.
	return a.with(func(c *origin.Client) error {
		if _, err := c.Send(context.Background(), "System", "SetUpdateChannel", map[string]any{"Value": name}); err != nil {
			return err
		}
		if _, err := c.Send(context.Background(), "System", "GetUpdateChannel", nil); err != nil {
			return err
		}
		a.offerUpdate(c)

		return nil
	})
}

// restart is a wait to reconnect to host (connectOriginAfterDelay): after
// an update, with a countdown, or after an ILLEGAL_STATE_OPERATION error.
type restart struct {
	until     time.Time
	stop      chan struct{}
	host      string
	once      sync.Once
	countdown bool
}

// checkUpdate reconnects after an installed update, as the app's
// handleSystemUpdateStatusAndRestartIfNecessary: disconnect now, count down
// restartDelay (Origin.ORIGIN_UPDATE_AND_RESTART_DELAY, 180 s), reconnect.
func (a *App) checkUpdate(c *origin.Client, m origin.Message) {
	var u struct {
		Status          string
		PercentComplete int
	}
	if m.Decode(&u) != nil || u.Status != "UPDATE_INSTALLING" || u.PercentComplete < 100 {
		return
	}
	a.reconnectAfter(c, a.restartDelay, true)
}

// reconnectAfter is the app's connectOriginAfterDelay: disconnect now and
// reconnect to c's host after delay, counting down when countdown is set.
// It does nothing while a wait already runs or once c is replaced.
func (a *App) reconnectAfter(c *origin.Client, delay time.Duration, countdown bool) {
	a.mu.Lock()
	if a.restart != nil || a.client != c {
		a.mu.Unlock()

		return
	}
	a.mu.Unlock()
	a.disconnect()
	r := &restart{host: c.Host(), until: time.Now().Add(delay), countdown: countdown, stop: make(chan struct{})}
	a.mu.Lock()
	a.restart = r
	a.mu.Unlock()
	a.refresh()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		end := time.NewTimer(delay)
		defer end.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-t.C:
				a.refresh() // the countdown
			case <-end.C:
				a.mu.Lock()
				current := a.restart == r
				a.restart = nil
				a.mu.Unlock()
				if current { // connectOriginAfterDelay → connectOrigin
					if err := a.ConnectOrigin(); err != nil {
						a.emitError(err)
					}
				}

				return
			}
		}
	}()
}

// cancelRestart ends a restart wait: the user connected or disconnected.
func (a *App) cancelRestart() {
	a.mu.Lock()
	r := a.restart
	a.restart = nil
	a.mu.Unlock()
	if r != nil {
		r.once.Do(func() { close(r.stop) })
	}
}

// restartSeconds is the countdown shown while waiting to reconnect (0
// without one), or nil.
func (a *App) restartSeconds() *int {
	if a.restart == nil {
		return nil
	}
	s := 0
	if a.restart.countdown {
		s = max(int(time.Until(a.restart.until).Round(time.Second)/time.Second), 0)
	}

	return &s
}
