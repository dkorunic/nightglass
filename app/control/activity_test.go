// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

func task(t *testing.T, js string) *origin.TaskStatus {
	t.Helper()
	var ts origin.TaskStatus
	if err := json.Unmarshal([]byte(js), &ts); err != nil {
		t.Fatal(err)
	}

	return &ts
}

//go:fix inline
func f64(v float64) *float64 { return new(v) }

//go:fix inline
func ip(v int) *int { return new(v) }

// K: the status line says what the app's statusStringForOriginStatus says.
func TestStatusText(t *testing.T) {
	stacked := &origin.ImageReady{StackDepth: 7}
	for _, c := range []struct {
		name string
		in   statusIn
		want string
	}{
		{"off", statusIn{}, "No Connection"},
		{"dialing", statusIn{Connecting: true}, "Connecting to Origin…"},
		{"searching", statusIn{Searching: true}, "Searching for Origin…"},
		{"searching while redialling", statusIn{Searching: true, Connecting: true}, "Searching for Origin…"},
		{"no task yet", statusIn{Connected: true}, "Connecting to Origin…"},
		// W: idle and initialized, the imaging screen's hint (updateUIForScopeStatusIdle).
		{"ready", statusIn{Connected: true, Task: task(t, `{"State":"IDLE","IsReady":true}`)}, `Near-realtime view. Click "Start Imaging" to start a long exposure.`},
		{"ready snapshot", statusIn{Connected: true, Snapshot: true, Task: task(t, `{"State":"IDLE","IsReady":true}`)}, `Near-realtime view. Click "Capture Image" to start a quick exposure.`},
		{"ready moving", statusIn{Connected: true, Moving: true, Snapshot: true, Task: task(t, `{"State":"IDLE","IsReady":true}`)}, "Near-realtime view. Telescope moving. Images might appear blurry."},
		{"ready slow link", statusIn{Connected: true, LowBandwidth: true, Task: task(t, `{"State":"IDLE","IsReady":true}`)}, `Near-realtime view. Click "Start Imaging" to start a long exposure.`},
		{"needs init", statusIn{Connected: true, Task: task(t, `{"State":"IDLE"}`)}, "Needs Initialization"},
		{"slow link", statusIn{Connected: true, LowBandwidth: true, Task: task(t, `{"State":"IDLE"}`)}, "Low Network Bandwidth!"},
		{"exporting", statusIn{Connected: true, TransferPercent: new(40), Task: task(t, `{"State":"IDLE","IsReady":true}`)}, "Exporting Images: 40% Complete"},
		{"exported", statusIn{Connected: true, TransferPercent: new(100), Task: task(t, `{"State":"IDLE"}`)}, "Needs Initialization"},
		{"requesting", statusIn{Connected: true, WaitingFinal: true, DownloadingFinal: true, Task: task(t, `{"State":"IDLE"}`)}, "Requesting Final Image…"},
		{"final dl", statusIn{Connected: true, DownloadingFinal: true, Task: task(t, `{"State":"IDLE"}`)}, "Downloading Final Image…"},
		{"stacked dl", statusIn{Connected: true, DownloadingStacked: stacked, Speed: 1.234, Task: task(t, `{"State":"IMAGING_OBJECT"}`)}, "Downloading Image 7 (1.23 MB/s)…"},
		{"frame dl", statusIn{Connected: true, DownloadingStacked: stacked, Task: task(t, `{"State":"CAPTURING_MOSAIC"}`)}, "Downloading Frame 7 (0.00 MB/s)…"},
		{"saving", statusIn{Connected: true, SavingFinal: true, Task: task(t, `{"State":"IDLE"}`)}, "Saving Final Image…"},
		{"processing", statusIn{Connected: true, Processing: stacked, DownloadingStacked: stacked, Task: task(t, `{"State":"IMAGING_OBJECT"}`)}, "Processing Image 7…"},
		{"processing frame", statusIn{Connected: true, Processing: stacked, Task: task(t, `{"State":"CAPTURING_MOSAIC"}`)}, "Processing Frame 7…"},
		{"processing final", statusIn{Connected: true, Processing: &origin.ImageReady{ImageType: "FINAL_STACKED_MASTER"}, DownloadingFinal: true, Task: task(t, `{"State":"IDLE"}`)}, "Processing Final Image…"},
		{"requesting beats processing", statusIn{Connected: true, WaitingFinal: true, Processing: stacked, Task: task(t, `{"State":"IDLE"}`)}, "Requesting Final Image…"},
		{"focuser cal", statusIn{Connected: true, Task: task(t, `{"State":"CALIBRATING_FOCUSER"}`)}, "Calibrating Focuser…"},
		{"centering", statusIn{Connected: true, Task: task(t, `{"State":"CENTERING_TARGET"}`)}, "Centering Target…"},
		{"centering slew", statusIn{Connected: true, Task: task(t, `{"State":"CENTERING_TARGET","CenteringInfo":{"CenteringStatus":"SLEWING"}}`)}, "Slewing…"},
		{"centering align", statusIn{Connected: true, Task: task(t, `{"State":"CENTERING_TARGET","CenteringInfo":{"CenteringStatus":"ALIGNING"}}`)}, "Aligning…"},
		{"centering focus", statusIn{Connected: true, Task: task(t, `{"State":"CENTERING_TARGET","CenteringInfo":{"CenteringStatus":"FOCUSING"}}`)}, "Focusing…"},
		{"acquiring", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_OBJECT"}`)}, "Acquiring Image…"},
		{"stacking", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_OBJECT","ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","StackDepth":12,"TotalTime":120.5}]}}`)}, "Total Stacked Images: 12\nTotal Integration Time: 120 s."},
		{"list no run", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST"}`)}, "Running Imaging Schedule…"},
		{"list index", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST","ImagingInfo":{"ImageUuid":"u","TotalObjects":3,"NumObjectsRemaining":1}}`)}, "Imaging Object 2 of 3"},
		{"list running", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST","ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","ObjectName":"M 42","StackingStatus":"RUNNING","TotalTime":60,"RemainingTime":3725}]}}`)}, "Imaging M 42\n 1:02:05 remaining"},
		{"list waiting", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST","ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","StackingStatus":"WAITING","MinimumStartTime":"22:00"}]}}`)}, "Waiting to begin imaging Object at 22:00"},
		{"list moving", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST","ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","ObjectName":"M 1","StackingStatus":"MOVING_TO_OBJECT"}]}}`)}, "Moving to M 1"},
		{"list failed", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST","ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","ObjectName":"M 1","StackingStatus":"FAILED"}]}}`)}, "Failed to image M 1"},
		{"recapture", statusIn{Connected: true, Task: task(t, `{"State":"RECAPTURE_OBJECT"}`)}, "Recapturing Scheduled Object…"},
		{"snapshot", statusIn{Connected: true, Task: task(t, `{"State":"SAMPLE_CAPTURE"}`)}, "Capturing Snapshot…"},
		{"video", statusIn{Connected: true, Task: task(t, `{"State":"VIDEO_CAPTURE"}`)}, "Recording Video…"},
		{"init", statusIn{Connected: true, Task: task(t, `{"State":"INITIALIZING"}`)}, "Initializing"},
		{"init step", statusIn{Connected: true, Task: task(t, `{"State":"INITIALIZING","InitializationInfo":{"PercentageComplete":40,"CurrentStep":"MOVING_MOUNT"}}`)}, "Initializing: 40%, Moving…"},
		{"init pct", statusIn{Connected: true, Task: task(t, `{"State":"INITIALIZING","InitializationInfo":{"PercentageComplete":5,"CurrentStep":"NONE"}}`)}, "Initializing: 5%"},
		{"aligning", statusIn{Connected: true, Task: task(t, `{"State":"ALIGNING"}`)}, "Aligning…"},
		{"aligning pts", statusIn{Connected: true, Task: task(t, `{"State":"ALIGNING","AlignmentInfo":{"NumPoints":3}}`)}, "Aligning: 3 point(s)"},
		{"focusing", statusIn{Connected: true, Task: task(t, `{"State":"FOCUSING"}`)}, "Focusing…"},
		{"polar", statusIn{Connected: true, Task: task(t, `{"State":"POLAR_ALIGNING","PolarAlignInfo":{"AzimuthError":0.0174533,"AltitudeError":-0.00872665}}`)}, "Polar Align Your Origin\nAz. Err: 1.00°, Alt. Err: -0.50°"},
		{"darks", statusIn{Connected: true, Task: task(t, `{"State":"GENERATING_DARKS","DarkGenerationInfo":{"PercentageComplete":30}}`)}, "Generating Darks 30%…"},
		{"flats", statusIn{Connected: true, Task: task(t, `{"State":"GENERATING_FLATS"}`)}, "Generating Flats…"},
		{"flats pct", statusIn{Connected: true, FlatsPercent: new(float64(55)), Task: task(t, `{"State":"GENERATING_FLATS"}`)}, "Generating Flats 55%…"},
		{"factory", statusIn{Connected: true, Task: task(t, `{"State":"FACTORY_COLLIMATION"}`)}, "Factory Calibration…"},
		{"dark current", statusIn{Connected: true, Task: task(t, `{"State":"MEASURING_DARK_CURRENT"}`)}, "Factory Calibration…"},
		{"restore darks", statusIn{Connected: true, Task: task(t, `{"State":"RESTORE_DARKS_TO_FACTORY"}`)}, "Restoring Dark Frames…"},
		{"flash", statusIn{Connected: true, Task: task(t, `{"State":"COPY_ALL_DIRECTORIES_TO_FLASH_DRIVE"}`)}, "Copying all to Flash Drive…"},
		{"zip", statusIn{Connected: true, Task: task(t, `{"State":"ZIPPING_DIRECTORY"}`)}, "Compressing Files…"},
		{"update", statusIn{Connected: true, Task: task(t, `{"State":"UPDATING_SYSTEM"}`)}, "Updating System…"},
		{"fan", statusIn{Connected: true, Task: task(t, `{"State":"RUNNING_FAN_TEST"}`)}, "Running Fan Test…"},
		{"mosaic", statusIn{Connected: true, Task: task(t, `{"State":"CAPTURING_MOSAIC"}`)}, "Capturing Mosaic…"},
		{"mosaic tiles", statusIn{Connected: true, Task: task(t, `{"State":"CAPTURING_MOSAIC","ImagingInfo":{"MosaicMetadata":{"TilesTotal":9,"TilesCompleted":4}}}`)}, "Captured 4 of 9 Tiles"},
		{"composing", statusIn{Connected: true, Task: task(t, `{"State":"COMPOSING_MOSAIC","ComposingMosaicInfo":{"PercentComplete":62.7}}`)}, "Composing Mosaic 62%…"},
		{"unknown state", statusIn{Connected: true, Task: task(t, `{"State":"SOMETHING_NEW"}`)}, ""},
	} {
		if got := statusText(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// K: the progress bar shows the exposure in progress while imaging, and
// the run's progress for a schedule whose exposure is not known.
func TestProgress(t *testing.T) {
	run := `{"State":"IMAGING_LIST","ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","StackingStatus":"RUNNING","TotalTime":30,"RemainingTime":90}]}}`
	for _, c := range []struct {
		name string
		in   statusIn
		want float64
	}{
		{"idle", statusIn{Connected: true, Task: task(t, `{"State":"IDLE"}`), Exposure: new(float64(10)), Exposed: new(float64(5))}, -1},
		{"object", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_OBJECT"}`), Exposure: new(float64(10)), Exposed: new(2.5)}, 0.25},
		{"object, exposure unknown", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_OBJECT"}`)}, -2}, // indeterminate
		{"list", statusIn{Connected: true, Task: task(t, run)}, 0.25},
		{"list, exposure known", statusIn{Connected: true, Task: task(t, run), Exposure: new(float64(10)), Exposed: new(float64(4))}, 0.4},
		{"list, nothing remaining", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_LIST"}`)}, -1},
		{"mosaic tile running", statusIn{Connected: true, Task: task(t, strings.Replace(run, "IMAGING_LIST", "CAPTURING_MOSAIC", 1)), Exposure: new(float64(10)), Exposed: new(float64(1))}, 0.1},
		{"mosaic between tiles", statusIn{Connected: true, Task: task(t, `{"State":"CAPTURING_MOSAIC"}`)}, -1},
		{"overexposed", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_OBJECT"}`), Exposure: new(float64(10)), Exposed: new(float64(11))}, 1},
	} {
		if got := progress(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// L: the image shown is the app's imageForScopeStatus choice.
func TestShownImage(t *testing.T) {
	imaging := task(t, `{"State":"IMAGING_OBJECT"}`)
	idle := task(t, `{"State":"IDLE"}`)
	all := map[string]bool{"live": true, "stacked": true, "final": true}
	for _, c := range []struct {
		name   string
		in     statusIn
		frames map[string]bool
		want   string
	}{
		{"nothing", statusIn{Task: idle}, nil, ""},
		{"live only", statusIn{Task: idle}, map[string]bool{"live": true}, "live"},
		{"imaging with stack", statusIn{Task: imaging}, all, "stacked"},
		{"imaging, no stack yet", statusIn{Task: imaging}, map[string]bool{"live": true, "final": true}, "final"},
		{"idle prefers final", statusIn{Task: idle}, all, "final"},
		{"final downloading", statusIn{Task: idle, DownloadingFinal: true}, all, "stacked"},
		{"final awaited", statusIn{Task: idle, WaitingFinal: true}, all, "stacked"},
		{"stack processing", statusIn{Task: idle, Processing: &origin.ImageReady{}}, all, "stacked"},
		{"stack downloading", statusIn{Task: idle, DownloadingStacked: &origin.ImageReady{}}, map[string]bool{"live": true, "stacked": true}, "stacked"},
		{"idle, old stack", statusIn{Task: idle}, map[string]bool{"live": true, "stacked": true}, "live"},
		{"snapshot to save", statusIn{Task: idle}, map[string]bool{"live": true, "final": true, "snapshot": true}, "snapshot"},
	} {
		if got := shownImage(c.in, c.frames); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// K, L: the "activity" event follows the telescope and the downloads.
func TestActivityEvents(t *testing.T) {
	a, rec, s := connected(t)
	rec.wait(t, "activity", func(d any) bool { return strings.HasPrefix(d.(Activity).Text, "Near-realtime view.") })
	s.SetStatus("TaskController", fake.Msg{"State": "INITIALIZING", "InitializationInfo": fake.Msg{"PercentageComplete": 20, "CurrentStep": "ALIGNING"}})
	s.NotifyStatus("TaskController")
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text == "Initializing: 20%, Aligning…" })
	s.SetFile("live/1.jpg", fake.JPEG(1))
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Image == "live" })
	if got := a.Activity(); got.Image != "live" || got.Text != "Initializing: 20%, Aligning…" || got.Progress != -1 {
		t.Errorf("Activity() = %+v", got)
	}
	a.Disconnect()
	rec.wait(t, "activity", func(d any) bool { return d.(Activity).Text == "No Connection" })
}

// The status button's warning, in the app's updateOriginStatusButton order.
func TestWarning(t *testing.T) {
	idle := `{"State":"IDLE","IsReady":true}`
	for _, c := range []struct {
		name string
		in   statusIn
		want string
	}{
		{"off", statusIn{}, ""},
		{"off, no location", statusIn{NoLocation: true}, "Location Not Set"},
		{"restarting, no location", statusIn{NoLocation: true, RestartSeconds: new(5)}, ""},
		{"fine", statusIn{Connected: true, Task: task(t, idle), Battery: "HIGH", Charger: "CHARGING", DiskLevel: "OK"}, ""},
		{"no location connected", statusIn{Connected: true, NoLocation: true, Task: task(t, idle)}, ""},
		{"polar first", statusIn{Connected: true, Task: task(t, `{"State":"POLAR_ALIGNING"}`), Battery: "CRITICALLY_LOW"}, "Polar Aligning"},
		{"critical", statusIn{Connected: true, Task: task(t, idle), Battery: "CRITICALLY_LOW", LowBandwidth: true}, "Battery Critical!"},
		{"low", statusIn{Connected: true, Task: task(t, idle), Battery: "LOW", LowBandwidth: true}, "Low Battery!"},
		{"slow", statusIn{Connected: true, Task: task(t, idle), Battery: "MED", LowBandwidth: true, Charger: "FAULT"}, "Low Network Bandwidth!"},
		{"charger", statusIn{Connected: true, Task: task(t, idle), Charger: "FAULT", DiskLevel: "LOW"}, "Charger Fault"},
		{"disk", statusIn{Connected: true, Task: task(t, idle), DiskLevel: "LOW", Guiding: true}, "Low Disk Space"},
		{"guiding", statusIn{Connected: true, Task: task(t, idle), Guiding: true}, "Guiding"},
	} {
		if got := warning(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The status screen's task line and the imaging screen's target label,
// with demo mode as the app shows it.
func TestTaskTextAndTargetLabel(t *testing.T) {
	for _, c := range []struct {
		name         string
		in           statusIn
		task, target string
	}{
		{"ready", statusIn{Connected: true, Task: task(t, `{"State":"IDLE","IsReady":true}`)}, "Ready To Image", ""},
		{"demo", statusIn{Connected: true, Selected: true, Task: task(t, `{"State":"IDLE","IsReady":true,"IsFakeInitialized":true}`)}, "Ready To Image\n(Demo Mode)", "Demo Mode"},
		{"demo, no target", statusIn{Connected: true, Task: task(t, `{"State":"IDLE","IsReady":true,"IsFakeInitialized":true}`)}, "Ready To Image\n(Demo Mode)", ""},
		{"pointed at", statusIn{Connected: true, PointedAt: "M 31", Task: task(t, `{"State":"IDLE","IsReady":true}`)}, "Ready To Image", "M 31"},
		{"slow first", statusIn{Connected: true, LowBandwidth: true, Task: task(t, `{"State":"IDLE","IsReady":true}`)}, "Low Network Bandwidth!", ""},
		{"needs init", statusIn{Connected: true, Task: task(t, `{"State":"IDLE"}`)}, "Needs Initialization", ""},
		{"imaging", statusIn{Connected: true, Task: task(t, `{"State":"IMAGING_OBJECT","IsFakeInitialized":true,"ImagingInfo":{"ImageUuid":"u","ObjectInfoList":[{"Uuid":"u","ObjectName":"M 42"}]}}`)}, "Acquiring Image…", "Imaging: M 42"},
		{"off", statusIn{}, "No Connection", ""},
	} {
		screen := c.in
		screen.StatusScreen = true
		if got := statusText(screen); got != c.task {
			t.Errorf("%s: task %q, want %q", c.name, got, c.task)
		}
		if got := targetLabel(c.in); got != c.target {
			t.Errorf("%s: target %q, want %q", c.name, got, c.target)
		}
	}
}
