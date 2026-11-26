// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/dkorunic/nightglass/origin"
)

// Status texts used more than once.
const (
	lowBandwidth       = "Low Network Bandwidth!"
	factoryCalibration = "Factory Calibration"
)

// Activity is the "activity" event: the app's status line, its progress
// bar (Progress in 0..1, or -1 when hidden), the image it shows ("live",
// "stacked", "final", or "" for none), the warning its status button
// shows ("" for none) and the average download speed (MB/s) its status
// screen shows.
type Activity struct {
	Text string
	// Box is the final image's progress box, "" when not shown (finalBox).
	Box     string
	Image   string
	Warning string
	Task    string // the status screen's task line (statusStringForOriginStatus)
	Target  string // the imaging screen's target label, "" for none
	Init    string // the initialize button's text, "" when hidden
	Manual  string // the imaging screen's manual mode summary, "" outside manual mode
	// Forced is the state the capture button goes by until the Origin
	// reports it, "" for none (forcedTempStatusForUI).
	Forced   string
	Progress float64 // 0..1, -1 hidden, -2 indeterminate
	Speed    float64
	Waiting  bool // waiting to reconnect (delayedOriginReconnect)
	// MasterBusy: a master image is downloaded, processed or saved; Reframe
	// is disabled meanwhile (updateUIForScopeStatus), though not while only
	// waiting for the final one.
	MasterBusy bool
}

// statusIn is everything the status line, progress bar and image choice
// depend on.
type statusIn struct {
	Task                  *origin.TaskStatus // nil until the first TaskController status
	DownloadingStacked    *origin.ImageReady // the stacked master downloading, or nil
	Processing            *origin.ImageReady // a master processed after its download, or nil
	Exposure, Exposed     *float64           // Camera/GetCaptureParameters, seconds
	FlatsPercent          *float64           // FactoryCalibrationController FlatGenerationInfo
	TransferPercent       *int               // TaskController/FileTransferStatus, until HaltTasks
	RestartSeconds        *int               // waiting for the Origin to restart after an update
	FinalPercent          *int               // the final image's download progress
	Tiles                 *MosaicView        // the newest GetMosaicTiles answer
	PointedAt             string             // the target the telescope points at (imagingTargetObjectID), "" for none
	Battery, Charger      string             // Mount/GetStatus BatteryLevel, ChargerStatus
	DiskLevel             string             // Disk/GetStatus Level: OK or LOW
	ImagingName           string             // the run's object in the app's label style
	Forced                string             // the state shown until the Origin reports it
	Speed                 float64            // average download speed, MB/s
	Selected              bool               // a target is selected
	Searching             bool               // a network search runs
	Connected, Connecting bool
	WaitingFinal          bool // FinishImaging sent, final master not here yet
	DownloadingFinal      bool
	SavingFinal           bool
	LowBandwidth          bool
	Snapshot              bool // snapshot mode
	Moving                bool // going to a target or slewing (Telescope.isTelescopeSlewing)
	Guiding               bool // Autoguider/GetStatus IsGuiding
	NoLocation            bool // no observing location saved
	StatusScreen          bool // the status screen's wording, not the imaging screen's
	CancellingInit        bool // Cancel Initialization sent
	Composition           bool // the final image downloading is a mosaic's composition
}

// tileActive are the stacking states of a tile being worked on.
var tileActive = []string{"MOVING_TO_OBJECT", "FOCUSING_AND_MOVING_TO_OBJECT", "RUNNING", "DITHERING"}

// mosaicTile is the number of the tile being captured: the tile of the
// run's ImageUuid, else the one active tile, else 0 (unknown).
func mosaicTile(in statusIn) int {
	if in.Tiles == nil {
		return 0
	}
	id := func(t MosaicTile, i int) int {
		if t.TileId > 0 {
			return t.TileId
		}

		return i + 1
	}
	for i, t := range in.Tiles.Tiles {
		if t.Uuid == in.Task.ImagingInfo.ImageUuid && (t.StackingStatus == "WAITING" || slices.Contains(tileActive, t.StackingStatus)) {
			return id(t, i)
		}
	}
	found := 0
	for i, t := range in.Tiles.Tiles {
		if slices.Contains(tileActive, t.StackingStatus) {
			if found != 0 {
				return 0 // more than one: unknown
			}
			found = id(t, i)
		}
	}

	return found
}

// mosaicText is statusStringForOriginStatus for CAPTURING_MOSAIC and
// RECAPTURE_MOSAIC_TILE: the tile (N) and frame (F of M) under way.
func mosaicText(in statusIn) string {
	ts := in.Task
	recapture := ts.State == "RECAPTURE_MOSAIC_TILE"
	tiles := func() string { // "Captured X of Y Tiles"
		if mm := ts.ImagingInfo.MosaicMetadata; mm != nil && mm.TilesTotal > 0 {
			return fmt.Sprintf("Captured %d of %d Tiles", int(mm.TilesCompleted), int(mm.TilesTotal))
		}
		if in.Tiles != nil && in.Tiles.Meta.TilesTotal > 0 {
			return fmt.Sprintf("Captured %d of %d Tiles", in.Tiles.Meta.TilesCompleted, in.Tiles.Meta.TilesTotal)
		}

		return ""
	}
	o := ts.ImagingInfo.Current()
	if o == nil {
		if recapture {
			return "Recapturing Mosaic Tile…"
		}
		if t := tiles(); t != "" {
			return t
		}

		return "Capturing Mosaic…"
	}
	n := mosaicTile(in)
	pick := func(withTile, without string) string {
		if n > 0 {
			return fmt.Sprintf(withTile, n)
		}

		return without
	}
	// Frames per tile: the run's time over the exposure.
	frames, frame := 0, 0
	if o.TotalTime != nil && o.RemainingTime != nil && in.Exposure != nil && *in.Exposure > 0 {
		if m := int(math.Round((*o.TotalTime + *o.RemainingTime) / *in.Exposure)); m >= 1 && m <= 10000 {
			frames = m
		}
	}
	if o.StackDepth != nil {
		frame = int(*o.StackDepth) + 1
		if frames > 0 {
			frame = min(frame, frames)
		}
	}
	var line string
	switch o.StackingStatus {
	case "RUNNING":
		switch {
		case recapture && n > 0 && frames > 0:
			line = fmt.Sprintf("Recapturing Tile %d: Frame %d of %d", n, frame, frames)
		case recapture && n > 0:
			line = fmt.Sprintf("Recapturing Tile %d: Frame %d", n, frame)
		case recapture && frames > 0:
			line = fmt.Sprintf("Recapturing Tile: Frame %d of %d", frame, frames)
		case recapture:
			line = fmt.Sprintf("Recapturing Tile: Frame %d", frame)
		case n > 0 && frames > 0:
			line = fmt.Sprintf("Tile %d: Frame %d of %d", n, frame, frames)
		case n > 0:
			line = fmt.Sprintf("Tile %d: Frame %d", n, frame)
		case frames > 0:
			line = fmt.Sprintf("Capturing Frame %d of %d", frame, frames)
		default:
			line = fmt.Sprintf("Capturing Frame %d", frame)
		}
		if !recapture && o.StackDepth != nil && o.TotalTime != nil {
			line += fmt.Sprintf("\nTotal Stacked Images: %d\nTotal Integration Time: %d s.", int(*o.StackDepth), int(*o.TotalTime))
		}
	case "DITHERING":
		line = pick("Dithering on Tile %d", "Dithering") + "…"
	case "MOVING_TO_OBJECT":
		if recapture {
			line = pick("Moving to Recapture Tile %d", "Moving to Recapture Mosaic Tile") + "…"
		} else {
			line = pick("Moving to Tile %d", "Moving to Next Tile") + "…"
		}
	case "FOCUSING_AND_MOVING_TO_OBJECT":
		if recapture {
			line = pick("Focusing and Moving to Recapture Tile %d", "Focusing and Moving to Recapture Mosaic Tile") + "…"
		} else {
			line = pick("Focusing and Moving to Tile %d", "Focusing and Moving to Next Tile") + "…"
		}
	default:
		if recapture {
			line = pick("Recapturing Tile %d", "Recapturing Mosaic Tile") + "…"
		} else {
			line = pick("Waiting to Capture Tile %d", "Waiting to Capture Next Tile") + "…"
		}
	}
	if t := tiles(); t != "" && !recapture {
		line += "\n" + t
	}

	return line
}

// manualText is the imaging screen's "Manual Mode: ISO %d, Exposure %.1f%s"
// (OriginImageCaptureFragment.updateUI), in μs below 0.1 ms, s from 10 ms.
func manualText(iso int, exp float64) string {
	unit := "s"
	switch {
	case exp < 1e-4:
		exp, unit = exp*1e6, "μs"
	case exp < 0.01:
		exp, unit = exp*1e3, "ms"
	}

	return fmt.Sprintf("Manual Mode: ISO %d, Exposure %.1f%s", iso, exp, unit)
}

// initButton is the app's updateOriginInitializeButton: "Re-Initialize
// Telescope" while idle and not initialized, "Cancel Initialization" while
// initializing ("Cancelling Initialization…" once asked), else hidden.
func initButton(in statusIn) string {
	if !in.Connected || in.Task == nil {
		return ""
	}
	switch {
	case in.Task.State == "IDLE" && !in.Task.IsReady:
		return "Re-Initialize Telescope"
	case in.Task.State == "INITIALIZING" && in.CancellingInit:
		return "Cancelling Initialization…"
	case in.Task.State == "INITIALIZING":
		return "Cancel Initialization"
	}

	return ""
}

// targetLabel is the imaging screen's target name (updateTargetInfoAndAudioButtons):
// "Imaging: <name>" during a run, "Demo Mode" otherwise in demo mode.
func targetLabel(in statusIn) string {
	if !in.Connected || in.Task == nil {
		return ""
	}
	if in.Snapshot { // updateTargetInfoAndAudioButtons
		return "⬤  Snapshot Mode"
	}
	if origin.IsImagingState(in.Task.State) {
		if o := in.Task.ImagingInfo.Current(); o != nil && o.ObjectName != "" {
			return "Imaging: " + cmp.Or(in.ImagingName, o.ObjectName)
		}

		return ""
	}
	if in.Task.IsFakeInitialized { // with a target only
		if in.PointedAt != "" || in.Selected {
			return "Demo Mode"
		}

		return ""
	}

	return in.PointedAt // shown while the telescope points at it
}

// warning is the app's updateOriginStatusButton, whose icons become words:
// connected, the first of polar alignment, a critical or low battery, a
// slow link, a charger fault, a nearly full disk and guiding; disconnected,
// a missing location.
func warning(in statusIn) string {
	if !in.Connected || in.Task == nil {
		if in.NoLocation && in.RestartSeconds == nil {
			return "Location Not Set"
		}

		return ""
	}
	switch {
	case in.Task.State == "POLAR_ALIGNING":
		return "Polar Aligning"
	case in.Battery == "CRITICALLY_LOW":
		return "Battery Critical!"
	case in.Battery == "LOW":
		return "Low Battery!"
	case in.LowBandwidth:
		return lowBandwidth
	case in.Charger == "FAULT":
		return "Charger Fault"
	case in.DiskLevel == "LOW":
		return "Low Disk Space"
	case in.Guiding:
		return "Guiding"
	}

	return ""
}

// finalWait is the final master's progress box message
// (finalMasterHUDWaitMessage).
func finalWait(in statusIn) string {
	if in.Composition { // currentMasterIsMosaicComposition
		return "Please wait. Mosaic composition and download in progress, this can take several minutes."
	}

	return "Please wait. This can take several minutes."
}

// finalBox is the progress box the app shows over the screen while the
// final master is downloaded, processed and saved, besides the status line
// (updateFinalMasterHUDPhase from 0 % as the download starts,
// ImageDownloadAndProcess's "Processing Image…",
// saveFinalStackedMasterWithPayload's "Saving Image…" without the wait
// text); "" when none is shown. Its cases go in statusText's order.
func finalBox(in statusIn) string {
	switch {
	case in.WaitingFinal:
		return ""
	case in.Processing != nil:
		if in.Processing.ImageType == "FINAL_STACKED_MASTER" {
			return "Processing Image…\n" + finalWait(in)
		}

		return ""
	case in.DownloadingFinal:
		pct := 0
		if in.FinalPercent != nil {
			pct = *in.FinalPercent
		}

		return fmt.Sprintf("Downloading Image — %d%%\n%s", pct, finalWait(in))
	case in.DownloadingStacked != nil:
		return ""
	case in.SavingFinal:
		return "Saving Image…"
	}

	return ""
}

// statusText is the app's statusStringForOriginStatus: a download or save
// in progress first (statusStringForOriginDefault), else the task state.
//
//nolint:maintidx // the app's status rules, one case each
func statusText(in statusIn) string {
	switch {
	case in.WaitingFinal:
		return "Requesting Final Image…"
	case in.Processing != nil:
		switch {
		case in.Processing.ImageType == "FINAL_STACKED_MASTER":
			return "Processing Final Image…"
		case in.Task != nil && in.Task.State == "CAPTURING_MOSAIC" && in.Processing.StackDepth > 0:
			return fmt.Sprintf("Processing Frame %d…", in.Processing.StackDepth)
		}

		return fmt.Sprintf("Processing Image %d…", in.Processing.StackDepth)
	case in.DownloadingFinal:
		return "Downloading Final Image…"
	case in.DownloadingStacked != nil:
		what := "Image"
		if in.Task != nil && in.Task.State == "CAPTURING_MOSAIC" && in.DownloadingStacked.StackDepth > 0 {
			what = "Frame"
		}

		return fmt.Sprintf("Downloading %s %d (%.2f MB/s)…", what, in.DownloadingStacked.StackDepth, in.Speed)
	case in.SavingFinal:
		return "Saving Final Image…"
	case in.TransferPercent != nil && *in.TransferPercent >= 0 && *in.TransferPercent <= 99:
		return fmt.Sprintf("Exporting Images: %d%% Complete", *in.TransferPercent)
	case in.RestartSeconds != nil && *in.RestartSeconds > 0:
		return fmt.Sprintf("Reconnecting to Origin in %d…", *in.RestartSeconds)
	case in.RestartSeconds != nil:
		return "Origin Restarting…"
	case !in.Connected && in.Searching: // origin_status_searching_for
		return "Searching for Origin…"
	case !in.Connected && !in.Connecting:
		return "No Connection"
	case !in.Connected || in.Task == nil:
		return "Connecting to Origin…"
	}
	ts := in.Task
	if ts.State == "IDLE" && in.StatusScreen {
		// statusStringForOriginStatus(IDLE), as the status screen shows it.
		switch {
		case in.LowBandwidth:
			return lowBandwidth
		case ts.IsReady && ts.IsFakeInitialized:
			return "Ready To Image\n(Demo Mode)"
		case ts.IsReady:
			return "Ready To Image"
		}

		return "Needs Initialization"
	}
	switch ts.State {
	case "IDLE":
		// Initialized, the imaging screen's hint (updateUIForScopeStatusIdle);
		// otherwise statusStringForOriginStatus(IDLE).
		switch {
		case ts.IsReady && in.Moving:
			return "Near-realtime view. Telescope moving. Images might appear blurry."
		case ts.IsReady && in.Snapshot:
			return `Near-realtime view. Click "Capture Image" to start a quick exposure.`
		case ts.IsReady:
			return `Near-realtime view. Click "Start Imaging" to start a long exposure.`
		case in.LowBandwidth:
			return lowBandwidth
		}

		return "Needs Initialization"
	case "CENTERING_TARGET":
		if ts.CenteringInfo != nil {
			switch ts.CenteringInfo.CenteringStatus {
			case "SLEWING":
				return "Slewing…"
			case "ALIGNING":
				return "Aligning…"
			case "FOCUSING":
				return "Focusing…"
			}
		}

		return "Centering Target…"
	case "IMAGING_LIST":
		return listText(ts.ImagingInfo)
	case "IMAGING_OBJECT":
		if o := ts.ImagingInfo.Current(); o != nil && o.StackDepth != nil && o.TotalTime != nil {
			return fmt.Sprintf("Total Stacked Images: %d\nTotal Integration Time: %d s.", int(*o.StackDepth), int(*o.TotalTime))
		}

		return "Acquiring Image…"
	case "INITIALIZING":
		ii := ts.InitializationInfo
		if ii == nil {
			return "Initializing"
		}
		s := fmt.Sprintf("Initializing: %d%%", int(ii.PercentageComplete))
		if step, ok := map[string]string{"MOVING_MOUNT": "Moving", "ALIGNING": "Aligning", "FOCUSING": "Focusing"}[ii.CurrentStep]; ok {
			s += ", " + step + "…"
		}

		return s
	case "ALIGNING":
		if ts.AlignmentInfo != nil && ts.AlignmentInfo.NumPoints != nil {
			return fmt.Sprintf("Aligning: %d point(s)", int(*ts.AlignmentInfo.NumPoints))
		}

		return "Aligning…"
	case "POLAR_ALIGNING":
		s := "Polar Align Your Origin"
		if p := ts.PolarAlignInfo; p != nil {
			s += fmt.Sprintf("\nAz. Err: %.2f°, Alt. Err: %.2f°", p.AzimuthError*rad2deg, p.AltitudeError*rad2deg)
		}

		return s
	case "GENERATING_DARKS":
		s := "Generating Darks"
		if ts.DarkGenerationInfo != nil {
			s += fmt.Sprintf(" %d%%", int(ts.DarkGenerationInfo.PercentageComplete))
		}

		return s + "…"
	case "GENERATING_FLATS":
		s := "Generating Flats"
		if in.FlatsPercent != nil {
			s += fmt.Sprintf(" %d%%", int(*in.FlatsPercent))
		}

		return s + "…"
	case "CAPTURING_MOSAIC", "RECAPTURE_MOSAIC_TILE":
		return mosaicText(in)
	case "COMPOSING_MOSAIC":
		if c := ts.ComposingMosaicInfo; c != nil && c.PercentComplete != nil {
			return fmt.Sprintf("Composing Mosaic %d%%…", int(*c.PercentComplete))
		}

		return "Composing Mosaic…"
	}
	if s, ok := stateTexts[ts.State]; ok {
		return s + "…"
	}

	return ""
}

const rad2deg = 180 / math.Pi

// stateTexts are the states the app describes with a fixed text.
var stateTexts = map[string]string{
	"CALIBRATING_FOCUSER":                 "Calibrating Focuser",
	"RECAPTURE_OBJECT":                    "Recapturing Scheduled Object",
	"SAMPLE_CAPTURE":                      "Capturing Snapshot",
	"VIDEO_CAPTURE":                       "Recording Video",
	"FOCUSING":                            "Focusing",
	"RESTORE_DARKS_TO_FACTORY":            "Restoring Dark Frames",
	"RESTORING_FLATS_TO_FACTORY":          "Restoring Flat Frames",
	"MEASURING_DARK_CURRENT":              factoryCalibration,
	"FACTORY_DARK_FLAT_GENERATION":        factoryCalibration,
	"FACTORY_HOT_SPOT_CENTERING":          factoryCalibration,
	"FACTORY_HARDWARE_CALIBRATION":        factoryCalibration,
	"FACTORY_FINALIZATION":                factoryCalibration,
	"FACTORY_COLLIMATION":                 factoryCalibration,
	"FACTORY_FA":                          factoryCalibration,
	"COPY_DIRECTORY_TO_FLASH_DRIVE":       "Copying to Flash Drive",
	"COPY_ALL_DIRECTORIES_TO_FLASH_DRIVE": "Copying all to Flash Drive",
	"ZIPPING_DIRECTORY":                   "Compressing Files",
	"UPDATING_SYSTEM":                     "Updating System",
	"RUNNING_FAN_TEST":                    "Running Fan Test",
	"FACTORY_UPDATE":                      "Factory Update",
}

// listText is statusStringForOriginStatusImagingList.
func listText(ii origin.ImagingInfo) string {
	o := ii.Current()
	if o == nil {
		switch {
		case ii.ImageUuid == "":
			return "Running Imaging Schedule…"
		case ii.TotalObjects > 0:
			return fmt.Sprintf("Imaging Object %d of %d", ii.TotalObjects-ii.NumObjectsRemaining, ii.TotalObjects)
		}

		return "Running Imaging Schedule"
	}
	name := o.ObjectName
	if name == "" {
		name = "Object"
	}
	switch o.StackingStatus {
	case "RUNNING":
		s := "Imaging " + name
		if o.TotalTime != nil && o.RemainingTime != nil && *o.RemainingTime > 0 {
			s += "\n " + elapsed(int(*o.RemainingTime)) + " remaining"
		}

		return s
	case "WAITING":
		s := "Waiting to begin imaging " + name
		if o.MinimumStartTime != "" {
			s += " at " + o.MinimumStartTime
		}

		return s
	case "MOVING_TO_OBJECT":
		return "Moving to " + name
	case "FOCUSING_AND_MOVING_TO_OBJECT":
		return "Focusing and moving to " + name
	case "DITHERING":
		return "Dithering " + name
	case "COMPLETED":
		return "Completed imaging " + name
	case "FAILED":
		return "Failed to image " + name
	}

	return "Running Imaging Schedule"
}

// elapsed is Android's DateUtils.formatElapsedTime: MM:SS, or H:MM:SS.
func elapsed(s int) string {
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}

	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// progress is the app's loadingProgress: shown while imaging an object, for
// a schedule's object with time remaining and for a mosaic tile being
// stacked; it follows the exposure in progress (updateProgressUI) when that
// is known, else the schedule object's share of its total time.
func progress(in statusIn) float64 {
	if in.DownloadingFinal && in.FinalPercent != nil {
		return float64(*in.FinalPercent) / 100
	}
	if in.Task == nil {
		return -1
	}
	o := in.Task.ImagingInfo.Current()
	frac := 0.0
	switch in.Task.State {
	case "IMAGING_OBJECT":
	case "IMAGING_LIST":
		if o == nil || o.RemainingTime == nil || *o.RemainingTime <= 0 {
			return -1
		}
		if o.TotalTime != nil {
			frac = *o.TotalTime / (*o.RemainingTime + *o.TotalTime)
		}
	case "CAPTURING_MOSAIC", "RECAPTURE_MOSAIC_TILE":
		running := o != nil && o.StackingStatus == "RUNNING"
		if in.Tiles != nil && !running { // mosaicTilesMapHasRunningTile
			running = slices.ContainsFunc(in.Tiles.Tiles, func(t MosaicTile) bool { return t.StackingStatus == "RUNNING" })
		}
		if !running {
			return -1
		}
	default:
		return -1
	}
	if in.Exposure != nil && in.Exposed != nil && *in.Exposure > 0 {
		frac = *in.Exposed / *in.Exposure
	} else if in.Task.State == "IMAGING_OBJECT" {
		return -2 // indeterminate until the exposure is known (hideProgress)
	}

	return min(max(frac, 0), 1)
}

// shownImage is the app's imageForScopeStatus: the stacked master while
// imaging or while a master is downloading or saving, else the final
// master, else the live view. Waiting for or processing a master counts as
// downloading it. A snapshot waiting to be saved or discarded
// comes first, as the app's SaveDeleteImageFragment covers the screen.
func shownImage(in statusIn, have map[string]bool) string {
	busy := in.WaitingFinal || in.DownloadingFinal || in.DownloadingStacked != nil || in.Processing != nil || in.SavingFinal
	switch {
	case have[kindSnapshot]:
		return kindSnapshot
	case have[kindStacked] && (busy || in.Task != nil && origin.IsImagingState(in.Task.State)):
		return kindStacked
	case have[kindFinal]:
		return kindFinal
	case have[kindLive]:
		return kindLive
	}

	return ""
}
