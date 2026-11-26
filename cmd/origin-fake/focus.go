// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"math"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// The simulated focuser: it travels at focusSpeed, and the star field
// blurs with the distance from bestFocus (the captured position, so the
// fake starts in focus). Terrestrial focus lands terrestrialShift further
// out, as a near target needs; both are Nightglass's choices.
const (
	bestFocus        = 18447.0
	terrestrialShift = 2500.0
	focusSpeed       = 4000.0 // steps per second
	focusSweep       = 1500.0 // an autofocus run tries ± this around its start
	focusTries       = 7
)

// focusPos is the focuser's reported position.
func (m *sim) focusPos() float64 {
	switch v := m.s.Status("Focuser")["Position"].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}

	return bestFocus
}

// blur is the star blur in pixels across a 1200-pixel frame.
func (m *sim) blur() float64 { return min(math.Abs(m.focusPos()-bestFocus)/200, 15) }

// startFocuser cancels any focuser move and returns the context of the
// next one, which ends when another starts.
func (m *sim) startFocuser(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	if m.focusStop != nil {
		m.focusStop()
	}
	m.focusStop = cancel
	m.mu.Unlock()

	return ctx
}

// moveFocuser travels to target, reporting IsMoveToOver false and the
// velocity on the way; it returns false when stopped first.
func (m *sim) moveFocuser(ctx context.Context, target float64) bool {
	pos, last := m.focusPos(), time.Now()
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			m.s.SetStatus("Focuser", fake.Msg{"IsMoveToOver": true, "Velocity": 0.0})
			m.s.NotifyStatus("Focuser")

			return false
		case now := <-t.C:
			step := focusSpeed * now.Sub(last).Seconds()
			last = now
			if math.Abs(target-pos) <= step {
				m.s.SetStatus("Focuser", fake.Msg{"Position": target, "IsMoveToOver": true, "Velocity": 0.0})
				m.s.NotifyStatus("Focuser")

				return true
			}
			pos += math.Copysign(step, target-pos)
			m.s.SetStatus("Focuser", fake.Msg{"Position": math.Round(pos), "IsMoveToOver": false, "Velocity": math.Copysign(focusSpeed, target-pos)})
			if i%4 == 0 {
				m.s.NotifyStatus("Focuser")
			}
		}
	}
}

// autofocus runs RunInfinityAutoFocus or RunTerrestrialAutoFocus: a sweep
// around start (the focuser's position when 0), reporting FocusInfo, then
// the best position; HaltTasks ends it where it is.
func (m *sim) autofocus(start float64, terrestrial bool) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.halt != nil {
		m.halt()
	}
	m.halt = cancel
	m.mu.Unlock()
	if start <= 0 {
		start = m.focusPos()
	}
	best := bestFocus
	if terrestrial {
		best += terrestrialShift
	}
	report := func(pos float64, pct int) {
		m.s.SetStatus("TaskController", fake.Msg{"FocusInfo": fake.Msg{"FocusPosition": math.Round(pos), "PercentageComplete": pct}})
		m.state("FOCUSING", "")
	}
	report(start, 0)
	go func() {
		defer m.state("IDLE", "")
		fctx := m.startFocuser(ctx)
		for i := range focusTries {
			pos := start - focusSweep + 2*focusSweep*float64(i)/(focusTries-1)
			if !m.moveFocuser(fctx, pos) {
				return
			}
			report(pos, 100*(i+1)/(focusTries+1))
			select {
			case <-fctx.Done():
				return
			case <-time.After(m.step / 2): // a frame at each position
			}
		}
		if m.moveFocuser(fctx, best) {
			report(best, 100)
		}
	}()
}
