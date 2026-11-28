// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// A simulated mosaic: a grid of tiles captured one after another, each a
// few stacked frames, then composed into a final image on request.

type simTile struct {
	Uuid                     string
	Status                   string // WAITING, RUNNING, COMPLETED
	TileId, TileRow, TileCol int
	StackDepth               int
}

type simMosaic struct {
	uuid, name, status string // status: NOT_STARTED, IN_PROGRESS, COMPLETED, ABORTED
	tiles              []*simTile
	cols, rows         int
}

func (mo *simMosaic) meta() fake.Msg {
	done := 0
	for _, t := range mo.tiles {
		if t.Status == "COMPLETED" {
			done++
		}
	}

	return fake.Msg{
		"ListUuid": mo.uuid, "ObjectName": mo.name, "Status": mo.status, "TilesTotal": len(mo.tiles), "TilesCompleted": done,
		"TilesAttempted": done, "Grid": fake.Msg{"GridWidth": mo.cols, "GridHeight": mo.rows},
	}
}

func (mo *simMosaic) tileMap() fake.Msg {
	out := fake.Msg{}
	for _, t := range mo.tiles {
		out[t.Uuid] = fake.Msg{
			"Uuid": t.Uuid, "TileId": t.TileId, "TileRow": t.TileRow, "TileCol": t.TileCol,
			"StackDepth": t.StackDepth, "StackingStatus": t.Status,
		}
	}

	return out
}

// mosaicMsg answers the Mosaic module and the mosaic TaskController commands;
// ok is false for anything else.
func (m *sim) mosaicMsg(cmd string, req, r fake.Msg) bool {
	switch cmd {
	case "Mosaic/GetMaximumTiles":
		r["MaximumTiles"] = 20
	case "Mosaic/GetAvailableMosaics":
		m.mu.Lock()
		list := make([]any, 0, len(m.mosaics))
		for _, mo := range m.mosaics {
			list = append(list, mo.meta())
		}
		m.mu.Unlock()
		r["AvailableMosaics"] = list
	case "TaskController/GetObservingListInfo":
		m.mu.Lock()
		mo := m.mosaics[fmt.Sprint(req["ListUuid"])]
		status := "UNKNOWN"
		if mo != nil && mo.status == "COMPLETED" {
			status = "FINISHED"
		}
		m.mu.Unlock()
		if mo == nil { // neither a mosaic nor an observing list (listMsg)
			r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("Unknown ListUuid %v", req["ListUuid"]) // INVALID_DATA

			break
		}
		r["ImagingInfo"] = fake.Msg{"ListUuid": req["ListUuid"], "ListStatus": status}
	case "Mosaic/GetMosaicTiles":
		m.mu.Lock()
		if mo := m.mosaics[fmt.Sprint(req["MosaicUuid"])]; mo != nil {
			r["MosaicMetadata"], r["MosaicTiles"] = mo.meta(), mo.tileMap()
		}
		m.mu.Unlock()
	case "TaskController/RunCaptureMosaic":
		w, _ := req["MosaicWidth"].(float64)
		h, _ := req["MosaicHeight"].(float64)
		stride := 0.8 * 0.014672238805970147 // the app's default field
		cols, rows := max(1, int(math.Ceil(w/stride))), max(1, int(math.Ceil(h/stride)))
		mo := &simMosaic{uuid: fmt.Sprint(req["Uuid"]), name: fmt.Sprint(req["ObjectName"]), status: "IN_PROGRESS", cols: cols, rows: rows}
		for i := range cols * rows {
			mo.tiles = append(mo.tiles, &simTile{Uuid: fmt.Sprintf("%s-T%d", mo.uuid, i+1), TileId: i + 1, TileRow: i / cols, TileCol: i % cols, Status: "WAITING"})
		}
		m.mu.Lock()
		m.mosaics[mo.uuid] = mo
		m.mu.Unlock()
		m.captureTiles(mo, "CAPTURING_MOSAIC", nil)
	case "TaskController/ResumeImagingList":
		m.mu.Lock()
		mo := m.mosaics[fmt.Sprint(req["Uuid"])]
		m.mu.Unlock()
		if mo != nil {
			m.captureTiles(mo, "CAPTURING_MOSAIC", nil)
		}
	case "TaskController/RecaptureMosaicTile":
		m.mu.Lock()
		mo := m.mosaics[fmt.Sprint(req["MosaicUuid"])]
		var tile *simTile
		if mo != nil {
			for _, t := range mo.tiles {
				if t.Uuid == fmt.Sprint(req["ObjectUuid"]) {
					tile = t
				}
			}
		}
		m.mu.Unlock()
		if tile != nil {
			r["NewUuid"] = tile.Uuid
			m.captureTiles(mo, "RECAPTURE_MOSAIC_TILE", tile)
		}
	case "TaskController/RunComposeMosaic":
		comp := fmt.Sprint(req["Uuid"]) + "-COMPOSED"
		r["CompositionUuid"] = comp
		go m.compose(fmt.Sprint(req["Uuid"]), comp)
	default:
		return false
	}

	return true
}

// captureTiles captures the mosaic's unfinished tiles (or only one, when
// recapturing), reporting each as the Origin's task status does.
func (m *sim) captureTiles(mo *simMosaic, state string, only *simTile) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.run != nil {
		m.run()
	}
	m.run, m.uuid, m.name = cancel, mo.uuid, mo.name
	mo.status = "IN_PROGRESS"
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			if ctx.Err() != nil {
				mo.status = "ABORTED"
			} else {
				mo.status = "COMPLETED"
			}
			m.mu.Unlock()
		}()
		for _, t := range mo.tiles {
			m.mu.Lock()
			skip := only != nil && t != only || only == nil && t.Status == "COMPLETED"
			m.mu.Unlock()
			if skip {
				continue
			}
			for depth := 1; depth <= 3; depth++ {
				m.mu.Lock()
				t.Status, t.StackDepth = "RUNNING", depth
				meta := mo.meta()
				m.mu.Unlock()
				m.s.SetStatus("TaskController", fake.Msg{"State": state, "ImagingInfo": fake.Msg{
					"ImageUuid": t.Uuid, "ListUuid": mo.uuid, "MosaicMetadata": meta,
					"ObjectInfoList": []any{fake.Msg{
						"Uuid": t.Uuid, "ObjectName": fmt.Sprintf("%s_Tile%d", mo.name, t.TileId),
						"StackingStatus": "RUNNING", "StackDepth": depth, "TotalTime": float64(10 * depth), "RemainingTime": float64(10 * (3 - depth)),
					}},
				}})
				m.s.NotifyStatus("TaskController")
				select {
				case <-ctx.Done():
					return
				case <-time.After(m.stackIv):
				}
			}
			f := m.skyHere(fullW, fullH, 3) // the tile's place in the grid, 1° × 0.7° apart
			f.RA += (float64(t.TileCol) - float64(mo.cols-1)/2) / max(math.Cos(f.Dec*math.Pi/180), 0.05)
			f.Dec -= (float64(t.TileRow) - float64(mo.rows-1)/2) * 0.7
			m.mu.Lock()
			m.fields[t.Uuid] = f
			t.Status = "COMPLETED"
			m.kept[t.Uuid] = fmt.Sprintf("%s_Tile%d", mo.name, t.TileId) // its master can be downloaded
			m.mu.Unlock()
		}
		m.mu.Lock()
		if m.uuid == mo.uuid {
			m.run, m.uuid, m.name = nil, "", ""
		}
		m.mu.Unlock()
		m.state("IDLE", "")
	}()
}

// compose composes a mosaic, reporting progress, then delivers the result
// as a final image named by its CompositionUuid.
func (m *sim) compose(list, comp string) {
	for _, pct := range []float64{25, 75} {
		m.s.SetStatus("TaskController", fake.Msg{"State": "COMPOSING_MOSAIC", "ComposingMosaicInfo": fake.Msg{"PercentComplete": pct, "ListUuid": list}})
		m.s.NotifyStatus("TaskController")
		time.Sleep(m.step)
	}
	f := m.skyHere(1600, fullH, 3) // the whole grid
	m.mu.Lock()
	if mo := m.mosaics[list]; mo != nil {
		m.grids[comp] = [2]int{mo.cols, mo.rows}
		f.Width = float64(mo.cols) + fake.OriginField - 1
		f.H = int(float64(f.W) * (float64(mo.rows-1)*0.7 + fake.OriginFieldY) / f.Width)
	}
	m.fields[comp] = f
	m.kept[comp] = "Mosaic"
	m.mu.Unlock()
	m.state("IDLE", "")
	m.deliverFinal(comp)
}

// thumbnail serves a small image for ImageServer/GetThumbnail.
func (m *sim) thumbnail(uuid string) {
	loc := "thumb/" + uuid + ".jpg"
	f := m.field(uuid)
	f.W, f.H = 240, max(1, f.H*240/max(f.W, 1))
	m.s.SetFile(loc, f.JPEG())
	m.s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": loc, "ImageType": "THUMBNAIL", "Uuid": uuid})
}

// update simulates System/Update: the download, then the install, after
// which an Origin restarts (the app then reconnects after 180 s).
func (m *sim) update() {
	for _, st := range []fake.Msg{
		{"Status": "UPDATE_DOWNLOADING", "PercentComplete": 50},
		{"Status": "UPDATE_DOWNLOADING", "PercentComplete": 100},
		{"Status": "UPDATE_INSTALLING", "PercentComplete": 50},
		{"Status": "UPDATE_INSTALLING", "PercentComplete": 100},
	} {
		time.Sleep(m.step)
		m.s.Notify("System", "UpdateStatus", st)
	}
}
