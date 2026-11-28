// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// A simulated observing list (RunObservingList): its objects imaged one
// after another by the telescope, each slewed to, waited for until its
// MinimumStartTime, then a few stacked frames (TotalDuration scaled down to
// at most 3 frames, as mosaic tiles), its master kept as a run's.

type obsObject struct {
	uuid, name, start, status string // status: WAITING, MOVING_TO_OBJECT, RUNNING, COMPLETED, FAILED, STOPPED
	ra, dec, total, exposure  float64
	depth                     int
}

type obsList struct {
	uuid, date, status string // status: RUNNING, FINISHED, STOPPED
	objects            []*obsObject
	powerDown          bool
}

// info is the list's ImagingInfo, current naming the object being imaged
// (m.mu held).
func (l *obsList) info(current string) fake.Msg {
	objs := make([]any, 0, len(l.objects))
	remaining := 0
	for _, o := range l.objects {
		if o.status != "COMPLETED" {
			remaining++
		}
		objs = append(objs, fake.Msg{
			"Uuid": o.uuid, "ObjectName": o.name, "StackingStatus": o.status, "StackDepth": o.depth,
			"TotalTime": float64(o.depth) * o.exposure, "MinimumStartTime": o.start,
		})
	}

	return fake.Msg{
		"ImageUuid": current, "ListUuid": l.uuid, "ListStatus": l.status, "ObjectInfoList": objs,
		"TotalObjects": len(l.objects), "NumObjectsRemaining": remaining,
	}
}

func (l *obsList) summary() fake.Msg {
	done := 0
	for _, o := range l.objects {
		if o.status == "COMPLETED" {
			done++
		}
	}

	return fake.Msg{"Uuid": l.uuid, "DateTime": l.date, "Status": l.status, "ObjectsTotal": len(l.objects), "ObjectsCompleted": done}
}

// newObsList reads RunObservingList.
func newObsList(req fake.Msg) *obsList {
	l := &obsList{uuid: fmt.Sprint(req["Uuid"]), date: time.Now().Format(time.DateTime), status: "RUNNING"}
	l.powerDown, _ = req["PowerDownOnCompletion"].(bool)
	locs, _ := req["Location"].([]any)
	for i, x := range locs {
		p, _ := x.(map[string]any)
		o := &obsObject{status: "WAITING", total: 60, exposure: 10}
		o.uuid, _ = p["Uuid"].(string)
		o.uuid = cmp.Or(o.uuid, fmt.Sprintf("%s-%d", l.uuid, i+1))
		o.name, _ = p["Name"].(string)
		o.start, _ = p["MinimumStartTime"].(string)
		o.ra, _ = p["Ra"].(float64)
		o.dec, _ = p["Dec"].(float64)
		if v, ok := p["TotalDuration"].(float64); ok && v > 0 {
			o.total = v
		}
		if v, ok := p["ExposureTime"].(float64); ok && v > 0 {
			o.exposure = v
		}
		l.objects = append(l.objects, o)
	}

	return l
}

// listMsg answers the observing list commands; ok is false for anything
// else, and for ResumeImagingList and GetObservingListInfo of a uuid that is
// not an observing list (a mosaic's: mosaicMsg).
func (m *sim) listMsg(key string, req, r fake.Msg) bool {
	m.mu.Lock()
	l := m.lists[fmt.Sprint(cmp.Or(req["ListUuid"], req["Uuid"]))]
	m.mu.Unlock()
	switch key {
	case "TaskController/RunObservingList":
		if l := newObsList(req); req["Uuid"] == nil || len(l.objects) == 0 {
			r["ErrorCode"], r["ErrorMessage"] = -4, "missing required field Uuid or Location" // MISSING_DATA
		} else {
			m.mu.Lock()
			m.lists[l.uuid] = l
			m.mu.Unlock()
			m.runList(l, l.objects)
		}
	case "TaskController/ResumeImagingList":
		if l == nil {
			return false
		}
		all, _ := req["AllTargets"].(bool)
		m.mu.Lock()
		var todo []*obsObject
		for _, o := range l.objects {
			if all || o.status != "COMPLETED" {
				o.status, o.depth = "WAITING", 0
				todo = append(todo, o)
			}
		}
		if len(todo) > 0 {
			l.status = "RUNNING"
		}
		m.mu.Unlock()
		if len(todo) > 0 {
			m.runList(l, todo)
		}
	case "TaskController/GetObservingListInfo":
		if l == nil {
			return false
		}
		m.mu.Lock()
		r["ImagingInfo"] = l.info(m.uuid)
		m.mu.Unlock()
	case "ImageServer/GetAvailableImagingLists":
		m.mu.Lock()
		lists := slices.SortedFunc(maps.Values(m.lists), func(a, b *obsList) int { return cmp.Or(cmp.Compare(a.date, b.date), cmp.Compare(a.uuid, b.uuid)) })
		out := make([]any, 0, len(lists))
		for _, l := range lists {
			out = append(out, l.summary())
		}
		m.mu.Unlock()
		r["AvailableObservingLists"] = out
	case "ImageServer/DeleteImagingList":
		recursive, _ := req["RecursiveDelete"].(bool)
		m.mu.Lock()
		switch {
		case l == nil:
			r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("Unknown ListUuid %v", req["ListUuid"]) // INVALID_DATA
		case l.status == "RUNNING":
			r["ErrorCode"], r["ErrorMessage"] = -5, "The list is running." // ILLEGAL_STATE_OPERATION
		default:
			m.deleteList(l, recursive)
		}
		m.mu.Unlock()
	case "ImageServer/DeleteAllImagingLists":
		m.mu.Lock()
		for _, l := range m.lists {
			if l.status != "RUNNING" {
				m.deleteList(l, true)
			}
		}
		m.mu.Unlock()
	default:
		return false
	}

	return true
}

// deleteList forgets l and, recursive, its objects' folders (m.mu held).
func (m *sim) deleteList(l *obsList, recursive bool) {
	delete(m.lists, l.uuid)
	if recursive {
		for _, o := range l.objects {
			delete(m.kept, o.uuid)
		}
	}
}

// runList images todo, objects of l, as the IMAGING_LIST task; HaltTasks or
// CancelImaging (stopImaging) stop it, the object under way then STOPPED.
func (m *sim) runList(l *obsList, todo []*obsObject) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.run != nil {
		m.run()
	}
	m.run, m.uuid, m.name = cancel, todo[0].uuid, todo[0].name
	m.mu.Unlock()
	m.listUpdate(l, todo[0].uuid, func() {}) // busy from now on
	go func() {
		cur := todo[0].uuid
		for _, o := range todo {
			if !m.listObject(ctx, l, o, cur) {
				m.mu.Lock()
				if o.status != "COMPLETED" {
					o.status = "STOPPED"
				}
				l.status = "STOPPED"
				m.mu.Unlock()

				return
			}
			cur = o.uuid
		}
		m.mu.Lock()
		if m.uuid != cur { // stopped as the last object completed
			m.mu.Unlock()

			return
		}
		m.run, m.uuid, m.name = nil, "", ""
		l.status = "FINISHED"
		m.s.SetStatus("TaskController", fake.Msg{"State": "IDLE", "ImagingInfo": l.info("")})
		m.mu.Unlock()
		m.s.NotifyStatus("TaskController")
		if l.powerDown {
			log.Print("observing list finished: powering down as asked")
			m.powerCycle(false)
		}
	}()
}

// listObject images o after prev, reporting each step; false when the list
// was stopped.
func (m *sim) listObject(ctx context.Context, l *obsList, o *obsObject, prev string) bool {
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	if !m.listUpdate(l, prev, func() { m.uuid, m.name = o.uuid, o.name }) {
		return false
	}
	// Waiting for a start time to come: briefly, the simulator does not keep
	// the GUI waiting for hours.
	t, err := time.ParseInLocation("02 01 2006 15:04:05", o.start, time.Local) //nolint:gosmopolitan // MinimumStartTime is the Origin's local time
	if err == nil && t.After(time.Now()) && !wait(2*m.step) {
		return false
	}
	if !m.listUpdate(l, o.uuid, func() { o.status = "MOVING_TO_OBJECT" }) {
		return false
	}
	m.mount.gotoRaDec(o.ra, o.dec)
	for m.mount.moving() {
		if !wait(50 * time.Millisecond) {
			return false
		}
		m.mount.step()
	}
	frames := min(3, max(1, int(o.total/o.exposure)))
	for depth := 1; depth <= frames; depth++ {
		if !wait(m.stackIv) || !m.listUpdate(l, o.uuid, func() { o.status, o.depth = "RUNNING", depth }) {
			return false
		}
		loc := fmt.Sprintf("Images/Astrophotography/%s/Stacked_%d.jpg", dirName(o.uuid, o.name), depth)
		f := m.skyHere(fullW, fullH, depth)
		m.mu.Lock()
		m.fields[o.uuid] = f
		m.mu.Unlock()
		m.s.SetFile(loc, f.JPEG())
		m.s.Notify("ImageServer", "NewImageReady", withField(fake.Msg{
			"FileLocation": loc, "ImageType": "STACKED_MASTER", "StackDepth": depth, "ObjectName": o.name, "Uuid": o.uuid,
		}, f))
	}

	return m.listUpdate(l, o.uuid, func() {
		o.status = "COMPLETED"
		m.kept[o.uuid] = o.name // its folder and master, as a run's
	})
}

// listUpdate runs f and publishes l as the IMAGING_LIST status, unless the
// list was stopped: stopImaging clears m.uuid (cur, the object under way)
// under m.mu before it reports IDLE, so a stopped list never overwrites that.
func (m *sim) listUpdate(l *obsList, cur string, f func()) bool {
	m.mu.Lock()
	if m.uuid != cur {
		m.mu.Unlock()

		return false
	}
	f()
	m.s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_LIST", "ImagingInfo": l.info(m.uuid), "ErrorCode": 0, "ErrorMessage": ""})
	m.mu.Unlock()
	m.s.NotifyStatus("TaskController")

	return true
}
