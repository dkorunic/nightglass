// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
)

// Schedules, as Celestron's web client (firmware 1.5.6286) has them, which
// the Android app has not: tonight's targets go to the Origin as one
// imaging list (TaskController/RunObservingList), which it runs by itself,
// waiting for each target to rise; earlier lists can be rerun or deleted.

// ScheduleEntry is a target of tonight's schedule.
type ScheduleEntry struct {
	Name        string  // as shown; sent folder-safe
	RA, Dec     float64 // J2000, degrees
	DurationSec float64 // the target's total imaging time
	ExposureSec float64
	ISO         int
}

// Visibility is a target's rise, transit and set on the observer's local
// day (zero: not that day), for the Target tab and the schedule.
type Visibility struct {
	Rise, Transit, Set time.Time
	Circumpolar        bool
	NeverRises         bool
}

// Visibility computes a target's rise, transit and set today where the
// observer is, in the observer's time zone.
func (a *App) Visibility(raDeg, decDeg float64) Visibility {
	s := a.Settings()
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		loc = time.Local //nolint:gosmopolitan // no zone chosen: the computer's
	}
	r := catalog.RiseTransitSet(raDeg, decDeg, s.LatDeg, s.LonDeg, a.now().In(loc))

	return Visibility{Rise: r.Rise, Transit: r.Transit, Set: r.Set, Circumpolar: r.Circumpolar, NeverRises: r.NeverRises}
}

// SetSchedule keeps the draft of tonight's schedule.
func (a *App) SetSchedule(entries []ScheduleEntry) error {
	for _, e := range entries {
		if e.Name == "" || e.DurationSec <= 0 || e.ExposureSec <= 0 || e.ISO < 0 ||
			math.Abs(e.Dec) > 90 || e.RA < 0 || e.RA >= 360 {
			return errors.New("Schedule: each target needs a name, a place, a duration and an exposure")
		}
	}
	a.update(func(s *Settings) { s.Schedule = entries })

	return nil
}

// RunSchedule sends tonight's schedule to the Origin, which images each
// target in turn, not before it rises (MinimumStartTime, local time, always
// sent: without it the Origin writes a list it cannot list again).
func (a *App) RunSchedule() error {
	s := a.Settings()
	if len(s.Schedule) == 0 {
		return errors.New("Schedule: no targets scheduled")
	}
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		loc = time.Local //nolint:gosmopolitan // the Origin takes local time
	}
	now := a.now()
	r := math.Pi / 180
	locs := make([]any, 0, len(s.Schedule))
	for _, e := range s.Schedule {
		start := ""
		if rise := catalog.RiseTransitSet(e.RA, e.Dec, s.LatDeg, s.LonDeg, now.In(loc)).Rise; rise.After(now) {
			start = rise.In(loc).Format("02 01 2006 15:04:05")
		}
		locs = append(locs, map[string]any{
			"Uuid": strings.ToUpper(origin.NewUUID()), "Name": origin.FolderName(e.Name),
			"Ra": e.RA * r, "Dec": e.Dec * r, "TotalDuration": e.DurationSec, "ExposureTime": e.ExposureSec,
			"ISO": e.ISO, "SaveRawImage": s.SaveRaw, "MinimumStartTime": start,
		})
	}

	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		_, err := c.Send(context.Background(), "TaskController", "RunObservingList", map[string]any{
			"Uuid": strings.ToUpper(origin.NewUUID()), "PowerDownOnCompletion": s.SchedulePowerDown,
			"AutoFocusAfterSlew": s.ScheduleAutofocus, "Location": locs,
		})

		return err
	})
}

// ImagingList is an imaging list on the Origin
// (ImageServer/GetAvailableImagingLists).
type ImagingList struct {
	Uuid, DateTime, Status         string
	ObjectsTotal, ObjectsCompleted int
}

// Schedules lists the Origin's imaging lists, newest first, without the
// mosaics (also lists, shown in Mosaics).
func (a *App) Schedules() ([]ImagingList, error) {
	mosaic := map[string]bool{}
	for _, m := range a.Settings().Mosaics {
		mosaic[strings.ToUpper(m.Uuid)] = true
	}
	var out []ImagingList
	err := a.with(func(c *origin.Client) error {
		m, err := c.Send(context.Background(), "ImageServer", "GetAvailableImagingLists", nil)
		if err != nil {
			return err
		}
		var v struct{ AvailableObservingLists []ImagingList }
		if err := m.Decode(&v); err != nil {
			return err
		}
		for _, l := range v.AvailableObservingLists {
			if !mosaic[strings.ToUpper(l.Uuid)] {
				out = append(out, l)
			}
		}

		return nil
	})
	// "YYYY-MM-DD HH:MM:SS" sorts as it reads.
	slices.SortFunc(out, func(x, y ImagingList) int { return strings.Compare(y.DateTime, x.DateTime) })

	return out, err
}

// ScheduleInfo is an imaging list's targets and how far each got.
func (a *App) ScheduleInfo(uuid string) (origin.ImagingInfo, error) {
	var v struct{ ImagingInfo origin.ImagingInfo }
	err := a.with(func(c *origin.Client) error {
		m, err := c.Send(context.Background(), "TaskController", "GetObservingListInfo", map[string]any{"ListUuid": uuid})
		if err != nil {
			return err
		}

		return m.Decode(&v)
	})

	return v.ImagingInfo, err
}

// DeleteSchedule deletes an imaging list, and its images when files.
func (a *App) DeleteSchedule(uuid string, files bool) error {
	return a.set("ImageServer", "DeleteImagingList", map[string]any{"ListUuid": uuid, "RecursiveDelete": files})
}

// RerunSchedule images an imaging list's targets again: all, or only those
// that did not complete.
func (a *App) RerunSchedule(uuid string, all bool) error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		_, err := c.Send(context.Background(), "TaskController", "ResumeImagingList", map[string]any{"Uuid": uuid, "AllTargets": all})

		return err
	})
}
