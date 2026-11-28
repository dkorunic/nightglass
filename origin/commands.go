// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// SlewRates are the Origin app's four manual speeds (native table 2, 4, 6, 9).
var SlewRates = []int{2, 4, 6, 9}

// Focuser travel (Origin.java ORIGIN_MIN/MAX_FOCUSER_POSITION).
const (
	FocuserMin = 0
	FocuserMax = 60000
)

// ErrNoLocation: the native app also skips initialization at 0°, 0°.
var ErrNoLocation = errors.New("origin: observer location not set")

func rad(deg float64) float64 { return deg * math.Pi / 180 }

// Site is the observer for RunInitialize: degrees (east positive) and an
// IANA time zone such as "Europe/Zagreb". Fake is the app's demo mode: an
// initialization for indoor testing that skips the sky.
type Site struct {
	Zone           string
	LatDeg, LonDeg float64
	Fake           bool
}

// Initialize starts the telescope's own initialization and alignment at t.
// Date and time go out as local time in site.Zone, as the app sends them.
func (c *Client) Initialize(ctx context.Context, site Site, t time.Time) error {
	if math.Abs(site.LatDeg) < 1e-7 && math.Abs(site.LonDeg) < 1e-7 {
		return ErrNoLocation
	}
	if site.Zone == "" {
		return errors.New("origin: time zone not set")
	}
	loc, err := time.LoadLocation(site.Zone)
	if err != nil {
		return fmt.Errorf("origin: time zone %q: %w", site.Zone, err)
	}
	lt := t.In(loc)
	// Sent without waiting, as the native library's commands (any error
	// reaches subscribers).
	err = c.Post(ctx, "TaskController", "RunInitialize", map[string]any{
		"Date": lt.Format("02 01 2006"), "Time": lt.Format("15:04:05"), "TimeZone": site.Zone,
		"Latitude": rad(site.LatDeg), "Longitude": rad(site.LonDeg), "FakeInitialize": site.Fake,
	})

	return err
}

// GotoRaDec slews to a J2000 position in degrees. The Origin takes J2000
// (native SlewCelestronOrigin sends its input unchanged), so this never precesses.
func (c *Client) GotoRaDec(ctx context.Context, raDeg, decDeg float64) error {
	if c.Imaging() {
		return ErrImaging
	}
	// Not waited for, as native SlewCelestronOrigin.
	err := c.Post(ctx, "Mount", "GotoRaDec", map[string]any{"Ra": rad(raDeg), "Dec": rad(decDeg)})

	return err
}

func validRate(r int) bool { return r == 0 || slices.Contains(SlewRates, max(r, -r)) }

// Slew moves the mount at a signed SlewRates speed per axis (0 stops that
// axis): positive altRate is up, positive azmRate is azimuth increasing.
func (c *Client) Slew(ctx context.Context, altRate, azmRate int) error {
	if !validRate(altRate) || !validRate(azmRate) {
		return fmt.Errorf("origin: slew rates %d, %d: want 0 or ±%v", altRate, azmRate, SlewRates)
	}
	if (altRate != 0 || azmRate != 0) && c.Imaging() {
		return ErrImaging // a stop always goes out
	}
	// Not waited for, as native MoveCelestronOrigin.
	err := c.Post(ctx, "Mount", "Slew", map[string]any{"AltRate": altRate, "AzmRate": azmRate})

	return err
}

// StopSlew stops manual motion. Like the app, it first halts a running
// CENTERING_TARGET task, which would otherwise slew right back.
func (c *Client) StopSlew(ctx context.Context) error {
	if c.TaskState() == "CENTERING_TARGET" {
		if err := c.Post(ctx, "TaskController", "HaltTasks", nil); err != nil {
			return err
		}
	}

	return c.Slew(ctx, 0, 0)
}

// FocusMove runs the focuser continuously (Focuser/Move, as Celestron's web
// app's Move In/Out; not in the app): v in -3..3, positive moving in, 0
// stopping it.
func (c *Client) FocusMove(ctx context.Context, v int) error {
	if v < -3 || v > 3 {
		return fmt.Errorf("origin: focus speed %d outside -3..3", v)
	}
	_, err := c.Send(ctx, "Focuser", "Move", map[string]any{"Value": v})

	return err
}

// ErrImaging is the native app's DVC_BUSY_ERROR: no go-to or slew while
// the telescope is imaging.
var ErrImaging = errors.New("origin: the telescope is busy imaging; stop imaging first")

// imagingStates are the states Origin.isImagingState counts (2, 4, 5, 6, 7).
var imagingStates = []string{"CAPTURING_MOSAIC", "IMAGING_LIST", "IMAGING_OBJECT", "RECAPTURE_OBJECT", "RECAPTURE_MOSAIC_TILE"}

// IsImagingState reports whether a TaskController State is an imaging run.
func IsImagingState(state string) bool { return slices.Contains(imagingStates, state) }

// Imaging reports whether the newest TaskController State is an imaging run.
func (c *Client) Imaging() bool { return IsImagingState(c.TaskState()) }

// TaskState is the newest TaskController State, "" before the first status.
func (c *Client) TaskState() string {
	var ts TaskStatus
	if m, ok := c.Last("TaskController/GetStatus"); ok {
		_ = m.Decode(&ts)
	}

	return ts.State
}

// SetTracking turns sidereal tracking on or off.
func (c *Client) SetTracking(ctx context.Context, on bool) error {
	_, err := c.Send(ctx, "Mount", "EnableTracking", map[string]any{"Value": on})

	return err
}

// FocusTo moves the focuser to an absolute position.
func (c *Client) FocusTo(ctx context.Context, pos int) error {
	if pos < FocuserMin || pos > FocuserMax {
		return fmt.Errorf("origin: focuser position %d outside %d..%d", pos, FocuserMin, FocuserMax)
	}
	_, err := c.Send(ctx, "Focuser", "MoveTo", map[string]any{"Value": pos})

	return err
}

// FocusBy moves the focuser by delta from its last reported position,
// clamped to the travel.
func (c *Client) FocusBy(ctx context.Context, delta int) error {
	m, ok := c.Last("Focuser/GetStatus")
	if !ok {
		var err error
		if m, err = c.Send(ctx, "Focuser", "GetStatus", nil); err != nil {
			return err
		}
	}
	var fs FocuserStatus
	if err := m.Decode(&fs); err != nil {
		return err
	}

	return c.FocusTo(ctx, min(max(fs.Position+delta, FocuserMin), FocuserMax))
}

// AutoFocus runs the app's autofocus: terrestrial in snapshot mode, else
// infinity autofocus starting from focus position start (0: where the Origin chooses).
func (c *Client) AutoFocus(ctx context.Context, terrestrial bool, start int) error {
	var err error
	if terrestrial {
		_, err = c.Send(ctx, "TaskController", "RunTerrestrialAutoFocus", nil)
	} else {
		var p map[string]any
		if start > 0 { // 0: from where the Origin chooses (after a filter change)
			p = map[string]any{"StartingPosition": start}
		}
		_, err = c.Send(ctx, "TaskController", "RunInfinityAutoFocus", p)
	}

	return err
}

// Imaging is one RunImaging request, built as the app builds it. It images
// where the telescope points (go to the target first): Name, Mag and
// SizeArcmin only describe the target. ExposureSec and ISO are sent only in
// Manual mode; otherwise the telescope chooses. A nil Mag or a zero
// SizeArcmin is unknown and left out. Track is set for a Moon or planet
// target (the app's TargetTrack). Uuid is generated when empty.
type Imaging struct {
	Mag                  *float64
	Name                 string
	Uuid                 string
	Track                []TrackPoint
	SizeArcmin           float64
	TotalDurationSec     float64 // > 0: the Origin ends the run itself after this long
	ExposureSec          float64
	ISO                  int
	DitherDistance       int
	DitherInterval       int
	Manual               bool
	SaveRaw              bool
	StopOnPlateSolveFail bool
	Dither               bool
}

// TrackPoint is one place of a moving target: J2000 degrees at Julian date
// JD. On the wire it is radians, as every angle.
type TrackPoint struct{ RADeg, DecDeg, JD float64 }

// NewUUID returns a random (version 4) UUID, as java.util.UUID.randomUUID.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RunImaging starts imaging at the telescope's current position.
func (c *Client) RunImaging(ctx context.Context, im Imaging) error {
	if im.Uuid == "" {
		im.Uuid = NewUUID()
	}
	p := map[string]any{
		"Uuid": im.Uuid, "Name": im.Name, "SaveRawImage": im.SaveRaw,
		"ContinueImagingIfStackingFails": !im.StopOnPlateSolveFail,
	}
	if im.Manual {
		p["ExposureTime"], p["ISO"] = im.ExposureSec, im.ISO
	}
	if im.Dither {
		p["DitherDistance"], p["DitherInterval"] = im.DitherDistance, im.DitherInterval
	}
	if im.Mag != nil { // the app leaves out only an infinite (unknown) magnitude
		p["ObjectMagnitude"] = *im.Mag
	}
	if im.SizeArcmin > 0 {
		// Twice the object's full size, in radians: the app sends
		// ARCSEC_TO_RAD(size1) * 2, and size1 is SkySafari's full angular
		// size (CDeepSkyObject::GetDiameter divides it by 206264.8 only).
		p["ObjectSize"] = 2 * rad(im.SizeArcmin/60)
	}
	if im.TotalDurationSec > 0 {
		// A key of the app's RunImaging payload that the app itself never
		// sets; Origin Live View uses it for timed runs (seconds).
		p["TotalDuration"] = im.TotalDurationSec
	}
	if len(im.Track) > 0 {
		p["TargetTrack"] = wireTrack(im.Track)
	}
	_, err := c.Send(ctx, "TaskController", "RunImaging", p)

	return err
}

// wireTrack is a TargetTrack as the Origin takes it: J2000 radians.
func wireTrack(track []TrackPoint) []map[string]any {
	out := make([]map[string]any, len(track))
	for i, t := range track {
		out[i] = map[string]any{"Ra": rad(t.RADeg), "Dec": rad(t.DecDeg), "JulianDate": t.JD}
	}

	return out
}

// TrackMovingTarget has the mount follow track (Mount/TrackMovingTarget, as
// Celestron's web app tracks satellites; not used by the app). Not waited
// for, as GotoRaDec.
func (c *Client) TrackMovingTarget(ctx context.Context, track []TrackPoint) error {
	return c.Post(ctx, "Mount", "TrackMovingTarget", map[string]any{"TargetTrack": wireTrack(track)})
}

// CancelImaging discards the imaging run (the app's cancelImaging); use
// FinishImaging to keep it.
func (c *Client) CancelImaging(ctx context.Context) error {
	_, err := c.Send(ctx, "TaskController", "CancelImaging", nil)

	return err
}

// FinishImaging ends the run and asks for its final stacked master, as the
// app's finishImagingAndRequestFinalStackedMaster: HaltTasks, then
// GetFinalStackedMaster for the task status's ImageUuid (else fallback).
// The master arrives as an ImageServer/NewImageReady FINAL_STACKED_MASTER.
func (c *Client) FinishImaging(ctx context.Context, fallbackUuid string) error {
	// Read the run before halting: the halt's status update clears it.
	uuid := fallbackUuid
	var ts TaskStatus
	if m, ok := c.Last("TaskController/GetStatus"); ok && m.Decode(&ts) == nil && ts.ImagingInfo.ImageUuid != "" {
		uuid = ts.ImagingInfo.ImageUuid
	}
	if uuid == "" {
		return errors.New("origin: no imaging run to finish")
	}
	// Back to back, as the app: the final image is asked for whatever the
	// halt answers (errors reach subscribers).
	if err := c.Post(ctx, "TaskController", "HaltTasks", nil); err != nil {
		return err
	}

	return c.Post(ctx, "ImageServer", "GetFinalStackedMaster", map[string]any{"ImageUuid": uuid})
}

// GetFinalStackedMaster asks for the final master of the run uuid.
func (c *Client) GetFinalStackedMaster(ctx context.Context, uuid string) error {
	_, err := c.Send(ctx, "ImageServer", "GetFinalStackedMaster", map[string]any{"ImageUuid": uuid})

	return err
}

// AvailableStackedMasters returns which of uuids still have a stacked master
// on the telescope, in the order given.
func (c *Client) AvailableStackedMasters(ctx context.Context, uuids []string) ([]string, error) {
	m, err := c.Send(ctx, "ImageServer", "GetAvailableStackedMasters", map[string]any{"UuidList": uuids})
	if err != nil {
		return nil, err
	}
	var r struct{ AvailableStackedMasters map[string]any }
	if err := m.Decode(&r); err != nil {
		return nil, err
	}
	var out []string
	for _, u := range uuids {
		if _, ok := r.AvailableStackedMasters[u]; ok {
			out = append(out, u)
		}
	}

	return out, nil
}

// Filter is the installed filter the telescope reports ("Clear", "Nebula").
func (c *Client) Filter(ctx context.Context) (string, error) {
	m, err := c.Send(ctx, "Camera", "GetFilter", nil)
	if err != nil {
		return "", err
	}
	var r struct{ Filter string }
	err = m.Decode(&r)

	return r.Filter, err
}

// SetFilter tells the telescope which filter is installed.
func (c *Client) SetFilter(ctx context.Context, filter string) error {
	_, err := c.Send(ctx, "Camera", "SetFilter", map[string]any{"Filter": filter})

	return err
}

// HaltTasks stops whatever task is running.
func (c *Client) HaltTasks(ctx context.Context) error {
	return c.Post(ctx, "TaskController", "HaltTasks", nil) // not waited for, as the native library
}

// Capture is the app's camera mode: Manual (with Snapshot for manual live
// view) or automatic, where the telescope chooses exposure and ISO.
type Capture struct {
	Manual, Snapshot bool
	ExposureSec      float64
	ISO              int
}

// ApplyCapture sets the live view mode as the app's
// updateScopeWithManualCaptureParamsIfNecessary does: manual live view
// (SetEnableManual, then the parameters) only in Manual + Snapshot mode,
// else SetEnableAuto; then it asks for the mode back.
func (c *Client) ApplyCapture(ctx context.Context, cp Capture) error {
	if cp.Manual && cp.Snapshot {
		if _, err := c.Send(ctx, "LiveStream", "SetEnableManual", nil); err != nil {
			return err
		}
		if _, err := c.Send(ctx, "Camera", "SetCaptureParameters", map[string]any{"Exposure": cp.ExposureSec, "ISO": cp.ISO}); err != nil {
			return err
		}
	} else if _, err := c.Send(ctx, "LiveStream", "SetEnableAuto", nil); err != nil {
		return err
	}
	_, err := c.Send(ctx, "LiveStream", "GetEnableManual", nil)

	return err
}

// isoValues are OriginApi$ISOValueEnum.
var isoValues = []int{100, 200, 500, 1000, 2000, 5000, 10000, 20000}

// SupportedISOs are the ISO values within the camera's range, all of them
// before the camera has reported one (native GetSupportedScopeISOs).
func SupportedISOs(ci CameraInfo) []int {
	if ci.MaxIso <= 0 {
		return isoValues
	}
	var out []int
	for _, v := range isoValues {
		if v >= ci.MinIso && v <= ci.MaxIso {
			out = append(out, v)
		}
	}

	return out
}

// ClampISO keeps a supported ISO and replaces any other with the camera's
// default, 200 before it is known (ISOSettings.clampToSupportedISOValue).
func ClampISO(iso int, ci CameraInfo) int {
	if slices.Contains(SupportedISOs(ci), iso) {
		return iso
	}
	if ci.DefaultIso > 0 {
		return ci.DefaultIso
	}

	return 200
}

// ExposureLimits are the app's manual exposure bounds in seconds
// (ExposureSettingsFragment): the camera's range, at least 1 s outside
// snapshot mode; off a wedge at most 30 s outside snapshot mode, on a wedge
// 12 h. onWedge is nil before the mount configuration is known.
func ExposureLimits(ci CameraInfo, onWedge *bool, snapshot bool) (float64, float64) {
	lo := ci.MinExposure
	if lo == 0 {
		lo = 1
	}
	if !snapshot && lo < 1 {
		lo = 1
	}
	hi := ci.MaxExposure
	if hi == 0 {
		hi = 30
	}
	switch {
	case onWedge == nil:
	case *onWedge:
		hi = 43200
	case !snapshot && hi > 30:
		hi = 30
	}

	return lo, hi
}

// CameraInfo is the newest Camera/GetCameraInfo.
func (c *Client) CameraInfo() (CameraInfo, bool) {
	var ci CameraInfo
	m, ok := c.Last("Camera/GetCameraInfo")
	if ok {
		_ = m.Decode(&ci)
	}

	return ci, ok
}

// MountConfig is the newest Mount/GetMountConfig.
func (c *Client) MountConfig() (MountConfig, bool) {
	var mc MountConfig
	m, ok := c.Last("Mount/GetMountConfig")
	if ok {
		_ = m.Decode(&mc)
	}

	return mc, ok
}
