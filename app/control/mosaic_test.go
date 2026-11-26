// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

func TestMosaicPlanAndGrid(t *testing.T) {
	for typ, want := range map[string]float64{"Galaxy": 1200, "Open Cluster": 300, "Globular Cluster": 600, "Planetary Nebula": 600, "Planet": 30, "*": 60} {
		if got := mosaicDuration(typ); got != want {
			t.Errorf("%s: %v, want %v", typ, got, want)
		}
	}
	a, _ := newApp(t)
	// The default field: stride 0.8 × 0.01467 rad = 40.35′.
	if g := a.GridFor(120, 40); g.Cols != 3 || g.Rows != 1 || g.Tiles != 3 {
		t.Errorf("grid %+v", g)
	}
	p := a.MosaicPlanFor(ImageRequest{Catalog: "Messier", Name: "M 31"})
	m31 := messier(t, a, "M 31")
	if p.DurationSec != 1200 || math.Abs(p.WidthArcmin-1.2*max(m31.Size, m31.Minor)) > 1e-9 || p.MaxTiles != 20 || p.ISO != 200 {
		t.Errorf("plan %+v", p)
	}
}

// Begin Mosaic Capture sends the app's RunCaptureMosaic and records it.
func TestStartMosaic(t *testing.T) {
	a, _, s := connected(t)
	setSettings(t, a, func(st *Settings) { st.Snapshot, st.ManualImaging = true, true })
	m31 := messier(t, a, "M 31")
	zagreb, _ := time.LoadLocation("Europe/Zagreb") // the test site's zone
	at := time.Date(2026, 1, 15, 21, 30, 0, 0, time.UTC).In(zagreb)
	err := a.StartMosaic(MosaicStart{
		Catalog: "Messier", Name: "M 31", RA: m31.RADeg, Dec: m31.DecDeg, WidthArcmin: 120, HeightArcmin: 60,
		RotationDeg: 90, DurationSec: 600, ExposureSec: 10, ISO: 200, StartAt: at, AutoFocus: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := lastSent(t, s, "TaskController", "RunCaptureMosaic")
	r := math.Pi / 180
	uuid, _ := m["Uuid"].(string)
	if uuid != strings.ToUpper(uuid) || !near(m["MosaicWidth"], 2*r) || !near(m["MosaicHeight"], r) || !near(m["MosaicOrientation"], math.Pi/2) ||
		!near(m["ObjectRa"], m31.RADeg*r) || m["TotalDuration"] != 600.0 || m["ExposureTime"] != 10.0 || m["ISO"] != 200.0 ||
		m["MinimumStartTime"] != "15 01 2026 22:30:00" || m["ObjectName"] != "M 31" || m["AutoFocusAfterSlew"] != true ||
		m["PowerDownOnCompletion"] != false || !near(m["ObjectSize"], 2*m31.Size/60*r) {
		t.Errorf("RunCaptureMosaic %v", m)
	}
	if st := a.Settings(); st.Snapshot || st.ManualImaging {
		t.Error("snapshot and manual live view not left, as the app does")
	}
	if got := a.Settings().Mosaics; len(got) != 1 || got[0].Uuid != uuid || got[0].Title != "M 31 Mosaic" {
		t.Errorf("record %v", got)
	}
	if st := a.Settings(); !slices.Contains(st.PendingLists, uuid) || slices.Contains(st.PendingImages, uuid) {
		t.Errorf("pending lists %v, images %v: want the mosaic in the lists only", st.PendingLists, st.PendingImages)
	}
	if a.StartMosaic(MosaicStart{}) == nil {
		t.Error("an empty mosaic accepted")
	}
}

const tilesJSON = `{"MosaicMetadata":{"ListUuid":"MOS","Status":"IN_PROGRESS","TilesTotal":3,"TilesCompleted":1},
"MosaicTiles":{"t2":{"TileId":2,"StackingStatus":"RUNNING","StackDepth":4},"t1":{"TileId":1,"StackingStatus":"COMPLETED"},"t3":{"TileId":3,"StackingStatus":"WAITING"}}}`

func TestMosaicTilesAndStatus(t *testing.T) {
	a, _, s := connected(t)
	var tiles fake.Msg
	_ = json.Unmarshal([]byte(tilesJSON), &tiles)
	s.SetInfo("Mosaic/GetMosaicTiles", tiles)
	view, err := a.MosaicTiles("MOS")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tiles) != 3 || view.Tiles[0].Uuid != "t1" || view.Tiles[1].TileId != 2 || view.Meta.TilesCompleted != 1 {
		t.Fatalf("tiles %+v", view)
	}
	if m := lastSent(t, s, "Mosaic", "GetMosaicTiles"); m["MosaicUuid"] != "MOS" {
		t.Errorf("GetMosaicTiles %v", m)
	}
	exp := 10.0
	in := statusIn{Connected: true, Tiles: &view, Exposure: &exp, Task: task(t,
		`{"State":"CAPTURING_MOSAIC","ImagingInfo":{"ImageUuid":"t2","MosaicMetadata":{"TilesTotal":3,"TilesCompleted":1},
		"ObjectInfoList":[{"Uuid":"t2","StackingStatus":"RUNNING","StackDepth":4,"TotalTime":40,"RemainingTime":560}]}}`)}
	want := "Tile 2: Frame 5 of 60\nTotal Stacked Images: 4\nTotal Integration Time: 40 s.\nCaptured 1 of 3 Tiles"
	if got := statusText(in); got != want {
		t.Errorf("status %q, want %q", got, want)
	}
	in.Task = task(t, `{"State":"RECAPTURE_MOSAIC_TILE","ImagingInfo":{"ImageUuid":"t2","ObjectInfoList":[{"Uuid":"t2","StackingStatus":"MOVING_TO_OBJECT"}]}}`)
	if got := statusText(in); got != "Moving to Recapture Tile 2…" {
		t.Errorf("recapture %q", got)
	}
	in.Tiles = nil
	in.Task = task(t, `{"State":"CAPTURING_MOSAIC","ImagingInfo":{"ImageUuid":"x","ObjectInfoList":[{"Uuid":"x","StackingStatus":"WAITING"}]}}`)
	if got := statusText(in); got != "Waiting to Capture Next Tile…" {
		t.Errorf("no tiles %q", got)
	}
	pct := 30
	if got := finalBox(statusIn{Connected: true, DownloadingFinal: true, FinalPercent: &pct, Composition: true, Task: task(t, `{"State":"IDLE"}`)}); !strings.Contains(got, "Mosaic composition and download in progress") {
		t.Errorf("composition download %q", got)
	}
}

// Recapture, resume, compose and a tile's download send the app's payloads.
func TestMosaicCommands(t *testing.T) {
	a, rec, s := connected(t)
	s.SetInfo("TaskController/RunComposeMosaic", fake.Msg{"CompositionUuid": "COMP"})
	if err := a.RecaptureTile("MOS", "t2", 0, 1); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RecaptureMosaicTile"); m["MosaicUuid"] != "MOS" || m["ObjectUuid"] != "t2" || m["TileRow"] != 0.0 || m["TileCol"] != 1.0 {
		t.Errorf("recapture %v", m)
	}
	if err := a.ResumeMosaic("MOS"); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "ResumeImagingList"); m["Uuid"] != "MOS" {
		t.Errorf("resume %v", m)
	}
	// Both ask for the tiles at once (handleSimpleActionResponse).
	got := received(s, "Mosaic", "GetMosaicTiles")
	for end := time.Now().Add(2 * time.Second); len(got) < 2 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		got = received(s, "Mosaic", "GetMosaicTiles")
	}
	if len(got) < 2 || got[1]["MosaicUuid"] != "MOS" {
		t.Errorf("tiles not asked after re-capture and resume: %v", got)
	}
	if err := a.ComposeMosaic("MOS"); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "RunComposeMosaic"); m["Uuid"] != "MOS" || m["Scale"] != 0.5 || m["FlattenBackgrounds"] != true || m["NormalizeBackgrounds"] != true || m["EqualizeNoise"] != true {
		t.Errorf("compose %v", m)
	}
	if a.composition != "COMP" {
		t.Errorf("composition %q", a.composition)
	}
	if err := a.DownloadTile("t1"); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "ImageServer", "GetFinalStackedMaster"); m["ImageUuid"] != "t1" {
		t.Errorf("tile download %v", m)
	}
	// One tile master at a time (isTileMasterRequestInFlight).
	if err := a.DownloadTile("t2"); err == nil || err.Error() != "flash:2500:Please wait for the current image download to finish." {
		t.Errorf("second tile download while one is in flight: %v", err)
	}
	// A thumbnail arrives as a THUMBNAIL frame and is served.
	if err := a.MosaicThumbnail("t1", 0.25); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Wait(fake.Is("ImageServer", "GetThumbnail"), time.Second); m["ImageUuid"] != "t1" || m["Scale"] != 0.25 {
		t.Errorf("thumbnail %v", m)
	}
	s.SetFile("thumb/t1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "thumb/t1.jpg", "ImageType": "THUMBNAIL", "Uuid": "t1"})
	rec.wait(t, "thumb", func(d any) bool { return d.(Thumb) == Thumb{Uuid: "t1"} })
	// One that cannot be had is reported, so the frontend asks again.
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "", "ImageType": "THUMBNAIL", "Uuid": "t9"})
	rec.wait(t, "thumb", func(d any) bool { return d.(Thumb) == Thumb{Uuid: "t9", Failed: true} })
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/thumb/t1", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("thumb served %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	if a.DownloadTile("t1") == nil || a.ComposeMosaic("MOS") == nil {
		t.Error("tile download or compose while busy")
	}
}

// The list joins the local records with GetAvailableMosaics; deleting is local.
func TestMosaicList(t *testing.T) {
	a, _, s := connected(t)
	a.update(func(st *Settings) {
		st.Mosaics = []MosaicRecord{{Uuid: "OLD", Title: "Old Mosaic", Created: 1}, {Uuid: "NEW", Title: "New Mosaic", Created: 2}}
	})
	var v fake.Msg
	_ = json.Unmarshal([]byte(`{"AvailableMosaics":[{"ListUuid":"NEW","Status":"IN_PROGRESS","TilesTotal":4,"TilesCompleted":2}]}`), &v)
	s.SetInfo("Mosaic/GetAvailableMosaics", v)
	list := a.Mosaics()
	if len(list) != 2 || list[0].Uuid != "NEW" || list[0].Meta == nil || list[0].Meta.TilesCompleted != 2 || list[1].Meta != nil {
		t.Fatalf("list %+v", list)
	}
	if a.DeleteMosaic("NEW") == nil {
		t.Error("deleted a mosaic in progress")
	}
	if err := a.DeleteMosaic("OLD"); err != nil || len(a.Settings().Mosaics) != 1 {
		t.Errorf("delete: %v %v", err, a.Settings().Mosaics)
	}
}

// A finished mosaic started here is offered on connect, one question at a
// time; Cancel keeps it, Download Images… and Don't Warn forget it
// (queryForCompletedLists, warnAboutUndownloadedImageList).
func TestFinishedMosaicOffered(t *testing.T) {
	a, rec, s := connected(t)
	s.SetInfo("TaskController/GetObservingListInfo", fake.Msg{"ImagingInfo": fake.Msg{"ListUuid": "MOS", "ListStatus": "FINISHED"}})
	a.update(func(st *Settings) {
		st.Mosaics = []MosaicRecord{{Uuid: "MOS", Title: "M 31 Mosaic"}}
		st.PendingLists = []string{"MOS"}
	})
	c, _ := a.cl()
	a.queryLists(c)
	if m, ok := s.Wait(fake.Is("TaskController", "GetObservingListInfo"), time.Second); !ok || m["ListUuid"] != "MOS" {
		t.Fatalf("GetObservingListInfo %v", m)
	}
	rec.wait(t, "listready", func(d any) bool { return d.(ListReady) == ListReady{Uuid: "MOS", Title: "M 31 Mosaic"} })
	a.queryLists(c) // asked again while the question is open: not offered twice
	time.Sleep(200 * time.Millisecond)
	rec.mu.Lock()
	n := 0
	for _, e := range rec.got {
		if e.name == "listready" {
			n++
		}
	}
	rec.mu.Unlock()
	if n != 1 {
		t.Errorf("%d listready events, want 1", n)
	}
	a.AnswerList("MOS", false)
	if !slices.Contains(a.Settings().PendingLists, "MOS") {
		t.Error("Cancel forgot the mosaic")
	}
	a.AnswerList("MOS", true)
	if len(a.Settings().PendingLists) != 0 {
		t.Errorf("not forgotten: %v", a.Settings().PendingLists)
	}
}

// The composition's FINAL asks for its half-size thumbnail (the capture
// screen's preview), and once saved the mosaic's view names it (Show Image).
func TestCompositionPreviewAndShowImage(t *testing.T) {
	a, rec, s := connected(t)
	s.SetInfo("TaskController/RunComposeMosaic", fake.Msg{"CompositionUuid": "COMP"})
	if err := a.ComposeMosaic("MOS"); err != nil {
		t.Fatal(err)
	}
	s.SetFile("comp.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{
		"FileLocation": "comp.tiff", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "M 31 Mosaic", "Uuid": "COMP",
		"MosaicGrid": fake.Msg{"GridWidth": 3, "GridHeight": 2, "TileSpacingX": 0.9},
	})
	if m, ok := s.Wait(fake.Is("ImageServer", "GetThumbnail"), 2*time.Second); !ok || m["ImageUuid"] != "COMP" || m["Scale"] != 0.5 {
		t.Errorf("composition thumbnail %v", m)
	}
	toast := rec.wait(t, "toast", func(d any) bool { return strings.HasPrefix(d.(Toast).Message, "Saved ") }).(Toast)
	s.SetInfo("Mosaic/GetMosaicTiles", fake.Msg{"MosaicMetadata": fake.Msg{"ListUuid": "MOS"}, "MosaicTiles": fake.Msg{}})
	v, err := a.MosaicTiles("MOS")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Composed{Uuid: "COMP", Image: filepath.Base(strings.TrimPrefix(toast.Message, "Saved "))}); v.Composed != want {
		t.Errorf("composed %+v, want %+v", v.Composed, want)
	}
	// The grid is kept for the share screen's "3x2 mosaic" (MosaicGrid).
	if rec := a.Settings().Images[v.Composed.Image]; rec.GridW != 3 || rec.GridH != 2 {
		t.Errorf("composition record %+v, want a 3x2 grid", rec)
	}
}

// A downloaded tile's saved image is named on its row (Show Image).
func TestTileShowImage(t *testing.T) {
	a, rec, s := connected(t)
	setTask(t, a, s, fake.Msg{"State": "IDLE", "IsReady": true})
	if err := a.DownloadTile("t1"); err != nil {
		t.Fatal(err)
	}
	s.SetFile("t1.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "t1.tiff", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "Tile", "Uuid": "t1"})
	toast := rec.wait(t, "toast", func(d any) bool { return strings.HasPrefix(d.(Toast).Message, "Saved ") }).(Toast)
	s.SetInfo("Mosaic/GetMosaicTiles", fake.Msg{"MosaicMetadata": fake.Msg{"ListUuid": "MOS"}, "MosaicTiles": fake.Msg{
		"t1": fake.Msg{"TileId": 1, "StackingStatus": "COMPLETED"}, "t2": fake.Msg{"TileId": 2, "StackingStatus": "COMPLETED"},
	}})
	v, err := a.MosaicTiles("MOS")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Base(strings.TrimPrefix(toast.Message, "Saved ")); len(v.Tiles) != 2 || v.Tiles[0].Image != want || v.Tiles[1].Image != "" {
		t.Errorf("tiles %+v, want t1's image %q", v.Tiles, want)
	}
}

// A manual start is checked against tonight's window: from the target 10°
// up to sunrise (validateManualStartTimeAndSet).
func TestMosaicStartTimes(t *testing.T) {
	a, _ := newApp(t)
	m42 := messier(t, a, "M 42")
	w := a.MosaicStartTimes(m42.RADeg, m42.DecDeg)
	if !w.Visible || w.MinMs < evening.UnixMilli() || w.DawnMs <= w.MinMs {
		t.Errorf("M 42 window %+v from %v", w, evening)
	}
	if w := a.MosaicStartTimes(0, -85); w.Visible || w.MinMs != evening.UnixMilli() {
		t.Errorf("never up: %+v", w)
	}
}
