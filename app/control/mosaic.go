// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
)

// Mosaics, as the app's MosaicCaptureSettingsFragment, MosaicAreaFragment,
// MosaicConfirmFragment and MosaicCaptureFragment. The app chooses the
// area on its sky chart; Nightglass takes the size in arc minutes.

// The camera's field when no frame has reported one (MosaicAreaFragment).
const (
	defaultFovX = 0.021893731343283578
	defaultFovY = 0.014672238805970147
)

// MosaicPlan is a new mosaic's defaults and choices for its target.
type MosaicPlan struct {
	ExposureChoices           []float64 // manual sub-exposures
	ISOChoices                []int
	WidthArcmin, HeightArcmin float64 // area, as the app's sliders start
	RotationDeg               float64 // the camera's position angle
	DurationSec               float64 // per tile (getDefaultImagingDurationForSkyObject)
	ExposureSec               float64 // per sub-exposure when automatic
	ISO                       int
	MaxTiles                  int // Mosaic/GetMaximumTiles, 20 when unknown
}

// StartTimes is when a mosaic can start tonight, as Unix milliseconds: the
// earliest automatic start (the target 10° up, the app's minimum altitude)
// and the dawn a manual start must not pass (DawnMs 0: none); Visible is
// false when the target does not get that high tonight.
type StartTimes struct {
	MinMs, DawnMs int64
	Visible       bool
}

// MosaicStartTimes is the window the app's image settings check a manual
// start time against (ImageObjectSettingsFragment.validateManualStartTimeAndSet).
func (a *App) MosaicStartTimes(raDeg, decDeg float64) StartTimes {
	s := a.Settings()
	w := catalog.ImagingStart(raDeg, decDeg, 10, a.now(), s.LatDeg, s.LonDeg)
	st := StartTimes{MinMs: w.Min.UnixMilli(), Visible: w.Visible}
	if !w.Dawn.IsZero() {
		st.DawnMs = w.Dawn.UnixMilli()
	}

	return st
}

// mosaicDuration is the app's default per-tile duration for an object type
// (native table): stars 60 s, open clusters 300 s, globular clusters and
// planetary nebulae 600 s, nebulae and galaxies 1200 s, the Moon and
// planets 30 s.
func mosaicDuration(typ string) float64 {
	t := strings.ToLower(typ)
	switch {
	case t == "moon" || t == "planet":
		return 30
	case strings.Contains(t, "open cluster"):
		return 300
	case strings.Contains(t, "globular"), strings.Contains(t, "planetary"):
		return 600
	case strings.Contains(t, "galax"), strings.Contains(t, "nebula"), strings.Contains(t, "hii"),
		strings.Contains(t, "supernova"), strings.Contains(t, "cloud"):
		return 1200
	}

	return 60
}

// fov is the camera's field (radians) and orientation from the newest
// frame, else the connected camera's sensor field, else the app's defaults.
// ok is whether a frame reported it (only a frame knows the orientation).
func (a *App) fov() (x, y, orientation float64, ok bool) {
	if x, y, o, ok := a.frameFov(); ok {
		return x, y, o, true
	}
	if c, err := a.cl(); err == nil {
		if ci, have := c.CameraInfo(); have {
			if x, y, ok := sensorField(ci); ok {
				return x, y, 0, false
			}
		}
	}

	return defaultFovX, defaultFovY, 0, false
}

// frameFov is the field and orientation the newest frame reports.
func (a *App) frameFov() (x, y, orientation float64, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range []string{kindLive, kindStacked, kindFinal} {
		if f, have := a.frames[k]; have && f.Info.FovX > 0 && f.Info.FovY > 0 {
			return f.Info.FovX, f.Info.FovY, f.Info.Orientation, true
		}
	}

	return 0, 0, 0, false
}

// originFocalMM is the Origin's focal length: the app's default field
// height is an Origin 178 sensor (2048 × 2.4 µm) at it.
const originFocalMM = 335.0

// sensorField is the field (radians) of the camera Camera/GetCameraInfo
// describes, at originFocalMM. Not app parity: the app always plans with its
// Origin 178 field, which is wrong for an Origin 678C (1.31° × 0.74°) or a
// 585C (about 1.9° × 1.07°). PixelSize is taken as µm (origin-core logs the
// sensor's pixel size in um); a value outside 1–20 is not trusted.
func sensorField(ci origin.CameraInfo) (x, y float64, ok bool) {
	if ci.MaxImageWidth <= 0 || ci.MaxImageHeight <= 0 || ci.PixelSize < 1 || ci.PixelSize > 20 {
		return 0, 0, false
	}
	rad := ci.PixelSize / 1000 / originFocalMM // per pixel; small angle

	return ci.MaxImageWidth * rad, ci.MaxImageHeight * rad, true
}

// MosaicPlanFor returns the defaults for a mosaic of target (a catalog row's
// Catalog and Name; empty for typed coordinates).
func (a *App) MosaicPlanFor(r ImageRequest) MosaicPlan {
	p := MosaicPlan{WidthArcmin: 60, HeightArcmin: 60, DurationSec: 60, ExposureSec: 10, ISO: 200, ISOChoices: []int{100, 200, 2000}, MaxTiles: 20}
	var t catalog.Target
	if r.Name != "" {
		for _, x := range a.targets(r.Catalog, a.now()) {
			if x.Name == r.Name {
				t = x

				break
			}
		}
	}
	p.DurationSec = mosaicDuration(t.Type)
	// The object's size × 2 × kMosaicSizeScale (0.6), as the app's default.
	if size := max(t.Size, t.Minor); size > 0 && t.Type != "Moon" && t.Type != "Planet" {
		p.WidthArcmin, p.HeightArcmin = 1.2*size, 1.2*size
	}
	if _, _, o, ok := a.fov(); ok {
		p.RotationDeg = math.Mod(o+math.Pi, 2*math.Pi) * 180 / math.Pi
	}
	p.ExposureChoices = []float64{1, 5, 10, 15, 30}
	c, err := a.cl()
	if err != nil {
		return p
	}
	if mc, _ := c.MountConfig(); mc.OnWedge {
		p.ExposureChoices = append(p.ExposureChoices, 45, 60)
	}
	if f, ok := c.Last("Camera/GetFilter"); ok && f.Fields()["Filter"] == "Nebula" {
		p.ExposureSec = 15
	}
	if ci, _ := c.CameraInfo(); ci.DefaultIso > 0 {
		p.ISO = ci.DefaultIso
	}
	if m, err := c.Send(context.Background(), "Mosaic", "GetMaximumTiles", map[string]any{"Scale": 0.5}); err == nil {
		var v struct{ MaximumTiles int }
		if m.Decode(&v) == nil && v.MaximumTiles > 0 {
			p.MaxTiles = v.MaximumTiles
		}
	}

	return p
}

// MosaicGrid is the app's coverage preview: tiles a fifth of the camera's
// shorter side apart (20 % overlap).
type MosaicGrid struct{ Cols, Rows, Tiles int }

// GridFor returns the tiles a width × height (arc minutes) mosaic takes.
func (a *App) GridFor(widthArcmin, heightArcmin float64) MosaicGrid {
	x, y, _, _ := a.fov()
	stride := 0.8 * min(x, y)
	cols := int(math.Ceil(widthArcmin / 60 * math.Pi / 180 / stride))
	rows := int(math.Ceil(heightArcmin / 60 * math.Pi / 180 / stride))

	return MosaicGrid{cols, rows, cols * rows}
}

// MosaicStart is the confirmed mosaic.
type MosaicStart struct {
	StartAt                   time.Time // zero: start as soon as possible
	Catalog, Name             string    // the target; empty for typed coordinates
	RA, Dec                   float64   // its center, J2000 degrees
	WidthArcmin, HeightArcmin float64   // the area
	RotationDeg               float64   // 0..360
	DurationSec               float64   // per tile
	ExposureSec               float64   // per sub-exposure
	ISO                       int       //
	Manual                    bool      // manual camera settings
	PowerDown, AutoFocus      bool      // "Power Down On Completion", "Autofocus After Each Tile"
}

// MosaicRecord is the app's local record of a mosaic it started.
type MosaicRecord struct {
	Uuid, Title, Description string
	Created                  int64 // Unix seconds
}

// StartMosaic is "Begin Mosaic Capture" (beginCaptureTapped): it leaves
// snapshot and manual live view as the app does, then sends
// TaskController/RunCaptureMosaic and records the mosaic.
func (a *App) StartMosaic(m MosaicStart) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if m.WidthArcmin <= 0 || m.HeightArcmin <= 0 || m.DurationSec <= 0 || m.ExposureSec <= 0 {
		return errors.New("a mosaic needs a size, a per-tile duration and an exposure")
	}
	// Snapshot and manual live view are left in the settings only; the app
	// sends no camera command before RunCaptureMosaic (beginCaptureTapped).
	a.update(func(s *Settings) { s.Snapshot, s.ManualImaging = false, false })
	s := a.Settings()
	r := math.Pi / 180
	iso := m.ISO
	if iso == 0 {
		ci, _ := c.CameraInfo()
		iso = origin.ClampISO(0, ci)
	}
	uuid := strings.ToUpper(origin.NewUUID())
	p := map[string]any{
		"Uuid": uuid, "ObjectRa": m.RA * r, "ObjectDec": m.Dec * r,
		"MosaicWidth": m.WidthArcmin / 60 * r, "MosaicHeight": m.HeightArcmin / 60 * r,
		"MosaicOrientation": math.Mod(m.RotationDeg, 360) * r,
		"ExposureTime":      m.ExposureSec, "ISO": iso, "TotalDuration": m.DurationSec,
		"SaveRawImage": s.SaveRaw, "PowerDownOnCompletion": m.PowerDown, "AutoFocusAfterSlew": m.AutoFocus,
	}
	if !m.StartAt.IsZero() {
		// Local time, as the app formats it: the site's zone, as RunInitialize's.
		loc, err := time.LoadLocation(s.TimeZone)
		if err != nil {
			loc = time.Local //nolint:gosmopolitan // the app sends local time
		}
		p["MinimumStartTime"] = m.StartAt.In(loc).Format("02 01 2006 15:04:05")
	}
	if s.Dither {
		p["DitherInterval"], p["DitherDistance"] = s.DitherInterval, s.DitherDistance
	}
	if m.Name != "" {
		p["ObjectName"] = origin.FolderName(m.Name)
		for _, t := range a.targets(m.Catalog, a.now()) {
			if t.Name != m.Name {
				continue
			}
			a.mu.Lock()
			a.runTargets[uuid] = t // addMosaicStackedMasterTargetObjectIDForUUID
			a.mu.Unlock()
			if t.Type == "Moon" || t.Type == "Planet" || catalog.KnownMag(t.Mag) {
				p["ObjectMagnitude"] = t.Mag
			}
			if t.Size > 0 {
				p["ObjectSize"] = 2 * t.Size / 60 * r
			}

			break
		}
	}
	// Only a send that fails is the app's start failure; the mosaic is
	// recorded whatever the Origin answers (MosaicConfirmFragment).
	_, err = c.Send(context.Background(), "TaskController", "RunCaptureMosaic", p)
	var ce *origin.CommandError
	if err != nil && !errors.Is(err, errShown) && !errors.As(err, &ce) {
		return errors.New("flash:Failed to start mosaic capture. Please check the telescope connection and try again.")
	}
	title := "Mosaic"
	if m.Name != "" {
		title = m.Name + " Mosaic"
	}
	rec := MosaicRecord{uuid, title, "Mosaic capture started " + a.now().Local().Format("2006-01-02 15:04"), a.now().Unix()} //nolint:gosmopolitan // shown to the user
	a.update(func(s *Settings) {
		s.Mosaics = append(s.Mosaics, rec)
		s.PendingLists = append(s.PendingLists, uuid) // addListUUIDToCachedList
	})

	return nil
}

// MosaicMeta is MosaicMetadata.
type MosaicMeta struct {
	ListUuid, ObjectName, DateTime, Status     string
	TilesTotal, TilesCompleted, TilesAttempted int
	Grid                                       struct{ GridWidth, GridHeight int }
}

// MosaicItem is one mosaic of the list: the local record and what the
// Origin reports.
type MosaicItem struct {
	Meta *MosaicMeta
	MosaicRecord
}

// Mosaics lists the mosaics started here, newest first, with the Origin's
// Mosaic/GetAvailableMosaics.
func (a *App) Mosaics() []MosaicItem {
	recs := a.Settings().Mosaics
	metas := map[string]MosaicMeta{}
	if c, err := a.cl(); err == nil && len(recs) > 0 {
		if m, err := c.Send(context.Background(), "Mosaic", "GetAvailableMosaics", nil); err == nil {
			var v struct{ AvailableMosaics []MosaicMeta }
			if m.Decode(&v) == nil {
				for _, x := range v.AvailableMosaics {
					metas[x.ListUuid] = x
				}
			}
		}
	}
	out := make([]MosaicItem, 0, len(recs))
	for _, r := range recs {
		it := MosaicItem{MosaicRecord: r}
		if m, ok := metas[r.Uuid]; ok {
			it.Meta = &m
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(x, y MosaicItem) int { return int(y.Created - x.Created) })

	return out
}

// DeleteMosaic forgets a mosaic here, as the app's (the Origin keeps it).
func (a *App) DeleteMosaic(uuid string) error {
	for _, it := range a.Mosaics() {
		if it.Uuid == uuid && it.Meta != nil && it.Meta.Status == "IN_PROGRESS" {
			return errors.New("a mosaic in progress cannot be deleted")
		}
	}
	a.update(func(s *Settings) {
		s.Mosaics = slices.DeleteFunc(slices.Clone(s.Mosaics), func(r MosaicRecord) bool { return r.Uuid == uuid })
	})

	return nil
}

// MosaicTile is one tile of Mosaic/GetMosaicTiles, with Image the file its
// downloaded master was saved to (Nightglass's, for Show Image).
type MosaicTile struct {
	Uuid, Image                          string
	StackingStatus, FailureMessage       string
	TileId, TileRow, TileCol, StackDepth int
}

// MosaicView is a mosaic's metadata and tiles, in the app's order.
type MosaicView struct {
	Composed Composed // the composition saved this session, if any
	Tiles    []MosaicTile
	Meta     MosaicMeta
}

// Composed is a mosaic's composition: its Uuid (its preview thumbnail's)
// and the saved image's file name, for Show Image.
type Composed struct {
	Uuid, Image string
}

// decodeTiles reads a GetMosaicTiles answer and orders the tiles as the
// app: by TileId when every one is unique and above 0, else by row,
// column and Uuid.
func decodeTiles(m origin.Message) (MosaicView, error) {
	var v struct {
		MosaicMetadata MosaicMeta
		MosaicTiles    map[string]MosaicTile
	}
	if err := m.Decode(&v); err != nil {
		return MosaicView{}, err
	}
	view := MosaicView{Meta: v.MosaicMetadata}
	ids := map[int]bool{}
	byID := true
	for key, t := range v.MosaicTiles {
		if t.Uuid == "" {
			t.Uuid = key
		}
		byID = byID && t.TileId > 0 && !ids[t.TileId]
		ids[t.TileId] = true
		view.Tiles = append(view.Tiles, t)
	}
	slices.SortFunc(view.Tiles, func(x, y MosaicTile) int {
		if byID {
			return x.TileId - y.TileId
		}
		if x.TileRow != y.TileRow {
			return x.TileRow - y.TileRow
		}
		if x.TileCol != y.TileCol {
			return x.TileCol - y.TileCol
		}

		return strings.Compare(x.Uuid, y.Uuid)
	})

	return view, nil
}

// MosaicTiles asks for a mosaic's tiles (Mosaic/GetMosaicTiles) and
// remembers them for the status line.
func (a *App) MosaicTiles(uuid string) (MosaicView, error) {
	c, err := a.cl()
	if err != nil {
		return MosaicView{}, err
	}
	m, err := c.Send(context.Background(), "Mosaic", "GetMosaicTiles", map[string]any{"MosaicUuid": uuid})
	if err != nil {
		return MosaicView{}, err
	}
	view, err := decodeTiles(m)
	if err == nil {
		a.mu.Lock()
		kept := view // the view below is the caller's, changed after the unlock
		kept.Tiles = slices.Clone(view.Tiles)
		a.mosaicView = &kept
		view.Composed = a.composed[uuid]
		for i := range view.Tiles {
			view.Tiles[i].Image = a.savedImages[view.Tiles[i].Uuid]
		}
		a.mu.Unlock()
		// Show Image only while the saved image is there (imageDataAvailable).
		saved := func(name string) string {
			if p, err := a.galleryPath(name); err != nil || !fileExists(p) {
				return ""
			}

			return name
		}
		view.Composed.Image = saved(view.Composed.Image)
		for i := range view.Tiles {
			view.Tiles[i].Image = saved(view.Tiles[i].Image)
		}
	}

	return view, err
}

// MosaicThumbnail asks for a tile's or a composition's thumbnail (0.25 or
// 0.5 of the image); it arrives as a THUMBNAIL frame ("thumb" event).
func (a *App) MosaicThumbnail(uuid string, scale float64) error {
	return a.with(func(c *origin.Client) error {
		return c.Post(context.Background(), "ImageServer", "GetThumbnail", map[string]any{"ImageUuid": uuid, "Scale": scale})
	})
}

// RecaptureTile is "Re-Capture" (performRecaptureForTileAtIndex); row and
// col below 0 are unknown and left out.
func (a *App) RecaptureTile(mosaicUuid, tileUuid string, row, col int) error {
	return a.with(func(c *origin.Client) error {
		a.update(func(s *Settings) { s.Snapshot, s.ManualImaging = false, false })
		p := map[string]any{"MosaicUuid": mosaicUuid, "ObjectUuid": tileUuid}
		if row >= 0 { // each one when known (performRecaptureForTileAtIndex)
			p["TileRow"] = row
		}
		if col >= 0 {
			p["TileCol"] = col
		}
		if _, err := c.Send(context.Background(), "TaskController", "RecaptureMosaicTile", p); err != nil {
			return mosaicFailure(err, "Failed to start tile re-capture. Please check the telescope connection.")
		}
		a.askTilesNow(c, mosaicUuid)

		return nil
	})
}

// ResumeMosaic is "Re-Capture Failed or Uncaptured Tiles" (ResumeImagingList),
// only while idle.
func (a *App) ResumeMosaic(uuid string) error {
	return a.with(func(c *origin.Client) error {
		// The app leaves snapshot and manual mode first and checks nothing
		// but the mosaic's state (mosaicActionButtonTapped).
		a.update(func(s *Settings) { s.Snapshot, s.ManualImaging = false, false })
		if _, err := c.Send(context.Background(), "TaskController", "ResumeImagingList", map[string]any{"Uuid": uuid}); err != nil {
			return mosaicFailure(err, "Failed to resume mosaic capture. Please check the telescope connection.")
		}
		a.askTilesNow(c, uuid)

		return nil
	})
}

// ComposeMosaic is "Compose Mosaic" (performComposeMosaic), only while idle;
// the result arrives as a final image named by the CompositionUuid.
func (a *App) ComposeMosaic(uuid string) error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		s := a.Settings()
		m, err := c.Send(context.Background(), "TaskController", "RunComposeMosaic", map[string]any{
			"Uuid": uuid, "Scale": 0.5, "FlattenBackgrounds": s.MosaicFlatten, "NormalizeBackgrounds": s.MosaicNormalize,
			"EqualizeNoise": s.MosaicEqualize,
		})
		if err != nil {
			return mosaicFailure(err, "Failed to compose mosaic. Please try again.")
		}
		var v struct{ CompositionUuid string }
		_ = m.Decode(&v)
		a.mu.Lock()
		a.composition, a.composing = v.CompositionUuid, uuid
		a.mosaicUuids[v.CompositionUuid] = true
		if t, ok := a.runTargets[uuid]; ok {
			a.runTargets[v.CompositionUuid] = t // the composition is of the mosaic's target
		}
		a.mu.Unlock()

		return nil
	})
}

// DownloadTile asks for a tile's stacked master, only while idle ("Origin
// Not Ready" otherwise); it arrives and is saved as a final image.
func (a *App) DownloadTile(tileUuid string) error {
	return a.with(func(c *origin.Client) error {
		// Any master on its way (isBusyDownloadingOrProcessingMasterImage,
		// waitingForFinalStackedMasterImage), or a tile master asked for.
		busy := a.Activity().MasterBusy
		a.mu.Lock()
		busy = busy || a.waitingFinal || a.tileAsked != "" && time.Since(a.tileAskedAt) < 30*time.Second
		a.mu.Unlock()
		if busy {
			return errors.New("flash:2500:Please wait for the current image download to finish.") // showAlertWithoutButtons
		}
		if c.TaskState() != "IDLE" {
			return errors.New("alert:Origin Not Ready: Origin must be connected and idle (not initializing or imaging) to download an image.")
		}
		if err := c.GetFinalStackedMaster(context.Background(), tileUuid); err != nil {
			return err
		}
		a.mu.Lock()
		a.tileAsked, a.tileAskedAt = tileUuid, time.Now()
		a.mosaicUuids[tileUuid] = true // a tile: the mosaic's radius
		if v := a.mosaicView; v != nil {
			if t, ok := a.runTargets[v.Meta.ListUuid]; ok {
				a.runTargets[tileUuid] = t // a tile is of the mosaic's target
			}
		}
		a.mu.Unlock()

		return nil
	})
}

// ListReady is the "listready" event: a mosaic started here has finished
// on the Origin and was not downloaded (warnAboutUndownloadedImageList).
type ListReady struct {
	Uuid, Title string
}

// queryLists is the app's queryForCompletedLists: on connect it asks about
// each mosaic not yet downloaded or dismissed.
func (a *App) queryLists(c *origin.Client) {
	for _, u := range a.Settings().PendingLists {
		_ = c.Post(context.Background(), "TaskController", "GetObservingListInfo", map[string]any{"ListUuid": u})
	}
}

// checkList offers a finished mosaic for download, one question at a time.
func (a *App) checkList(m origin.Message) {
	var r struct{ ImagingInfo origin.ImagingInfo }
	if m.Decode(&r) != nil || r.ImagingInfo.ListStatus != "FINISHED" {
		return
	}
	u := r.ImagingInfo.ListUuid
	s := a.Settings()
	i := slices.IndexFunc(s.Mosaics, func(rec MosaicRecord) bool { return rec.Uuid == u })
	if !slices.Contains(s.PendingLists, u) || i < 0 {
		return
	}
	a.mu.Lock()
	asked := a.listAsked
	a.listAsked = true
	a.mu.Unlock()
	if !asked {
		a.emit("listready", ListReady{Uuid: u, Title: s.Mosaics[i].Title})
	}
}

// AnswerList is the answer to "listready": Download Images… and Don't Warn
// About This List forget the mosaic (forget), Cancel keeps asking on later
// connects.
func (a *App) AnswerList(uuid string, forget bool) {
	a.mu.Lock()
	a.listAsked = false
	a.mu.Unlock()
	if forget {
		a.update(func(s *Settings) {
			s.PendingLists = slices.DeleteFunc(slices.Clone(s.PendingLists), func(u string) bool { return u == uuid })
		})
	}
}

// askTilesNow asks for a mosaic's tiles at once, as the app does after a
// re-capture or resume starts (handleSimpleActionResponse).
func (a *App) askTilesNow(c *origin.Client, uuid string) {
	a.mu.Lock()
	a.tilesAsked = time.Now()
	a.mu.Unlock()
	_ = c.Post(context.Background(), "Mosaic", "GetMosaicTiles", map[string]any{"MosaicUuid": uuid})
}

// mosaicFailure is the capture screen's failure: an error the relay has
// shown stays silent, any other is the app's self-closing alert with its own
// text only (handleSimpleActionResponse, handleComposeMosaicResponse).
func mosaicFailure(err error, text string) error {
	if errors.Is(err, errShown) {
		return errShown
	}

	return errors.New("flash:" + text)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}
