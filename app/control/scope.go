// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
)

// CatalogInfo is one list for the catalog switcher.
type CatalogInfo struct {
	Name  string
	Count int
}

// Row is one target for the target panel. RA/Dec are J2000 degrees; Mag and
// Size at or below 0 are unknown.
type Row struct {
	Catalog, Name, Description, Type, Constellation string
	RA, Dec, Mag, Size                              float64
}

// Catalogs lists every catalog with its entry count, the Moon and planets
// first.
func (a *App) Catalogs() []CatalogInfo {
	out := make([]CatalogInfo, 0, len(a.cat.Lists())+1)
	out = append(out, CatalogInfo{catalog.SolarSystem, len(catalog.Bodies)})
	for _, l := range a.cat.Lists() {
		out = append(out, CatalogInfo{l, len(a.cat.Get(l))})
	}

	return out
}

// lists are the lists Targets(list) covers, in order.
func (a *App) lists(list string) []string {
	if list == "" {
		return append([]string{catalog.SolarSystem}, a.cat.Lists()...)
	}

	return []string{list}
}

// targets returns list's entries; the Moon and planets are where they are at t.
func (a *App) targets(list string, t time.Time) []catalog.Target {
	if list != catalog.SolarSystem {
		return a.cat.Get(list)
	}
	s := a.Settings()
	out := make([]catalog.Target, 0, len(catalog.Bodies))
	for _, b := range catalog.Bodies {
		ra, dec, mag, _ := catalog.Body(b, t, s.LatDeg, s.LonDeg)
		typ := "Planet"
		if b == "Moon" {
			typ = "Moon"
		}
		out = append(out, catalog.Target{Name: b, Type: typ, RADeg: ra, DecDeg: dec, Mag: mag})
	}

	return out
}

// Targets returns the rows of list, or of every list for "". The choice is
// remembered.
func (a *App) Targets(list string) []Row {
	rows := a.rows(list)
	// Remembered only when it changed: each update rewrites settings.json.
	if (list == "" || list == catalog.SolarSystem || a.cat.Get(list) != nil) && a.Settings().Catalog != list {
		a.update(func(s *Settings) { s.Catalog = list })
	}

	return rows
}

// AllTargets returns every list's rows, as Targets("") without changing the
// remembered list: the gallery's Change Target… searches them.
func (a *App) AllTargets() []Row { return a.rows("") }

func (a *App) rows(list string) []Row {
	now := a.now()
	var rows []Row
	for _, l := range a.lists(list) {
		for _, t := range a.targets(l, now) {
			rows = append(rows, Row{l, t.Name, t.Description, t.Type, t.Constellation, t.RADeg, t.DecDeg, t.Mag, t.Size})
		}
	}

	return rows
}

// Positions returns altitude and azimuth (degrees), interleaved, for each
// row Targets(list) returns, now and from the saved location; nil without
// a location.
func (a *App) Positions(list string) []float32 {
	s := a.Settings()
	if !s.HasLocation() {
		return nil
	}
	now := a.now()
	var out []float32
	for _, l := range a.lists(list) {
		for _, t := range a.targets(l, now) {
			alt, az := catalog.AltAz(t.RADeg, t.DecDeg, now, s.LatDeg, s.LonDeg)
			out = append(out, float32(alt), float32(az))
		}
	}

	return out
}

// SunMoon is the app's Tonight → Sun & Moon card (TonightListRowSunMoonVH):
// times as "15:04" at the site, "" for one that does not happen tonight
// ("Does not rise", "Does not set"); MoonText is its "N% illuminated, D
// days old".
type SunMoon struct {
	SunSet, AstroDusk, AstroDawn, SunRise string
	MoonRise, MoonSet                     string
	MoonPhase, MoonText                   string
}

// Tonight returns the Sun & Moon card for the saved location.
func (a *App) Tonight() (SunMoon, error) {
	s := a.Settings()
	if !s.HasLocation() {
		return SunMoon{}, errors.New("set your observing location in Settings first")
	}
	loc := siteZone(s) // a zone file read: once, not per time shown
	n := catalog.Tonight(a.now().In(loc), s.LatDeg, s.LonDeg)
	hm := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}

		return t.In(loc).Format("15:04")
	}

	return SunMoon{
		SunSet: hm(n.SunSet), AstroDusk: hm(n.AstroDusk), AstroDawn: hm(n.AstroDawn), SunRise: hm(n.SunRise),
		MoonRise: hm(n.MoonRise), MoonSet: hm(n.MoonSet), MoonPhase: n.MoonPhase,
		MoonText: fmt.Sprintf("%.0f%% illuminated, %.2f days old", n.MoonIllum*100, n.MoonAge),
	}, nil
}

// GotoBody goes to the Moon or a planet where it is now, as Goto.
func (a *App) GotoBody(name string) error {
	s := a.Settings()
	now := a.now()
	ra, dec, _, ok := catalog.Body(name, now, s.LatDeg, s.LonDeg)
	if !ok {
		return fmt.Errorf("unknown solar system body %q", name)
	}
	if err := a.Goto(ra, dec); err != nil {
		return err
	}
	// Not app parity: the app's go-to leaves the Moon or a planet to drift at
	// the sidereal rate until imaging sends its track. An aligned Origin is
	// given an hour's track (361 places 10 s apart, as Celestron's web app
	// tracks slow targets); unaligned it refuses one, so none is sent.
	var ms origin.MountStatus
	c, err := a.cl() // the go-to went out; a link dropped since sends no track
	if err == nil {
		if m, ok := c.Last("Mount/GetStatus"); ok {
			_ = m.Decode(&ms)
		}
	}
	if !ms.IsAligned {
		return nil
	}

	return c.TrackMovingTarget(context.Background(), catalog.TrackSpan(name, now, s.LatDeg, s.LonDeg, 361, 10*time.Second))
}

// Goto slews to a J2000 position (degrees). Like the native
// SlewCelestronOrigin it refuses a target below the minimum slew altitude
// (TEL_OUTSIDE_SLEW_LIMITS), and one above the web client's maximum when set.
func (a *App) Goto(ra, dec float64) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	s := a.Settings()
	if !s.HasLocation() {
		return errors.New("set your observing location in Settings first")
	}
	// The app's "Command Failure" alerts: below the horizon (goToRADec,
	// -201), else outside the slew limits (native SlewCelestronOrigin, -215).
	switch alt, _ := catalog.AltAz(ra, dec, a.now(), s.LatDeg, s.LonDeg); {
	case alt < 0:
		return errors.New("alert:Command Failure: The scope can't slew to the target coordinates because they are below the horizon.")
	case alt < s.SlewLimitMinDeg, s.SlewLimitMaxDeg > 0 && alt > s.SlewLimitMaxDeg:
		return errors.New("alert:Command Failure: The scope can't slew to the target coordinates because they are outside the user defined slew limits.")
	}
	if err := c.GotoRaDec(context.Background(), ra, dec); errors.Is(err, origin.ErrImaging) {
		return errShown // native returns a busy code Java shows nothing for
	} else if err != nil {
		return err
	}

	return nil
}

// ImageRequest is the target selected when the Image button is pressed:
// a row's Catalog and Name, or none. The telescope images where it points;
// the selection only names the run when it is in the field of view.
type ImageRequest struct {
	Catalog, Name string
	DurationMin   float64 // > 0: a timed run, which the Origin ends after this many minutes
}

// fovRadius is the app's skyObjectWithinScopeFOV limit, radians (about 1.05°).
const fovRadius = 0.018282985074626862

// imagingTarget is the target a run is named after, as the app's
// setupImagingTargetObject: the selected one when the telescope points
// within fovRadius of it, else the one getImagingTargetIDForRADec finds
// there; ok is false when there is none and the run goes unnamed.
func (a *App) imagingTarget(r ImageRequest, raDeg, decDeg float64, now time.Time) (catalog.Target, bool) {
	near := func(t catalog.Target) float64 { return separation(raDeg, decDeg, t.RADeg, t.DecDeg) }
	if r.Name != "" {
		for _, t := range a.targets(r.Catalog, now) {
			if t.Name == r.Name && near(t) < fovRadius {
				return t, true
			}
		}
	}
	// The app prefers the objects of tonight's schedule, then Origin's best
	// objects, then any deep-sky object, then a star, then the nearest. The
	// Moon, the planets and the showpiece lists stand in for its best
	// objects (a table compiled into the app).
	tier := func(l string, t catalog.Target) int {
		switch {
		case l == catalog.SolarSystem || l == "Messier" || l == "Caldwell":
			return 0
		case t.Type == "Duplicate":
			return 4
		case t.Type == "*" || t.Type == "**":
			return 2
		}

		return 1
	}
	var best catalog.Target
	bestTier, bestSep := 5, math.Inf(1)
	for _, l := range a.lists("") {
		for _, t := range a.targets(l, now) {
			// The separation is at least the difference in Dec: most targets
			// are ruled out without the trigonometry (1.9 ms for all of
			// them). Twice the radius keeps acos rounding out of it.
			if math.Abs(t.DecDeg-decDeg) > 2*fovRadius*180/math.Pi {
				continue
			}
			d := near(t)
			if d >= fovRadius {
				continue
			}
			if k := tier(l, t); k < bestTier || k == bestTier && d < bestSep {
				best, bestTier, bestSep = t, k, d
			}
		}
	}

	return best, bestTier < 4
}

// separation is the angle in radians between two J2000 positions (degrees).
func separation(ra1, dec1, ra2, dec2 float64) float64 {
	r := math.Pi / 180
	c := math.Sin(dec1*r)*math.Sin(dec2*r) + math.Cos(dec1*r)*math.Cos(dec2*r)*math.Cos((ra1-ra2)*r)

	return math.Acos(max(-1, min(1, c)))
}

// capture is the app's effective camera setup: an unsupported ISO becomes
// the camera default and the exposure (the snapshot one in snapshot mode)
// is kept within the app's limits.
func (a *App) capture(c *origin.Client) (origin.Capture, float64, int) {
	s := a.Settings()
	ci, _ := c.CameraInfo()
	var wedge *bool
	if mc, ok := c.MountConfig(); ok {
		wedge = &mc.OnWedge
	}
	exp := s.ExposureSec
	if s.Snapshot {
		exp = s.SnapshotExposureSec
	}
	lo, hi := origin.ExposureLimits(ci, wedge, s.Snapshot)
	exp = min(max(exp, lo), hi)
	iso := origin.ClampISO(s.ISO, ci)

	return origin.Capture{Manual: s.ManualImaging, Snapshot: s.Snapshot, ExposureSec: exp, ISO: iso}, exp, iso
}

// limitExposures is the app's ExposureSettingsFragment.clampValueForUnits
// for the imaging and the snapshot exposure: one above the limit becomes
// the limit, explained off a wedge; one below becomes the minimum,
// explained outside snapshot mode.
func (a *App) limitExposures(c *origin.Client, s Settings) Settings {
	ci, _ := c.CameraInfo()
	mc, okm := c.MountConfig()
	var wedge *bool
	if okm {
		wedge = &mc.OnWedge
	}
	for _, e := range []struct {
		v        *float64
		snapshot bool
	}{{&s.ExposureSec, false}, {&s.SnapshotExposureSec, true}} {
		lo, hi := origin.ExposureLimits(ci, wedge, e.snapshot)
		switch {
		case *e.v > hi:
			*e.v = hi
			if okm && !mc.OnWedge {
				a.emit("alert", Alert{"", "Use optional wedge for exposures over " + exposureText(hi) + "."})
			}
		case *e.v < lo:
			*e.v = lo
			if !e.snapshot {
				a.emit("alert", Alert{"", "Use SnapShot mode for exposures under " + exposureText(lo) + "."})
			}
		}
	}

	return s
}

// exposureText is the app's exposureTimeStringForTimeInSeconds.
func exposureText(sec float64) string {
	switch {
	case sec < 9e-4:
		return fmt.Sprintf("%.1fμs", sec*1e6)
	case sec < 0.09:
		return fmt.Sprintf("%.1fms", sec*1e3)
	}

	return fmt.Sprintf("%.1fs", sec)
}

// Image is the app's capture button (captureImageButtonTouched): refused
// while the telescope moves or the Origin is busy; in snapshot mode it takes
// a snapshot. Otherwise it starts imaging at the telescope's current
// position, as startImagingObject: refused below 0°, warned (not refused)
// above 85° off a wedge, where field rotation may make it fail.
func (a *App) Image(r ImageRequest) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if a.moving(c) {
		return errMoving
	}
	// Busy, or (outside snapshot mode) not initialized: the app's
	// startImageCapture returns without a word (isOriginConnectedAndInitialized).
	if a.uiState(c) != "IDLE" {
		return errShown
	}
	if a.Settings().Snapshot {
		a.force("SAMPLE_CAPTURE")
		if err := a.snapshot(c); err != nil {
			a.force("")

			return err
		}

		return nil
	}
	if !ready(c) {
		return errShown
	}
	var ms origin.MountStatus
	m, ok := c.Last("Mount/GetStatus")
	if !ok || m.Decode(&ms) != nil {
		return errors.New("the telescope's position is not known yet")
	}
	alt := ms.Alt * 180 / math.Pi
	if alt < MinAlt {
		return fmt.Errorf("alert:Target Close to Zenith: Origin can only image objects between %d° and %d° altitude.", MinAlt, MaxAlt)
	}
	if mc, _ := c.MountConfig(); alt > MaxAlt && !mc.OnWedge {
		a.emit("alert", Alert{"Target Close to Zenith", "Object is close to zenith and field rotation may cause imaging to fail. If so, try using manual camera settings to shorten the exposure time."})
	}
	s := a.Settings()
	_, exp, iso := a.capture(c)
	if r.DurationMin < 0 {
		return errors.New("the run's duration must not be negative")
	}
	im := origin.Imaging{
		Uuid: origin.NewUUID(), Manual: s.ManualImaging, ExposureSec: exp, ISO: iso, TotalDurationSec: r.DurationMin * 60,
		SaveRaw: s.SaveRaw, StopOnPlateSolveFail: s.StopOnPlateSolveFail,
		Dither: s.Dither, DitherDistance: s.DitherDistance, DitherInterval: s.DitherInterval,
	}
	now := a.now()
	// The app's position is read only while the Origin is ready and aligned
	// (native ReadCelestronOrigin); unaligned coordinates name nothing.
	if t, ok := a.imagingTarget(r, ms.Ra*180/math.Pi, ms.Dec*180/math.Pi, now); ok && ms.IsAligned {
		im.Name, im.SizeArcmin = t.Name, max(t.Size, 0)
		a.mu.Lock()
		a.runTargets[im.Uuid] = t // addStackedMasterTargetObjectIDForUUID
		a.mu.Unlock()
		if t.Type == "Planet" || t.Type == "Moon" {
			im.Mag, im.Track = &t.Mag, catalog.Track(t.Name, now, s.LatDeg, s.LonDeg)
		} else if catalog.KnownMag(t.Mag) {
			im.Mag = &t.Mag
		}
	}
	uuid := im.Uuid
	// startImagingObject drops the masters and the stacked download before
	// sending (resetStackedMasterImaging).
	a.mu.Lock()
	delete(a.frames, kindStacked)
	delete(a.frames, kindFinal)
	if a.cancelStacked != nil {
		a.cancelStacked()
	}
	a.mu.Unlock()
	a.force("IMAGING_OBJECT")
	if err := c.RunImaging(context.Background(), im); err != nil {
		a.force("")

		return err
	}
	// The run is remembered until its final image is downloaded
	// (addImageUUIDToCachedList).
	a.mu.Lock()
	a.run = uuid
	a.timedRun, a.timedSeen = "", false
	if r.DurationMin > 0 {
		a.timedRun, a.timedHost = uuid, c.Host()
	}
	a.mu.Unlock()
	a.remember(uuid)
	a.refresh()

	return nil
}

// runUuid is the current run: the task status's ImageUuid, else the one
// this app started.
func (a *App) runUuid(c *origin.Client) string {
	var ts origin.TaskStatus
	if m, ok := c.Last("TaskController/GetStatus"); ok && m.Decode(&ts) == nil && ts.ImagingInfo.ImageUuid != "" {
		return ts.ImagingInfo.ImageUuid
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.run
}

// FinishImaging ends the run and fetches its final image, as the app's
// finishImageCapture: only while imaging one object or a list, and once a
// stacked image has arrived.
func (a *App) FinishImaging() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if a.moving(c) { // captureImageButtonTouched, before the end-imaging dialog
		return errMoving
	}
	if c.TaskState() != "IMAGING_OBJECT" {
		return errors.New("the Origin is not imaging an object")
	}
	a.mu.Lock()
	_, stacked := a.frames[kindStacked]
	a.mu.Unlock()
	if !stacked {
		return errors.New("no stacked image yet: wait for the first one, or cancel imaging")
	}
	a.mu.Lock()
	a.waitingFinal, a.timedRun = true, ""
	if a.cancelStacked != nil {
		a.cancelStacked() // finishImagingAndRequestFinalStackedMaster cancels it
	}
	a.dlStacked = nil // so the final image is not dropped as arriving mid-download
	a.mu.Unlock()
	a.refresh()
	if err := c.FinishImaging(context.Background(), a.runUuid(c)); err != nil {
		a.mu.Lock()
		a.waitingFinal = false
		a.mu.Unlock()
		a.refresh()

		return err
	}

	return nil
}

// CancelImaging discards the run, as the app's cancelImaging: no final
// image, and the run is forgotten.
func (a *App) CancelImaging() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if c.TaskState() == "RECAPTURE_OBJECT" {
		return errShown // the app offers no Cancel for a schedule's recapture
	}
	uuid := a.runUuid(c)
	imagingObject := c.TaskState() == "IMAGING_OBJECT"
	// cancelImaging forgets the run and drops both masters and the stacked
	// download, and ends a wait for the final image, whatever the Origin
	// answers.
	if imagingObject {
		a.forget(uuid)
	}
	a.mu.Lock()
	delete(a.frames, kindStacked)
	delete(a.frames, kindFinal)
	if a.cancelStacked != nil {
		a.cancelStacked()
	}
	a.dlStacked = nil
	a.waitingFinal, a.timedRun = false, ""
	a.mu.Unlock()
	a.refresh()

	return c.CancelImaging(context.Background())
}

// DownloadPending asks for the final image of an earlier run (the app's
// "Download Now").
func (a *App) DownloadPending(uuid string) error {
	return a.with(func(c *origin.Client) error { return c.GetFinalStackedMaster(context.Background(), uuid) })
}

// ForgetPending stops offering a run's image ("Don't Warn About This
// Image"), or every pending image for "" ("Don't Warn Again").
func (a *App) ForgetPending(uuid string) {
	if uuid == "" {
		// "Don't Warn Again" forgets every remembered run but the current
		// one, offered or not (SkySafariActivityOrigin$8).
		current := ""
		if c, err := a.cl(); err == nil {
			var ts origin.TaskStatus
			if m, ok := c.Last("TaskController/GetStatus"); ok && m.Decode(&ts) == nil && ts.State == "IMAGING_OBJECT" {
				current = ts.ImagingInfo.ImageUuid
			}
		}
		a.mu.Lock()
		a.offered = nil
		a.mu.Unlock()
		for _, u := range a.Settings().PendingImages {
			if u != current {
				a.forget(u)
			}
		}

		return
	}
	a.forget(uuid)
}

// manualLabel is the imaging screen's manual mode summary for the saved
// manual ISO (else the camera's default) and the exposure of the mode in use.
func (a *App) manualLabel() string {
	s := a.Settings()
	if !s.ManualImaging {
		return ""
	}
	iso, exp := s.ISO, s.ExposureSec
	if s.Snapshot {
		exp = s.SnapshotExposureSec
	}
	if iso == 0 {
		iso = 200
		if c, err := a.cl(); err == nil {
			if ci, _ := c.CameraInfo(); ci.DefaultIso > 0 {
				iso = ci.DefaultIso
			}
		}
	}

	return manualText(iso, exp)
}

// Initialize is the imaging screen's initialize button
// (originCancelInitializationButtonTapped): shown only while the Origin is
// idle and not initialized, it starts initialization; while initializing
// it cancels. Re-initializing an initialized Origin is Reinitialize.
func (a *App) Initialize() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	switch st := c.TaskState(); {
	case st == "INITIALIZING":
		if err := c.HaltTasks(context.Background()); err != nil {
			return err
		}
		a.mu.Lock()
		a.cancellingInit = true
		a.mu.Unlock()
		a.refresh()

		return nil
	case st != "IDLE":
		return errBusy
	case ready(c):
		return errors.New("Origin is already initialized: re-initialize it from Origin settings")
	}

	return a.runInitialize(c)
}

// Reinitialize is Origin settings' "Re-Initialize Now"
// (reInitializeScopeNowButtonTapped): idle only, initialized or not.
func (a *App) Reinitialize() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if err := idle(c); err != nil {
		return err
	}

	return a.runInitialize(c)
}

func (a *App) runInitialize(c *origin.Client) error {
	s := a.Settings()

	return c.Initialize(context.Background(), origin.Site{LatDeg: s.LatDeg, LonDeg: s.LonDeg, Zone: s.TimeZone}, a.now())
}

// Slew moves the mount; see origin.Client.Slew for the rates.
func (a *App) Slew(altRate, azmRate int) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	moving := altRate != 0 || azmRate != 0
	if moving && c.Imaging() {
		return errShown // refused before sending, silently as native: no stop owed
	}
	// The app's slew pad works only while the Origin is idle
	// (ScopeControlViewController.updateUIForOriginStatus).
	if moving && c.TaskState() != "IDLE" {
		return errBusy
	}
	// A moving slew is owed a stop from the moment it may reach the mount,
	// even when its response is lost; only an acknowledged stop clears that.
	if moving {
		a.mu.Lock()
		a.slewing = true
		a.mu.Unlock()
		a.refresh()
	}
	if err := c.Slew(context.Background(), altRate, azmRate); err != nil {
		return err
	}
	if !moving {
		a.mu.Lock()
		a.slewing = false
		a.mu.Unlock()
		a.refresh()
	}

	return nil
}

// StopSlew stops manual motion. The frontend calls it on button or key
// release and on window blur. Disconnected, the stop is owed: Connect's
// state handler sends it when the link returns.
func (a *App) StopSlew() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if err := c.StopSlew(context.Background()); err != nil {
		return err
	}
	a.mu.Lock()
	a.slewing = false
	a.mu.Unlock()
	a.refresh()

	return nil
}

// FocusJog runs the focuser while a button is held (Focuser/Move v, -3..3,
// positive in; 0 stops), only while idle as the focus buttons. Like a
// slew, a moving jog is owed a stop until one is acknowledged: a drop
// leaves it to Connect's state handler.
func (a *App) FocusJog(v int) error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if v < -3 || v > 3 {
		return fmt.Errorf("focus speed %d outside -3..3", v)
	}
	if v != 0 {
		if c.TaskState() != "IDLE" {
			return errFocusBusy
		}
		a.mu.Lock()
		a.jogging = true
		a.mu.Unlock()
	}
	if err := c.FocusMove(context.Background(), v); err != nil {
		return err
	}
	if v == 0 {
		a.mu.Lock()
		a.jogging = false
		a.mu.Unlock()
	}

	return nil
}

// HaltTasks stops any running task.
func (a *App) HaltTasks() error {
	return a.with(func(c *origin.Client) error { return c.HaltTasks(context.Background()) })
}

// AutoFocus runs autofocus as the app does: only while the Origin is idle;
// terrestrial in snapshot mode, else infinity from the current position.
func (a *App) AutoFocus() error {
	return a.with(func(c *origin.Client) error {
		if c.TaskState() != "IDLE" {
			return errShown // the app's autofocus control is disabled then
		}
		if a.Settings().Snapshot {
			return c.AutoFocus(context.Background(), true, 0)
		}
		var fs origin.FocuserStatus
		m, ok := c.Last("Focuser/GetStatus")
		if !ok {
			var err error
			if m, err = c.Send(context.Background(), "Focuser", "GetStatus", nil); err != nil {
				return err
			}
		}
		if err := m.Decode(&fs); err != nil {
			return err
		}

		// From the shown position, clamped to the limits (FocusOptionsFragment).
		l := a.FocusLimits()

		return c.InfinityAutoFocus(context.Background(), min(max(fs.Position, l.Min), l.Max), !a.Settings().RoughFocus)
	})
}

// ApplyCapture sets the saved camera mode on the telescope (see
// origin.Client.ApplyCapture). It runs on connect and after Settings change.
func (a *App) ApplyCapture() error {
	return a.with(func(c *origin.Client) error {
		if c.Imaging() { // the app's camera settings are locked while imaging
			return origin.ErrImaging
		}
		cp, _, _ := a.capture(c)

		return c.ApplyCapture(context.Background(), cp)
	})
}

// ISOChoice is the ISO picker (ISOSettings): the camera's values, the one
// in use (the saved one, else the camera's default) and whether the camera
// has reported its range, without which the app disables the picker.
type ISOChoice struct {
	Values []int
	Value  int
	Known  bool
}

// ISOChoices returns the ISO picker's state.
func (a *App) ISOChoices() ISOChoice {
	var ci origin.CameraInfo
	if c, err := a.cl(); err == nil {
		ci, _ = c.CameraInfo()
	}

	return ISOChoice{origin.SupportedISOs(ci), origin.ClampISO(a.Settings().ISO, ci), ci.MinIso > 0 && ci.MaxIso > 0}
}

// SupportedISOs lists the ISO values the connected camera accepts.
func (a *App) SupportedISOs() []int {
	c, err := a.cl()
	if err != nil {
		return origin.SupportedISOs(origin.CameraInfo{})
	}
	ci, _ := c.CameraInfo()

	return origin.SupportedISOs(ci)
}

func (a *App) with(f func(*origin.Client) error) error {
	c, err := a.cl()
	if err != nil {
		return err
	}

	return f(c)
}

// GotoTarget goes to a catalogue row: at its framing point for the large
// nebulae the app frames itself (CSkyChart::slewTelescope's imaging centre
// override; Nightglass computes its own, catalog.Framing), else at its
// position.
func (a *App) GotoTarget(list, name string) error {
	for _, t := range a.cat.Get(list) {
		if t.Name != name {
			continue
		}
		if ra, dec, ok := a.cat.Framing(t); ok {
			return a.Goto(ra, dec)
		}

		return a.Goto(t.RADeg, t.DecDeg)
	}

	return fmt.Errorf("%s is not in %s", name, list)
}
