// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Package control is the GUI's Go side. Wails binds every exported method
// of App to the frontend (app/main.go); this package has no Wails import,
// so it tests anywhere.
package control

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // final masters may be JPEG
	"image/png"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
)

// The Origin's imaging altitude window (Origin.java ORIGIN_MIN/MAX_ALTITUDE_FOR_IMAGING).
const (
	MinAlt = 0
	MaxAlt = 85
)

// errNoLocation is the app's "Precise Location Unavailable" refusal to
// connect without a location.
var errNoLocation = errors.New("Precise Location Unavailable: Nightglass was unable to determine your location accurately enough to display your sky. Please set your location manually and attempt to reconnect.")

// errShown is an error already shown to the user (an "Origin Error" alert,
// as the app's relay) or one the app shows nothing for; the frontend
// shows nothing more.
var errShown = errors.New("shown:")

// ignoredCode are the codes the app drops on any message (UNKNOWN_COMMAND,
// IMAGING_LIST_DOESNT_EXIST, OBJECT_NOT_PRESENT_IN_DATABASE, its own UNKNOWN).
func ignoredCode(code int) bool { return code == -2 || code == -300 || code == -301 || code == -404 }

// shownErr is the client's MapError: an Origin error the relay shows, or
// one it ignores, becomes errShown.
func shownErr(err error) error {
	var ce *origin.CommandError
	if errors.As(err, &ce) && (ignoredCode(ce.Code) || origin.Localized(ce.Code)) {
		return errShown
	}

	return err
}

// ErrNotConnected is returned by telescope commands with no client.
var ErrNotConnected = errors.New("not connected to a telescope")

// Toast levels.
const (
	toastInfo  = "info"
	toastWarn  = "warn"
	toastError = "error"
)

// Frame kinds: the image slots, the "frame" event's Kind and /frame/<kind>.
const (
	kindLive     = "live"
	kindStacked  = "stacked"
	kindFinal    = "final"
	kindSnapshot = "snapshot"
)

// keyFileTransfer is the Origin's file transfer progress.
const keyFileTransfer = "TaskController/FileTransferStatus"

// Event payloads sent to the frontend.
type (
	// Conn is the "conn" event.
	Conn struct {
		Host      string
		Error     string
		Connected bool
	}
	// Status is the "status" event: one module's latest fields.
	Status struct {
		Fields map[string]any
		Key    string
	}
	// FrameEvent is the "frame" event; the image is at /frame/<Kind>?<Seq>.
	FrameEvent struct {
		Kind string
		Info origin.ImageReady
		Seq  int
	}
	// Toast is the "toast" event.
	Toast struct {
		Level, Message string
	}
	// Orphans is the "orphans" event: earlier runs whose final image is
	// still on the telescope (warnAboutUndownloadedStackedMasterImages).
	Orphans struct {
		Uuids []string
	}
	// Alert is the "alert" event: a message the app shows in an alert box.
	Alert struct {
		Title, Message string
	}
	// Critical is the "critical" event: System/CriticalError. The app asks
	// the user to power cycle the Origin and offers to email Message (when
	// set) to Celestron support.
	Critical struct {
		Message string
	}
)

// App is bound to the frontend. Create it with New.
type App struct {
	tilesAsked  time.Time // when GetMosaicTiles was last asked during a mosaic
	tileAskedAt time.Time
	emit        func(event string, data any)
	cat         *catalog.Set
	httpc       *http.Client
	now         func() time.Time
	tune        func(*origin.Client) // tests shorten timings here

	client *origin.Client
	stop   context.CancelFunc
	frames map[string]origin.Frame
	seq    map[string]int

	restart       *restart                  // waiting for the Origin to restart after an update
	dlStacked     *origin.ImageReady        // stacked master downloading, or nil
	transferPct   *int                      // USB copy progress, until HaltTasks answers
	cancelStacked context.CancelFunc        // ends the stacked download under way
	finalPct      *int                      // the final image's download, percent, while it downloads
	mosaicView    *MosaicView               // the newest GetMosaicTiles answer
	lastFinal     *origin.Frame             // the last final master, after it leaves the screen
	composed      map[string]Composed       // each mosaic's saved composition, by mosaic Uuid
	savedImages   map[string]string         // the file each final image was saved to this session, by run or tile Uuid (mu)
	runTargets    map[string]catalog.Target // the target each run was named after, by Uuid (mu)
	mosaicUuids   map[string]bool           // the mosaic mosaicUuids asked for, by Uuid (mu)
	styledNames   map[string]string         // the label style of run names, by name (mu)
	finals        map[string]finalSeen      // isFinal answers, by gallery path (mu)
	lastAct       *Activity
	discover      func(context.Context) ([]string, error)         // the network search; tests replace it
	inhibit       func(why string, display bool) (release func()) // the OS sleep inhibitor; tests replace it
	release       func()                                          // releases the held inhibitor; nil when none (actMu)
	processing    *origin.ImageReady                              // a master being processed after its download
	fwdDone       chan struct{}                                   // closed when the connection's forward loop ends (mu)
	settingsPath  string
	thumbDir      string // THUMBNAIL images, as <Uuid>.jpg

	// Per connection, reset by Connect.
	run          string       // Uuid of the run this app started
	logs         string       // the logs button's text while logs are fetched
	zipFolder    string       // the folder DownloadFolderZip waits on
	lastFailure  string       // the task failure last shown from TaskController status
	timedRun     string       // a timed run's Uuid, until it ends and its final image is asked for
	timedHost    string       // the Origin running it: a reconnect to it keeps the timer
	composition  string       // the CompositionUuid of a mosaic being composed
	composing    string       // the mosaic whose composition that is
	tileAsked    string       // a tile master asked for (pendingTileMasterUuid)
	forced       string       // the state shown until the Origin reports it (forcedTempStatusForUI) (mu)
	selection    ImageRequest // the selected target (Catalog, Name), for the target label (mu)
	warnings     []string
	speeds       []float64    // last 5 download speeds, MB/s
	offered      []string     // the runs the last "orphans" event offered
	pointed      pointedCache // the last target label lookup (mu)
	settings     Settings
	restartDelay time.Duration // Origin.ORIGIN_UPDATE_AND_RESTART_DELAY; tests shorten it
	retryDelay   time.Duration // the reconnect after ILLEGAL_STATE_OPERATION; tests shorten it
	probeTimeout time.Duration // CheckHost's TCP connect
	searching    int           // network searches running (mu)
	gen          uint64        // counts connects and disconnects, so a slow ConnectOrigin sees the user acted (mu)
	samples      int

	mu sync.Mutex

	actMu       sync.Mutex // orders "activity" events; taken before mu
	saveMu      sync.Mutex // orders settings writes; taken before actMu and mu
	connecting  bool       // a ConnectOrigin runs (mu)
	heldDisplay bool       // the held inhibitor keeps the display on (actMu)
	slewing     bool
	jogging     bool // a FocusJog is owed a stop
	calWarned   bool // "Factory Calibration Incomplete" shown, once per run

	initDone bool // the last task status was a completed initialization

	waitingFinal   bool // FinishImaging sent, final master not here yet
	dlFinal        bool // final master downloading
	saving         bool // final master being saved
	cancellingInit bool // Cancel Initialization sent, still initializing
	joined         bool // the first task status since connecting was seen
	listAsked      bool // a "listready" question is open (suppressWarningAboutUndownloadedImageList)
	timedSeen      bool // the timed run has been seen imaging
	dlComposition  bool // the final image downloading is the composition
	slowWarned     bool
}

// New loads the catalogs and the settings at settingsPath. emit delivers
// events to the frontend.
func New(settingsPath string, emit func(event string, data any)) (*App, error) {
	cat, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Dir(settingsPath)
	}
	a := &App{
		emit: emit, settingsPath: settingsPath, cat: cat, now: time.Now,
		thumbDir: filepath.Join(cache, "nightglass", "MosaicThumbnails"),
		httpc:    &http.Client{Timeout: 60 * time.Second},
		frames:   map[string]origin.Frame{}, seq: map[string]int{}, composed: map[string]Composed{}, savedImages: map[string]string{}, runTargets: map[string]catalog.Target{}, mosaicUuids: map[string]bool{}, styledNames: map[string]string{}, finals: map[string]finalSeen{},
		restartDelay: 180 * time.Second, retryDelay: 10 * time.Second, probeTimeout: 2 * time.Second,
		discover: func(ctx context.Context) ([]string, error) { return origin.Discover(ctx, origin.DiscoveryTime) },
		inhibit:  inhibitSleep,
	}
	a.settings, err = LoadSettings(settingsPath)
	if err != nil {
		a.warnings = append(a.warnings, err.Error())
	}

	return a, nil
}

// Launched counts a launch (for Quick Start, QuickStartDue). main calls it
// once Wails has let this instance run, so a second instance, which only
// hands over to the first, writes nothing.
func (a *App) Launched() { a.update(func(s *Settings) { s.LaunchCount++ }) }

// QuickStartDue is the app's showCelestronOriginQuickStart(false): the
// Quick Start opens by itself during the first three launches.
func (a *App) QuickStartDue() bool { return a.Settings().LaunchCount <= 3 }

// Warnings returns problems found at startup, for the frontend to show once.
func (a *App) Warnings() []string { return a.warnings }

// Settings returns the current settings.
func (a *App) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.settings
}

// SaveSettings validates, stores and applies s. Connected, manual
// exposures outside what the Origin allows are brought within it, with the
// app's explanation; the frontend reads the stored Settings back.
func (a *App) SaveSettings(s Settings) error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	// Display and format settings a caller leaves out keep their defaults.
	d := DefaultSettings()
	if s.NightBrightness == 0 {
		s.NightBrightness = d.NightBrightness
	}
	s.TimeFormat, s.DateOrder, s.CoordFormat = cmp.Or(s.TimeFormat, d.TimeFormat), cmp.Or(s.DateOrder, d.DateOrder), cmp.Or(s.CoordFormat, d.CoordFormat)
	if err := s.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	cur := a.settings
	a.mu.Unlock()
	if c, err := a.cl(); err == nil {
		s = a.limitExposures(c, s)
	} else if hi := cmp.Or(cur.MaxExposureSec, d.MaxExposureSec); hi > 0 {
		// Disconnected: the last limit the Origin allowed, and the app's 1 s
		// floor outside snapshot mode (ExposureSettingsFragment).
		s.ExposureSec = min(max(s.ExposureSec, 1), hi)
		s.SnapshotExposureSec = min(s.SnapshotExposureSec, hi)
	}
	s.PendingImages, s.Favorites, s.LaunchCount, s.RemoteVersions = cur.PendingImages, cur.Favorites, cur.LaunchCount, cur.RemoteVersions
	s.Mosaics, s.PendingLists = cur.Mosaics, cur.PendingLists
	s.OriginModel, s.OriginVersion, s.Images = cur.OriginModel, cur.OriginVersion, cur.Images
	s.LastAnnouncement, s.ManualHost, s.EverConnected = cur.LastAnnouncement, cur.ManualHost, cur.EverConnected
	s.Host, s.Catalog = cur.Host, cur.Catalog // set by Connect and Targets
	if c, err := a.cl(); err == nil {
		ci, _ := c.CameraInfo()
		var wedge *bool
		if mc, ok := c.MountConfig(); ok {
			wedge = &mc.OnWedge
		}
		_, s.MaxExposureSec = origin.ExposureLimits(ci, wedge, false) // maximumExposureTime caches it
	} else {
		s.MaxExposureSec = cur.MaxExposureSec
	}
	// The app's camera settings are locked while imaging (AdvancedCameraSettingsMenu).
	if c, err := a.cl(); err == nil && c.Imaging() {
		s.ManualImaging, s.Snapshot, s.ISO = cur.ManualImaging, cur.Snapshot, cur.ISO
		s.ExposureSec, s.SnapshotExposureSec = cur.ExposureSec, cur.SnapshotExposureSec
	}
	// Its dither sliders end at 30 and 10.
	s.DitherDistance, s.DitherInterval = min(max(s.DitherDistance, 1), 30), min(max(s.DitherInterval, 1), 10)
	if err := writeSettings(a.settingsPath, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	a.refresh() // the idle hint follows snapshot mode

	return nil
}

func (a *App) update(f func(*Settings)) {
	// One write at a time, in the order of the changes.
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	f(&a.settings)
	s := a.settings
	a.mu.Unlock()
	if err := writeSettings(a.settingsPath, s); err != nil {
		a.emit("toast", Toast{toastWarn, "could not save settings: " + err.Error()})
	}
}

// Discover lists telescopes found on the network.
func (a *App) Discover() ([]string, error) {
	return a.search(context.Background())
}

// search runs the network search, the status line saying so meanwhile.
func (a *App) search(ctx context.Context) ([]string, error) {
	a.mu.Lock()
	a.searching++
	a.mu.Unlock()
	a.refresh()
	defer func() {
		a.mu.Lock()
		a.searching--
		a.mu.Unlock()
		a.refresh()
	}()

	return a.discover(ctx)
}

// CheckHost is the app's doesOriginExistAtIPAddress for an address typed
// by hand: a TCP connection to it must open (port 80 unless one is given).
// Its error is the app's "Manual IP Address Invalid" alert.
func (a *App) CheckHost(host string) error {
	h, err := origin.NormalizeHost(host)
	if err != nil {
		return err
	}
	if a.probe(context.Background(), h) != nil {
		return fmt.Errorf("Manual IP Address Invalid: Nightglass cannot find your Origin telescope at the IP address you have manually requested (%s).", h)
	}

	return nil
}

// errConnFailure is the app's alert when nothing answers at the address
// (error -10, origin_wirelessconnperscopeerror).
var errConnFailure = Alert{"Connection Failure", "Nightglass can't make a wireless connection to the telescope. Make sure the telescope is powered on and you've joined its Wi-Fi network"}

// probe is the app's TestDeviceTCPSocket: a TCP connection to the
// telescope's web port (80 unless host names one).
func (a *App) probe(ctx context.Context, host string) error {
	addr := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		addr = net.JoinHostPort(host, "80")
	}
	conn, err := (&net.Dialer{Timeout: a.probeTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return conn.Close()
}

// ReconnectNow is "Attempt To Connect" while waiting to reconnect: the
// wait ends and the app connects to the telescope at once.
func (a *App) ReconnectNow() error {
	a.mu.Lock()
	r := a.restart
	a.mu.Unlock()
	if r == nil {
		return errors.New("not waiting to reconnect")
	}

	a.cancelRestart()

	return a.ConnectOrigin() // reconnectOrigin → connectOrigin
}

// Connect connects to host, replacing any current connection, and keeps
// reconnecting until Disconnect.
func (a *App) Connect(host string) error {
	host, err := origin.NormalizeHost(host)
	if err != nil {
		return err
	}
	// The app's locationSufficientForOriginConnection: a location of about
	// 0°, 0° (both within 1e-4 rad) is not one.
	if s := a.Settings(); math.Abs(s.LatDeg) < 1e-4*180/math.Pi && math.Abs(s.LonDeg) < 1e-4*180/math.Pi {
		return errNoLocation
	}
	a.Disconnect()
	c := origin.New(host)
	c.MapError = shownErr
	// The app's openTelescope: a TCP probe (below), then a WebSocket its
	// library keeps redialling until it opens (60 s handshakes, waits of
	// 1 ms growing to 10 s). After a drop the app connects anew
	// (reconnectOrigin → connectOrigin), as Run's end does below.
	c.GiveUp = true
	c.DialTimeout, c.MinBackoff, c.MaxBackoff = 60*time.Second, time.Millisecond, 10*time.Second
	if a.tune != nil {
		a.tune(c)
	}
	// OnState runs on c.Run's goroutine only, so these need no lock.
	wasUp, reported := false, false
	c.OnState = func(ok bool, err error) {
		a.mu.Lock()
		current := a.client == c
		stopSlew := current && ok && a.slewing
		stopJog := current && ok && a.jogging
		a.mu.Unlock()
		// A replaced client's late dial must not reach the GUI or the mount.
		if !current {
			return
		}
		// One event per change of state: a telescope that stays off is
		// reported once, not on every reconnect attempt.
		if !ok && !wasUp && reported {
			return
		}
		// Each drop and reconnect starts afresh, as the app's
		// telescopeConnectionClosed and handleOriginTelescopeConnectionOpened.
		if wasUp != ok {
			a.mu.Lock()
			a.resetLink(ok)
			a.mu.Unlock()
		}
		wasUp, reported = ok, true
		ev := Conn{Connected: ok, Host: host}
		if err != nil {
			ev.Error = err.Error()
		}
		a.emit("conn", ev)
		a.refresh()
		if ok {
			go a.onConnected(c)
		}
		// A drop mid-jog may leave the focuser running: stop it first thing.
		if stopJog {
			go func() {
				if c.FocusMove(context.Background(), 0) == nil {
					a.mu.Lock()
					a.jogging = false
					a.mu.Unlock()
				}
			}()
		}
		// A drop mid-slew may leave the mount moving: stop it first thing.
		if stopSlew {
			go func() {
				if c.StopSlew(context.Background()) == nil {
					a.mu.Lock()
					a.slewing = false
					a.mu.Unlock()
				}
			}()
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	sub := c.Subscribe()
	a.mu.Lock()
	done := make(chan struct{})
	a.client, a.stop, a.fwdDone = c, stop, done
	a.gen++
	a.run, a.dlFinal, a.dlStacked, a.saving, a.logs = "", false, nil, false, ""
	if a.timedHost != host {
		a.timedRun, a.timedSeen = "", false
	}
	a.resetLink(true)
	a.mu.Unlock()
	a.refresh()
	go func() {
		// Nothing answering at the address fails at once with the app's
		// error -10 text (TestDeviceTCPSocket, finishConnectingScope).
		if err := a.probe(ctx, host); err != nil {
			a.mu.Lock()
			current := a.client == c
			a.mu.Unlock()
			if current && ctx.Err() == nil {
				a.disconnect() //nolint:contextcheck // it cancels this connection's ctx
				a.emit("alert", errConnFailure)
			}

			return
		}
		// A telescope that closes the connection normally is disconnected,
		// not redialled (the app's onWebSocketClosedRelay).
		err := c.Run(ctx)
		a.mu.Lock()
		current := a.client == c
		a.mu.Unlock()
		switch {
		case !current:
		case errors.Is(err, origin.ErrClosed):
			a.disconnect() //nolint:contextcheck // it cancels this connection's ctx
		case errors.Is(err, origin.ErrGaveUp):
			// A drop: connect anew, searching unless the address was typed.
			a.disconnect()                            //nolint:contextcheck // it cancels this connection's ctx
			if err := a.ConnectOrigin(); err != nil { //nolint:contextcheck // a new connection, not the cancelled one
				a.emitError(err)
			}
		}
	}()
	go func() {
		defer close(done)
		a.forward(ctx, c, sub)
	}()
	a.update(func(s *Settings) { s.Host = host })

	return nil
}

// resetLink forgets what belongs to one connection: the images (the app's
// resetStackedMasterImaging, resetLiveImage), the wait for a final image,
// transfer and initialization progress, and, on connecting, the download
// speeds, the bandwidth warning and having joined a run. Frame numbers keep
// counting, so the frontend's /frame/<kind>?<seq> URLs never repeat. Call
// with mu held.
func (a *App) resetLink(connecting bool) {
	a.frames = map[string]origin.Frame{}
	a.lastFinal = nil
	a.forced = ""
	a.waitingFinal, a.transferPct, a.cancellingInit = false, nil, false
	if connecting {
		a.speeds, a.samples, a.slowWarned, a.joined = nil, 0, false, false
		a.tileAsked = ""
	}
}

// Disconnect closes the connection, if any, stopping a slew first (spec
// §5): the mount must not keep moving once the app lets go of it. It also
// ends a wait for the Origin to restart.
func (a *App) Disconnect() {
	a.cancelRestart()
	if done := a.disconnect(); done != nil {
		<-done // nothing of that connection's runs after this
	}
}

// disconnect ends the connection. It returns a channel closed once the
// connection's forward loop has ended; forward's own paths call it and
// must not wait for that.
func (a *App) disconnect() <-chan struct{} {
	a.mu.Lock()
	c, stop, owed, jog, done := a.client, a.stop, a.slewing, a.jogging, a.fwdDone
	a.client, a.stop, a.fwdDone = nil, nil, nil
	a.gen++
	if stop != nil {
		// As telescopeConnectionClosed: the images and the waits end with
		// the link (a drop does this in OnState; this one stops it first).
		a.resetLink(false)
	}
	a.transferPct, a.logs, a.zipFolder = nil, "", "" // the telescope's progress ends with the link
	a.mu.Unlock()
	if stop == nil {
		return nil
	}
	if owed && c.Connected() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if c.StopSlew(ctx) == nil {
			a.mu.Lock()
			a.slewing = false
			a.mu.Unlock()
		}
		cancel()
	}
	if jog && c.Connected() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if c.FocusMove(ctx, 0) == nil {
			a.mu.Lock()
			a.jogging = false
			a.mu.Unlock()
		}
		cancel()
	}
	stop()
	a.emit("conn", Conn{})
	a.refresh()

	return done
}

// ServiceShutdown is called by Wails on exit.
func (a *App) ServiceShutdown() error {
	a.Disconnect()

	return nil
}

func (a *App) cl() (*origin.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || !a.client.Connected() {
		return nil, ErrNotConnected
	}

	return a.client, nil
}

// Status returns the latest cached status of every module, for a redraw.
func (a *App) Status() []Status {
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c == nil {
		return nil
	}
	var out []Status
	for _, q := range origin.StatusQueries {
		k := q[0] + "/" + q[1]
		if m, ok := c.Last(k); ok {
			out = append(out, Status{Key: k, Fields: m.Fields()})
		}
	}

	return out
}

// frameKind maps an ImageType to the frame slot the GUI shows.
func frameKind(imageType string) string {
	switch imageType {
	case "LIVE", "ALIGNMENT", "FOCUSING": // the app shows all three as live view
		return kindLive
	case "STACKED_MASTER":
		return kindStacked
	case "FINAL_STACKED_MASTER":
		return kindFinal
	case "SAMPLE_CAPTURE":
		return kindSnapshot
	}

	return ""
}

// forward turns client messages into frontend events and image downloads.
func (a *App) forward(ctx context.Context, c *origin.Client, sub <-chan origin.Message) {
	slots := map[string]*origin.Latest[origin.ImageReady]{}
	for _, k := range []string{kindLive, kindStacked, kindFinal, kindSnapshot} {
		slots[k] = origin.NewLatest(func(info origin.ImageReady) { a.download(ctx, c, k, info) })
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-sub:
			key := m.Key() // built once, not by each case below
			a.logsMessage(ctx, c, m)
			a.zipMessage(ctx, c, m)
			if key == "TaskController/GetStatus" {
				m = a.taskFailure(m) //nolint:contextcheck // as originError
			}
			switch {
			case m.ErrorCode != 0 && origin.Localized(m.ErrorCode):
				// The app's relay shows these and keeps them from its screens.
			case key == "FactoryCalibrationController/GetStatus":
				a.checkCalibration(m)
			case key == "TaskController/GetStatus":
				var ts origin.TaskStatus
				if m.Decode(&ts) == nil {
					a.reported(ts.State) // the forced state ends once reported
				}
				a.checkInitialized(c, m)
				a.joinRun(ctx, c, m)
				a.endTimedRun(ctx, c)
				a.askTiles(ctx, c, m)
				if c.TaskState() != "INITIALIZING" {
					a.mu.Lock()
					a.cancellingInit = false
					a.mu.Unlock()
				}
			case key == "System/UpdateStatus":
				a.emit("status", Status{Key: key, Fields: m.Fields()})
				a.checkUpdate(c, m) //nolint:contextcheck // may disconnect, which cancels ctx
			case (key == "System/GetModel" || key == "System/GetVersion") && m.ErrorCode == 0:
				a.storeModelVersion(m)
				a.emit("status", Status{Key: key, Fields: m.Fields()})
			case key == "TaskController/GetObservingListInfo" && m.ErrorCode == 0:
				a.checkList(m)
			case key == keyFileTransfer:
				var ft struct{ PercentageComplete *int }
				if m.Decode(&ft) == nil && ft.PercentageComplete != nil {
					a.mu.Lock()
					a.transferPct = ft.PercentageComplete
					a.mu.Unlock()
				}
				a.emit("status", Status{Key: key, Fields: m.Fields()})
			case key == "TaskController/HaltTasks" && m.Type == origin.TypeResponse:
				// The app forgets the copy progress here (clearCachedState).
				a.mu.Lock()
				a.transferPct = nil
				a.mu.Unlock()
				a.emit("status", Status{Key: keyFileTransfer, Fields: map[string]any{}})
			}
			switch {
			case key == "ImageServer/NewImageReady":
				var info origin.ImageReady
				if m.Decode(&info) == nil {
					a.imageReady(ctx, c, slots, info)
				}
			case key == "Mosaic/GetMosaicTiles" && m.ErrorCode == 0:
				if view, err := decodeTiles(m); err == nil {
					a.mu.Lock()
					a.mosaicView = &view
					a.mu.Unlock()
				}
			case key == "Disk/LowSpaceWarning":
				a.emit("alert", Alert{"Very low disk space on your Origin device!", lowSpaceText(c, m)})
			case key == "System/CriticalError":
				var ce struct{ CriticalErrorMessage string }
				_ = m.Decode(&ce)
				a.emit("critical", Critical{Message: ce.CriticalErrorMessage})
			case m.ErrorCode != 0 || m.Module() == "Error":
				a.originError(m) //nolint:contextcheck // may disconnect, which cancels ctx
			case strings.HasPrefix(m.Command, "Get"), key == "TaskController/SafeToRemoveFlashDrive":
				a.emit("status", Status{Key: key, Fields: m.Fields()})
			}
			a.refresh()
		}
	}
}

// taskFailure handles a TaskController status that reports a task ending
// early (ErrorCode beside its State, repeated in each ~1 Hz push until the
// next task): the failure goes to the relay once, not every second, and the
// status is returned without it, so its State is taken like any other. A
// clean status ends the failure; the same one after it is shown again.
// Not app parity: Celestron's web app dedupes these pushes the same way.
func (a *App) taskFailure(m origin.Message) origin.Message {
	a.mu.Lock()
	if m.ErrorCode == 0 {
		a.lastFailure = ""
		a.mu.Unlock()

		return m
	}
	sig := fmt.Sprint(m.ErrorCode, " ", m.ErrorMessage)
	repeat := a.lastFailure == sig
	a.lastFailure = sig
	a.mu.Unlock()
	if !repeat {
		a.originError(m)
	}
	m.ErrorCode, m.ErrorMessage = 0, ""

	return m
}

// originError is the app's onOriginMessageRelay for an error-coded
// message. The relay acts only on codes the native library has a message
// for (it passes none otherwise), so of its special cases only -69 is ever
// reached: its -5 reconnect and its -2/-300/-301/-404 branches are dead.
func (a *App) originError(m origin.Message) {
	switch {
	case ignoredCode(m.ErrorCode):
		// No message of the app's own: nothing shown, as below.
	case m.ErrorCode == -69: // POWERING_DOWN_ORIGIN_ON_LIST_COMPLETION
		go func() {
			_ = a.CancelImaging() // forgets the run, as the app's cancelImaging
			a.disconnect()
			a.emit("alert", Alert{"", origin.ErrorText(m.ErrorCode, m.ErrorMessage)})
		}()
	case origin.Localized(m.ErrorCode):
		// The app's handleOriginError alert, for any message, waited for or
		// not; a waiting caller gets errShown (shownErr).
		a.emit("alert", Alert{"Origin Error", origin.ErrorText(m.ErrorCode, m.ErrorMessage)})
	default:
		// No message of the app's own: it shows nothing at the relay.
		slog.Warn("Origin error", "key", m.Key(), "code", m.ErrorCode, "message", m.ErrorMessage)
	}
}

// endTimedRun asks for a timed run's final image once the Origin has ended
// the run itself, as Finish asks for it (nobody pressed Finish).
func (a *App) endTimedRun(ctx context.Context, c *origin.Client) {
	a.mu.Lock()
	uuid := a.timedRun
	if uuid != "" && c.Imaging() {
		a.timedSeen = true // a status still IDLE from before the run started does not end it
	}
	done := uuid != "" && a.timedSeen && !c.Imaging()
	if done {
		a.timedRun, a.timedSeen, a.waitingFinal = "", false, true
	}
	a.mu.Unlock()
	if done {
		a.refresh()
		_ = c.Post(ctx, "ImageServer", "GetFinalStackedMaster", map[string]any{"ImageUuid": uuid})
	}
}

// askTiles asks for the running mosaic's tiles so the status line can name
// the tile: only when the active tile is not among the known ones, at most
// every 5 s (requestMosaicTilesForStatus).
func (a *App) askTiles(ctx context.Context, c *origin.Client, m origin.Message) {
	var ts origin.TaskStatus
	if m.Decode(&ts) != nil || (ts.State != "CAPTURING_MOSAIC" && ts.State != "RECAPTURE_MOSAIC_TILE") {
		return
	}
	uuid := ts.ImagingInfo.ListUuid
	if mm := ts.ImagingInfo.MosaicMetadata; mm != nil && mm.ListUuid != "" {
		uuid = mm.ListUuid
	}
	a.mu.Lock()
	known := a.mosaicView != nil && slices.ContainsFunc(a.mosaicView.Tiles, func(t MosaicTile) bool { return t.Uuid == ts.ImagingInfo.ImageUuid })
	due := uuid != "" && !known && time.Since(a.tilesAsked) >= 5*time.Second
	if due {
		a.tilesAsked = time.Now()
	}
	a.mu.Unlock()
	if due {
		_ = c.Post(ctx, "Mosaic", "GetMosaicTiles", map[string]any{"MosaicUuid": uuid})
	}
}

// thumbnail downloads a THUMBNAIL image and offers it ("thumb" event).
func (a *App) thumbnail(ctx context.Context, c *origin.Client, info origin.ImageReady) {
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second) // MosaicCaptureFragment.downloadBytes
	defer cancel()
	b, err := origin.FetchFile(tctx, a.httpc, c.Host(), info.FileLocation)
	if err != nil {
		slog.Warn("thumbnail", "uuid", info.Uuid, "err", err)
		a.emit("thumb", Thumb{Uuid: info.Uuid, Failed: true})

		return
	}
	if err := a.storeThumb(info.Uuid, b); err != nil {
		slog.Warn("thumbnail", "uuid", info.Uuid, "err", err)
		a.emit("thumb", Thumb{Uuid: info.Uuid, Failed: true})

		return
	}
	a.emit("thumb", Thumb{Uuid: info.Uuid})
}

// thumbPath is where a thumbnail is kept, as the app's MosaicThumbnailStore
// keeps them in a cache folder ("MosaicThumbnails/<uuid>.jpg") rather than
// in memory for the whole session.
func (a *App) thumbPath(uuid string) (string, error) {
	if uuid == "" || safeName(uuid, "") != uuid {
		return "", fmt.Errorf("not a thumbnail name: %q", uuid)
	}

	return filepath.Join(a.thumbDir, uuid+".jpg"), nil
}

// storeThumb keeps a thumbnail, replacing it whole so a reader never sees
// part of one.
func (a *App) storeThumb(uuid string, b []byte) error {
	p, err := a.thumbPath(uuid)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.thumbDir, 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, p)
}

// imageReady routes a NewImageReady: frames to their download slot,
// thumbnails to the mosaic screen.
func (a *App) imageReady(ctx context.Context, c *origin.Client, slots map[string]*origin.Latest[origin.ImageReady], info origin.ImageReady) {
	a.mu.Lock()
	busy := a.dlStacked != nil || a.saving
	a.mu.Unlock()
	switch k := frameKind(info.ImageType); {
	case k != "" && k != kindSnapshot && busy:
		// The app drops live and stacked frames while a stacked master
		// downloads or the final one is saved (handleNewImageReadyWithMessage);
		// snapshots go to the capture screen, which takes them regardless.
	case k != "":
		if k == kindSnapshot {
			a.mu.Lock()
			if a.forced == "SAMPLE_CAPTURE" {
				a.forced = "" // the snapshot is here
			}
			a.mu.Unlock()
		}
		slots[k].Offer(info)
		if k == kindFinal {
			a.finalArrived(ctx, c, info.Uuid)
		}
	case info.ImageType != "THUMBNAIL":
	case info.FileLocation == "":
		a.emit("thumb", Thumb{Uuid: info.Uuid, Failed: true}) // asked again later
	default:
		go a.thumbnail(ctx, c, info)
	}
}

// Thumb is the "thumb" event: a THUMBNAIL image arrived, or (Failed) did
// not, so the frontend may ask for it again (MosaicCaptureFragment$3$1).
type Thumb struct {
	Uuid   string
	Failed bool
}

// finalArrived is MosaicCaptureFragment's FINAL handling: the tile master
// asked for is no longer in flight, and a composition's half-size
// thumbnail is asked for, to preview it on the capture screen.
func (a *App) finalArrived(ctx context.Context, c *origin.Client, uuid string) {
	a.mu.Lock()
	if a.tileAsked == uuid {
		a.tileAsked = ""
	}
	comp := uuid != "" && uuid == a.composition
	a.mu.Unlock()
	if comp {
		_ = c.Post(ctx, "ImageServer", "GetThumbnail", map[string]any{"ImageUuid": uuid, "Scale": 0.5})
	}
}

// joinRun follows the first task status after connecting: joining a run in
// progress, the app asks for its image at once (setupImagingTargetObject)
// instead of waiting for the next stack.
func (a *App) joinRun(ctx context.Context, c *origin.Client, m origin.Message) {
	a.mu.Lock()
	first := !a.joined && a.client == c
	a.joined = true
	a.mu.Unlock()
	var ts origin.TaskStatus
	if !first || a.Settings().Snapshot || m.Decode(&ts) != nil || ts.ImagingInfo.ImageUuid == "" ||
		ts.State != "IMAGING_OBJECT" && ts.State != "IMAGING_LIST" {
		return
	}
	_ = c.Post(ctx, "ImageServer", "GetFinalStackedMaster", map[string]any{"ImageUuid": ts.ImagingInfo.ImageUuid})
}

// checkCalibration is the app's handleOriginNotFactoryCalibratedError: an
// Origin reporting IsCalibrated false gets one alert per app run.
func (a *App) checkCalibration(m origin.Message) {
	var fc struct{ IsCalibrated *bool }
	if m.Decode(&fc) != nil || fc.IsCalibrated == nil || *fc.IsCalibrated {
		return
	}
	a.mu.Lock()
	warned := a.calWarned
	a.calWarned = true
	a.mu.Unlock()
	if !warned {
		a.emit("alert", Alert{"Factory Calibration Incomplete", "Factory Calibration Incomplete"})
	}
}

// lowSpaceText is the app's low disk space alert, with the free space from
// the notification or else the last Disk status.
func lowSpaceText(c *origin.Client, m origin.Message) string {
	var d struct{ FreeBytes float64 }
	_ = m.Decode(&d)
	if d.FreeBytes <= 0 {
		if s, ok := c.Last("Disk/GetStatus"); ok {
			_ = s.Decode(&d)
		}
	}
	text := "Origin may not be able to properly operate until disk space is cleared up."
	if d.FreeBytes > 0 {
		text += fmt.Sprintf("\n\n%.2f GB remaining.", d.FreeBytes/1e9)
	}

	return text
}

// onConnected does what the app does as its imaging screen opens: set the
// camera mode (after waiting briefly for the camera and mount configuration,
// asked for on connect, so the exposure limits are right), question a
// reported nebula filter, and offer earlier runs' undownloaded images.
func (a *App) onConnected(c *origin.Client) {
	if !a.Settings().EverConnected { //nolint:misspell // the app's LAST_SUCCESSFULL_ORIGIN_WIFI
		a.update(func(s *Settings) { s.EverConnected = true })
	}
	// The native CelestronOriginHandleWebSocketOpened tells the Origin its
	// Wi-Fi regulatory domain: the site's country.
	if s := a.Settings(); s.HasLocation() {
		_ = c.Post(context.Background(), "Network", "SetRegulatoryDomain", map[string]any{"CountryCode": origin.CountryCode(s.LatDeg, s.LonDeg)})
	}
	_ = c.Post(context.Background(), "System", "HasUpdateAvailable", map[string]any{"ForceCheck": true})
	go a.offerUpdateOnConnect(c) // handleOriginTelescopeConnectionOpened
	go a.coolOnConnect(c)
	for range 60 {
		_, okc := c.CameraInfo()
		_, okm := c.MountConfig()
		if okc && okm {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.mu.Lock()
	current := a.client == c
	a.mu.Unlock()
	if !current {
		return
	}
	cp, _, _ := a.capture(c)
	if err := c.ApplyCapture(context.Background(), cp); err != nil {
		// The app posts these and shows nothing of its own: an Origin error
		// has had its alert from the relay (errShown), anything else is logged.
		if !errors.Is(err, errShown) {
			slog.Warn("could not set the camera mode", "err", err)
		}
	}
	if f, err := c.Filter(context.Background()); err == nil && f == "Nebula" {
		a.emit("nebula", struct{}{})
	}
	a.queryPending(c)
	a.queryLists(c)
}

// queryPending is the app's queryForOrphanedImages: earlier runs whose final
// image is still on the telescope (the current run aside) are offered.
func (a *App) queryPending(c *origin.Client) {
	pending := a.Settings().PendingImages
	var ts origin.TaskStatus
	if m, ok := c.Last("TaskController/GetStatus"); ok {
		_ = m.Decode(&ts)
	}
	// The run in progress is not offered, only a single object's
	// (IMAGING_OBJECT): a schedule's runs can be.
	pending = slices.DeleteFunc(slices.Clone(pending), func(u string) bool { return ts.State == "IMAGING_OBJECT" && u == ts.ImagingInfo.ImageUuid })
	if len(pending) == 0 {
		return
	}
	if avail, err := c.AvailableStackedMasters(context.Background(), pending); err == nil && len(avail) > 0 {
		a.mu.Lock()
		a.offered = avail
		a.mu.Unlock()
		a.emit("orphans", Orphans{Uuids: avail})
	}
}

// FilesOpened is what the app's file manager does as it opens: it asks for
// the disk's status and for undownloaded runs again.
func (a *App) FilesOpened() error {
	return a.with(func(c *origin.Client) error {
		_ = c.Post(context.Background(), "Disk", "GetStatus", nil)
		go a.queryPending(c)

		return nil
	})
}

// download fetches one frame; on failure the previous frame stays.
func (a *App) download(ctx context.Context, c *origin.Client, kind string, info origin.ImageReady) {
	// One download per kind runs at a time (forward's Latest slots).
	switch kind {
	case kindStacked:
		// After Finish only the final image is wanted (downloadPendingStackedMasterImage),
		// and FinishImaging cancels a stacked download under way.
		a.mu.Lock()
		skip := a.waitingFinal
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		a.cancelStacked = cancel
		a.mu.Unlock()
		defer cancel()
		if skip {
			return
		}
		a.setDownloading(kind, &info)
		defer a.setDownloading(kind, nil)
	case kindFinal:
		a.setDownloading(kind, &info)
		defer a.setDownloading(kind, nil)
	case kindSnapshot:
		// Downloaded at once (downloadSnapShotImageFromURL).
	default:
		if !a.waitForMasters(ctx) {
			return
		}
	}
	start := time.Now()
	dctx, cancel := context.WithTimeout(ctx, downloadTimeouts[kind])
	var progress func(done, total int64)
	if kind == kindFinal { // the app's "Downloading Image — N%" (onDownloadFileProgress)
		progress = func(done, total int64) {
			if total <= 0 {
				return
			}
			pct := int(done * 100 / total)
			a.mu.Lock()
			changed := a.finalPct == nil || *a.finalPct != pct
			a.finalPct = &pct
			a.mu.Unlock()
			if changed {
				a.refresh()
			}
		}
		defer func() {
			a.mu.Lock()
			a.finalPct = nil
			a.mu.Unlock()
		}()
	}
	b, err := origin.FetchFileProgress(dctx, a.httpc, c.Host(), info.FileLocation, progress)
	cancel()
	took := time.Since(start) // the download alone: decoding is not network time
	if ctx.Err() != nil {
		return // disconnected or replaced meanwhile: not this telescope's frame anymore
	}
	var f origin.Frame
	if err == nil {
		if kind == kindStacked || kind == kindFinal {
			a.setProcessing(&info) // postProcessingStackedMasterImage
		}
		// With the app's "AI Post Process" off, masters are only decoded:
		// no stretch, no auto-crop (ImageDownloadAndProcess).
		s := a.Settings()
		switch {
		case !s.PostProcess:
			f, err = origin.DecodePlain(info, b)
		case (kind == kindStacked || kind == kindFinal) && s.AutoCrop:
			f, err = origin.DecodeCrop(info, b, 32) // processUIImage's auto-crop
		default:
			f, err = origin.Decode(info, b)
		}
		a.setProcessing(nil)
	}
	if err != nil {
		// The app only logs a failed download (ImageDownloadAndProcess); the
		// previous frame stays. A snapshot's failure is shown.
		slog.Warn("Error Downloading Image", "kind", kind, "file", info.FileLocation, "err", err)
		if kind == kindSnapshot {
			a.emit("toast", Toast{toastError, "Unable to load Image"})
		}
		if kind == kindFinal && errors.Is(err, origin.ErrJPEG2000) {
			a.keepFinalFile(info, b, ".jp2")
		}

		return
	}
	// The app leaves out speeds measured while the Origin downloads an update.
	var us struct{ Status string }
	if m, ok := c.Last("System/UpdateStatus"); ok {
		_ = m.Decode(&us)
	}
	if ms := took.Milliseconds(); ms > 0 && us.Status != "UPDATE_DOWNLOADING" {
		a.noteSpeed(float64(len(f.Original)) / float64(ms) * 0.001) // MB/s, as the app measures
	}
	a.mu.Lock()
	a.frames[kind] = f
	a.seq[kind]++
	seq := a.seq[kind]
	if kind == kindFinal {
		a.waitingFinal = false
	}
	a.mu.Unlock()
	a.emit("frame", FrameEvent{Kind: kind, Seq: seq, Info: info})
	a.refresh() // the shown image may change
	if kind == kindFinal {
		a.autoSave(ctx, f)
	}
}

// downloadTimeouts are the app's per-image timeouts: downloadPendingLiveImage,
// downloadPendingStackedMasterImage (also the final master) and
// downloadSnapShotImageFromURL.
var downloadTimeouts = map[string]time.Duration{
	kindLive: 10 * time.Second, kindStacked: 45 * time.Second, kindFinal: 45 * time.Second, kindSnapshot: 60 * time.Second,
}

// setDownloading marks a final or stacked master download as running (info
// set) or over (nil).
// setProcessing marks a master as being processed after its download.
func (a *App) setProcessing(info *origin.ImageReady) {
	a.mu.Lock()
	a.processing = info
	a.mu.Unlock()
	a.refresh()
}

func (a *App) setDownloading(kind string, info *origin.ImageReady) {
	a.mu.Lock()
	if kind == kindFinal {
		a.dlFinal = info != nil
		a.dlComposition = info != nil && a.composition != "" && info.Uuid == a.composition
		if info != nil {
			a.waitingFinal = false // the app stops waiting as the download starts
		}
	} else {
		a.dlStacked = info
	}
	a.mu.Unlock()
	a.refresh()
}

// keepFinalFile saves a final image Nightglass cannot show (an Origin set to
// JPEG 2000 by ImageServer/SetFsmFormat) into SaveDir as it came, named as
// saveFrame names a final image, so the run is not lost. Not app parity: the
// app only ever receives TIFF.
func (a *App) keepFinalFile(info origin.ImageReady, b []byte, ext string) {
	s := a.Settings()
	name := safeName(info.ObjectName, safeName(info.Uuid, "origin")) + "-" + a.now().In(siteZone(s)).Format("20060102-150405")
	err := os.MkdirAll(s.SaveDir, 0o755)
	p := ""
	if err == nil {
		p, err = writeNew(filepath.Join(s.SaveDir, name), ext, b)
	}
	if err != nil {
		a.emit("toast", Toast{toastError, "Unable to save the JPEG 2000 final image: " + err.Error()})

		return
	}
	a.emit("toast", Toast{toastWarn, "Saved " + p + " (JPEG 2000, which Nightglass cannot show; set Origin → Final Image Format to TIFF or PNG)"})
}

// autoSave saves a final master as it arrives, as the app's
// saveFinalStackedMasterWithPayload does.
func (a *App) autoSave(ctx context.Context, f origin.Frame) {
	a.mu.Lock()
	a.saving = true
	a.mu.Unlock()
	a.refresh()
	p, err := a.saveFinal(f)
	a.mu.Lock()
	a.saving = false
	// Saved or not, the masters are dropped and live view returns
	// (resetStackedMasterImaging), unless the link was replaced meanwhile:
	// the frames are then another connection's.
	if ctx.Err() == nil {
		delete(a.frames, kindStacked)
		delete(a.frames, kindFinal)
		a.lastFinal = &f // for "Save final image again"
	}
	a.mu.Unlock()
	a.mu.Lock()
	if err == nil && f.Info.Uuid != "" {
		a.savedImages[f.Info.Uuid] = filepath.Base(p) // a tile's Show Image
	}
	if a.composition == f.Info.Uuid {
		a.composition = "" // pendingMosaicCompositionUUID is cleared, saved or not
		if err == nil {
			a.composed[a.composing] = Composed{Uuid: f.Info.Uuid, Image: filepath.Base(p)}
		}
	}
	a.mu.Unlock()
	if err == nil {
		// Only a saved run is forgotten; an unsaved one is offered again
		// on the next connect (SkySafariActivityImaging$5).
		a.forget(f.Info.Uuid)
	}
	a.refresh()
	if err != nil {
		slog.Warn("could not save the final image", "err", err)
		a.emit("alert", Alert{"Could Not Save Image To Device!", "Could not save this image to this computer, but a copy is saved on the telescope."})

		return
	}
	a.emit("toast", Toast{toastInfo, "Saved " + p})
	a.emit("saved", Saved{Name: filepath.Base(p)}) // the app opens the image (ImageDetailFragment)
}

// Saved is the "saved" event: a final image was saved under Name in SaveDir.
type Saved struct {
	Name string
}

// waitForMasters holds a live download while a stacked or final master is
// downloading or awaited, as the app's handleLiveImageWithPayload defers it.
// It reports false when ctx ends first.
func (a *App) waitForMasters(ctx context.Context) bool {
	for {
		a.mu.Lock()
		busy := a.dlFinal || a.dlStacked != nil || a.waitingFinal
		a.mu.Unlock()
		if !busy {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// noteSpeed records one download speed (MB/s) and warns once per connection
// when, after more than 60 downloads, the last 5 average at most 0.2 MB/s
// (the app's isOriginAverageBandwidthTooLow).
func (a *App) noteSpeed(mbps float64) {
	a.mu.Lock()
	a.speeds = append(a.speeds, mbps)
	if len(a.speeds) > 5 {
		a.speeds = a.speeds[1:]
	}
	a.samples++
	avg := a.avgSpeed()
	warn := !a.slowWarned && a.lowBandwidth()
	if warn {
		a.slowWarned = true
	}
	a.mu.Unlock()
	if warn {
		// The app's alert (SkySafariActivityImaging$2), "mobile device" there.
		a.emit("alert", Alert{"Low Network Bandwidth!", fmt.Sprintf("Communication between this computer and Origin is very slow (%.2f MB/s). A minimum of %.2f MB/s is recommended.\n\nThis may be a result of Origin being too far from your wifi router or this computer, or if too many users are connected to your Origin.\n\nYou may experience poor performance or unexpected behaviour.", avg, 0.2)})
	}
}

// avgSpeed is the average of the last 5 download speeds, MB/s
// (getAverageImageDownloadSpeedMBpS). Call with mu held.
func (a *App) avgSpeed() float64 {
	if len(a.speeds) == 0 {
		return 0
	}
	var sum float64
	for _, s := range a.speeds {
		sum += s
	}

	return sum / float64(len(a.speeds))
}

// lowBandwidth is isOriginAverageBandwidthTooLow: more than 60 downloads,
// the last 5 averaging at most 0.2 MB/s. Call with mu held.
func (a *App) lowBandwidth() bool {
	avg := a.avgSpeed()

	return a.samples > 60 && avg > 0 && avg <= 0.2
}

// remember and forget keep Settings.PendingImages.
func (a *App) remember(uuid string) {
	a.update(func(s *Settings) {
		if !slices.Contains(s.PendingImages, uuid) {
			s.PendingImages = append(s.PendingImages, uuid)
		}
	})
}

func (a *App) forget(uuid string) {
	if uuid == "" {
		return
	}
	a.update(func(s *Settings) {
		s.PendingImages = slices.DeleteFunc(slices.Clone(s.PendingImages), func(u string) bool { return u == uuid })
	})
}

// ServeHTTP serves the newest frame of a kind at /frame/<kind>, and a saved
// image at /frame/saved/<name> for the gallery; Wails mounts App there and
// strips the prefix.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if uuid, ok := strings.CutPrefix(strings.Trim(r.URL.Path, "/"), "thumb/"); ok {
		p, err := a.thumbPath(uuid)
		var b []byte
		if err == nil {
			b, err = os.ReadFile(p)
		}
		if err != nil {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(b))
		_, _ = w.Write(b)

		return
	}
	if name, ok := strings.CutPrefix(strings.Trim(r.URL.Path, "/"), "saved/"); ok {
		p, err := a.galleryPath(name)
		if err != nil {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Cache-Control", "no-store") // an image may be overwritten
		http.ServeFile(w, r, p)

		return
	}
	a.mu.Lock()
	f, ok := a.frames[strings.Trim(r.URL.Path, "/")]
	a.mu.Unlock()
	if !ok {
		http.NotFound(w, r)

		return
	}
	w.Header().Set("Content-Type", f.DisplayType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(f.Display)
}

// SaveFinal saves the final stacked master again (it is saved as it
// arrives) and returns the file's path.
func (a *App) SaveFinal() (string, error) {
	a.mu.Lock()
	f := a.lastFinal
	a.mu.Unlock()
	if f == nil {
		return "", errors.New("no final image yet: stop imaging to get one")
	}

	return a.saveFinal(*f)
}

// saveFinal saves a final master with the app's EXIF and XMP.
func (a *App) saveFinal(f origin.Frame) (string, error) {
	s := a.Settings()
	a.mu.Lock()
	mosaic := f.Info.Uuid != "" && a.mosaicUuids[f.Info.Uuid]
	a.mu.Unlock()
	if c, err := a.cl(); err == nil { // isMosaicPayload: a mosaic task runs
		st := c.TaskState()
		mosaic = mosaic || st == "CAPTURING_MOSAIC" || st == "RECAPTURE_MOSAIC_TILE" || st == "COMPOSING_MOSAIC"
	}
	// Saved under the target's common name, else its designation
	// (saveToPhotoGallery's getName()).
	if t, ok := a.resolveTarget(f.Info, mosaic); ok {
		f.Info.ObjectName = cmp.Or(catalog.CommonName(t), t.Name)
	}

	return a.saveFrame(f, origin.FinalMetadata(f.Info, s.LatDeg, s.LonDeg), f.Info.ObjectName)
}

// resolveTarget is what a final image is of, in the app's order
// (resolveTargetObjectIDForPayload): the target recorded for its run when
// it lies within the image's diagonal field of its centre (a mosaic's: the
// larger of 5° and twice its field; without a field it counts as near);
// a recorded target that does not lies elsewhere gives way to the object
// at the centre, else stays. Without one: the first " - " part of the
// Origin's ObjectName that the lists know, when near, else the object at
// the centre. ok is false when nothing resolves. An image without a centre
// (RA and Dec 0) is not searched there (the app's would be).
func (a *App) resolveTarget(info origin.ImageReady, mosaic bool) (catalog.Target, bool) {
	r := 180 / math.Pi
	ra, dec := info.Ra*r, info.Dec*r
	known := info.Ra != 0 || info.Dec != 0
	limit := math.Hypot(info.FovX, info.FovY) * r
	if mosaic {
		limit = max(5, 2*max(info.FovX, info.FovY)*r)
	}
	near := func(t catalog.Target) bool {
		return !known || limit <= 0 || separation(ra, dec, t.RADeg, t.DecDeg)*r <= limit
	}
	centre := func() (catalog.Target, bool) {
		if !known {
			return catalog.Target{}, false
		}

		return a.imagingTarget(ImageRequest{}, ra, dec, a.now())
	}
	a.mu.Lock()
	run, ok := a.runTargets[info.Uuid]
	a.mu.Unlock()
	if ok {
		if near(run) {
			return run, true
		}
		if t, ok := centre(); ok {
			return t, true
		}

		return run, true
	}
	for part := range strings.SplitSeq(info.ObjectName, " - ") {
		if t, _, ok := a.cat.Find(strings.TrimSpace(part)); ok {
			if near(t) {
				return t, true
			}

			break
		}
	}

	return centre()
}

// saveFrame writes f into SaveDir as the app saves to the photo gallery: a
// PNG of the image's pixels carrying m, named name-<time>.png.
func (a *App) saveFrame(f origin.Frame, m origin.Metadata, name string) (string, error) {
	s := a.Settings()
	pngData := f.Display
	if f.Uncompressed {
		var err error
		if pngData, err = origin.CompressPNG(f.Display); err != nil {
			return "", err
		}
	} else if f.DisplayType != "image/png" {
		img, _, err := image.Decode(bytes.NewReader(f.Display))
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return "", err
		}
		pngData = buf.Bytes()
	}
	b, err := origin.PNGWithExif(pngData, m)
	if err != nil {
		return "", err
	}
	// Named after the target, else the run (saveToPhotoGallery's
	// ImageUniqueID), at the time it was taken.
	name = safeName(name, safeName(f.Info.Uuid, "origin"))
	taken := m.Taken // the Origin's local clock
	loc := siteZone(s)
	if taken.IsZero() {
		taken = a.now().In(loc)
	}
	if err := os.MkdirAll(s.SaveDir, 0o755); err != nil {
		return "", err
	}
	// Every save is a new image, as each is a new gallery record in the
	// app: a name already taken (Save again, same second) gets -1, -2, ….
	p, err := writeNew(filepath.Join(s.SaveDir, name+"-"+taken.Format("20060102-150405")), ".png", b)
	if err != nil {
		return "", err
	}
	wall := time.Date(taken.Year(), taken.Month(), taken.Day(), taken.Hour(), taken.Minute(), taken.Second(), 0, loc)
	r := 180 / math.Pi
	title := f.Info.ObjectName
	if title == "" {
		title = name
	}
	rec := ImageRecord{
		Title: title, Taken: taken.Format("2006-01-02 15:04:05"), TakenUnix: wall.Unix(),
		RaDeg: f.Info.Ra * r, DecDeg: f.Info.Dec * r, FovXDeg: f.Info.FovX * r, FovYDeg: f.Info.FovY * r,
		RotationDeg: f.Info.Orientation * r, ExposureSec: f.Info.ExposureTime, ISO: f.Info.ISO, StackDepth: f.Info.StackDepth,
		LatDeg: m.LatDeg, LonDeg: m.LonDeg,
	}
	if g := f.Info.MosaicGrid; g != nil {
		rec.GridW, rec.GridH = g.GridWidth, g.GridHeight
	}
	rec.Uuid = f.Info.Uuid
	a.update(func(s *Settings) {
		if s.Images == nil {
			s.Images = map[string]ImageRecord{}
		}
		s.Images[filepath.Base(p)] = rec
	})

	return p, nil
}

// safeName makes name usable as one file name on every OS: path
// separators and characters Windows refuses become '_', and a name that is
// empty or starts with a dot (".", "..") becomes fallback.
func safeName(name, fallback string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>| `, r) {
			return '_'
		}

		return r
	}, name)
	if name == "" || strings.HasPrefix(name, ".") {
		return fallback
	}

	return name
}

// Activity is the current status line, progress and shown image.
func (a *App) Activity() Activity {
	in, have := a.statusIn()

	screen := in
	screen.StatusScreen = true

	return Activity{
		Text: statusText(in), Box: finalBox(in), Progress: progress(in), Image: shownImage(in, have), Warning: warning(in), Speed: in.Speed,
		Task: statusText(screen), Target: targetLabel(in), Waiting: in.RestartSeconds != nil,
		Init: initButton(in), Manual: a.manualLabel(),
		MasterBusy: in.DownloadingFinal || in.DownloadingStacked != nil || in.SavingFinal || in.Processing != nil,
		Forced:     in.Forced,
	}
}

// refresh sends an "activity" event when Activity changed.
func (a *App) refresh() {
	a.actMu.Lock()
	defer a.actMu.Unlock()
	a.keepAwake(a.needAwake())
	act := a.Activity()
	if a.lastAct != nil && *a.lastAct == act {
		return
	}
	a.lastAct = &act
	a.emit("activity", act)
}

func (a *App) statusIn() (statusIn, map[string]bool) {
	a.mu.Lock()
	c := a.client
	in := statusIn{
		WaitingFinal: a.waitingFinal, DownloadingFinal: a.dlFinal, DownloadingStacked: a.dlStacked, Processing: a.processing,
		SavingFinal: a.saving, Speed: a.avgSpeed(), LowBandwidth: a.lowBandwidth(),
		TransferPercent: a.transferPct, Snapshot: a.settings.Snapshot, Moving: a.slewing,
		RestartSeconds: a.restartSeconds(), NoLocation: !a.settings.HasLocation(), CancellingInit: a.cancellingInit,
		FinalPercent: a.finalPct, Tiles: a.mosaicView, Composition: a.dlComposition, Searching: a.searching > 0,
		Forced: a.forced,
	}
	have := map[string]bool{}
	for k := range a.frames {
		have[k] = true
	}
	a.mu.Unlock()
	if c == nil {
		return in, have
	}
	in.Connected = c.Connected()
	in.Connecting = !in.Connected
	in.Moving = in.Moving || goingTo(c)
	in.PointedAt, in.Selected = a.pointedAt(c)
	var ts origin.TaskStatus
	if m, ok := c.Last("TaskController/GetStatus"); ok && m.Decode(&ts) == nil {
		in.Task = &ts
		if o := ts.ImagingInfo.Current(); o != nil && o.ObjectName != "" {
			in.ImagingName = a.styled(o.ObjectName) // after the status is read
		}
	}
	var ms struct{ BatteryLevel, ChargerStatus string }
	if m, ok := c.Last("Mount/GetStatus"); ok && m.Decode(&ms) == nil {
		in.Battery, in.Charger = ms.BatteryLevel, ms.ChargerStatus
	}
	var disk struct{ Level string }
	if m, ok := c.Last("Disk/GetStatus"); ok && m.Decode(&disk) == nil {
		in.DiskLevel = disk.Level
	}
	var ag struct{ IsGuiding bool }
	if m, ok := c.Last("Autoguider/GetStatus"); ok && m.Decode(&ag) == nil {
		in.Guiding = ag.IsGuiding
	}
	var cp struct{ Exposure, ExposedTime *float64 }
	if m, ok := c.Last("Camera/GetCaptureParameters"); ok && m.Decode(&cp) == nil {
		in.Exposure, in.Exposed = cp.Exposure, cp.ExposedTime
	}
	var fc struct {
		FlatGenerationInfo *struct{ PercentageCompleted float64 }
	}
	if m, ok := c.Last("FactoryCalibrationController/GetStatus"); ok && m.Decode(&fc) == nil && fc.FlatGenerationInfo != nil {
		in.FlatsPercent = &fc.FlatGenerationInfo.PercentageCompleted
	}

	return in, have
}

// About is the catalog NOTICE: source commit, licenses and credits.
func (a *App) About() string { return catalog.Notice() }

// siteZone is the site's time zone, else the computer's.
func siteZone(s Settings) *time.Location {
	if loc, err := time.LoadLocation(s.TimeZone); err == nil {
		return loc
	}

	return time.Local //nolint:gosmopolitan // no site zone: the computer's
}

// SetSelection tells Go which target the frontend has selected, for the
// imaging screen's target label.
func (a *App) SetSelection(catalogName, name string) {
	a.mu.Lock()
	a.selection = ImageRequest{Catalog: catalogName, Name: name}
	a.mu.Unlock()
	a.refresh()
}

// pointedCache keeps pointedAt's answer for one position and selection.
type pointedCache struct {
	name    string
	sel     ImageRequest
	ra, dec float64
	filled  bool
}

// pointedAt is the target the telescope points at, named as runs are
// (setupImagingTargetObject), from a position read only while the Origin is
// ready and aligned; and whether a target is selected. The lookup runs only
// when the position or the selection changes.
func (a *App) pointedAt(c *origin.Client) (string, bool) {
	a.mu.Lock()
	sel, cache := a.selection, a.pointed
	a.mu.Unlock()
	var ms origin.MountStatus
	if !ready(c) {
		return "", sel.Name != ""
	}
	if m, ok := c.Last("Mount/GetStatus"); !ok || m.Decode(&ms) != nil || !ms.IsAligned {
		return "", sel.Name != ""
	}
	if cache.filled && cache.sel == sel && cache.ra == ms.Ra && cache.dec == ms.Dec {
		return cache.name, sel.Name != ""
	}
	name := ""
	if t, ok := a.imagingTarget(sel, ms.Ra*180/math.Pi, ms.Dec*180/math.Pi, a.now()); ok {
		name = catalog.StyledName(t) // "Andromeda Galaxy - M 31", as the app's label
	}
	a.mu.Lock()
	a.pointed = pointedCache{sel: sel, name: name, ra: ms.Ra, dec: ms.Dec, filled: true}
	a.mu.Unlock()

	return name, sel.Name != ""
}

// AppVersion is Nightglass's version (Version & Updates' "%s, ver %s"): the
// tag it was built from, else the commit, else "dev".
func AppVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}

	return "dev"
}

// AppVersion is bound for the frontend.
func (a *App) AppVersion() string { return AppVersion() }

// ConnectTo connects to host from the connect dialog: typed (manual), it is
// probed and kept as the address to use; picked from a search, it is not.
func (a *App) ConnectTo(host string, manual bool) error {
	if err := a.Connect(host); err != nil {
		return err
	}
	a.update(func(s *Settings) { s.ManualHost = manual })

	return nil
}

// ConnectOrigin is the app's connectOrigin, at start, after a drop, after
// the restart wait and from the status badge: a typed address is probed and
// connected to (one that does not answer is forgotten as typed and offered
// to search for, "manualinvalid" event); else the network is searched: one
// Origin found is connected to, several are offered ("origins" event); none
// opens Quick Start for a first connection ("quickstart") or reports the
// app's automatic connection failure. One runs at a time (connectingScope),
// and its result is dropped if the user connected or disconnected meanwhile.
func (a *App) ConnectOrigin() error {
	s := a.Settings()
	if math.Abs(s.LatDeg) < 1e-4*180/math.Pi && math.Abs(s.LonDeg) < 1e-4*180/math.Pi {
		return errNoLocation
	}
	a.mu.Lock()
	if a.connecting {
		a.mu.Unlock()

		return nil
	}
	a.connecting = true
	gen := a.gen
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.connecting = false
		a.mu.Unlock()
	}()
	acted := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()

		return a.gen != gen || a.client != nil
	}
	if s.ManualHost && s.Host != "" {
		if a.CheckHost(s.Host) != nil {
			a.update(func(s *Settings) { s.ManualHost = false })
			if !acted() {
				a.emit("manualinvalid", ManualInvalid{Host: s.Host})
			}

			return nil
		}
		if acted() {
			return nil
		}

		return a.Connect(s.Host)
	}
	found, _ := a.search(context.Background())
	switch {
	case acted():
	case len(found) == 1:
		return a.Connect(found[0])
	case len(found) > 1:
		a.emit("origins", Origins{Hosts: found}) // OriginScopeSelectionMenu
	case s.EverConnected:
		a.emit("alert", errAutoConnect)
	default:
		a.emit("quickstart", struct{}{})
	}

	return nil
}

// errAutoConnect is the app's handleOriginAdHocWiFiConnectionFailure.
var errAutoConnect = Alert{
	"Automatic Telescope Wi-Fi Connection Failed",
	`Nightglass can't make a wireless network connection to the telescope. Make sure it's on and connected to your local Wi-Fi network, or that you've joined its Wi-Fi network named with the prefix "Origin-".`,
}

// ManualInvalid is the "manualinvalid" event: the typed address Host did
// not answer; the frontend offers Try Automatically.
type ManualInvalid struct {
	Host string
}

// Origins is the "origins" event: the search found several; one is picked.
type Origins struct {
	Hosts []string
}

// emitError shows err as the frontend's act would: an "alert:" one in an
// alert box, else a toast (nothing for errShown).
func (a *App) emitError(err error) {
	m := err.Error()
	switch {
	case errors.Is(err, errShown):
	case strings.HasPrefix(m, "alert:"):
		title, msg, _ := strings.Cut(strings.TrimPrefix(m, "alert:"), ": ")
		a.emit("alert", Alert{title, msg})
	default:
		if title, msg, ok := strings.Cut(m, ": "); ok {
			a.emit("alert", Alert{title, msg})
		} else {
			a.emit("toast", Toast{toastError, m})
		}
	}
}

// styled is a run's object name in the app's label style ("Andromeda Galaxy
// - M 31"), looked up once per name; a name not in the lists stays as it is.
func (a *App) styled(name string) string {
	a.mu.Lock()
	v, ok := a.styledNames[name]
	a.mu.Unlock()
	if ok {
		return v
	}
	v = name
	if t, _, found := a.cat.Find(name); found {
		v = catalog.StyledName(t)
	}
	a.mu.Lock()
	a.styledNames[name] = v
	a.mu.Unlock()

	return v
}

// force sets the state the screen shows until the Origin reports it
// (forcedTempStatusForUI): IMAGING_OBJECT once a run is sent, SAMPLE_CAPTURE
// once a snapshot is; "" clears it.
func (a *App) force(state string) {
	a.mu.Lock()
	a.forced = state
	a.mu.Unlock()
	a.refresh()
}

// uiState is the state the screen and the capture button go by
// (originStatusForUI): the forced one until the Origin reports it.
func (a *App) uiState(c *origin.Client) string {
	st := c.TaskState()
	a.reported(st)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.forced != "" {
		return a.forced
	}

	return st
}

// reported ends the forced state when the Origin reports it.
func (a *App) reported(st string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.forced != "" && a.forced == st {
		a.forced = ""
	}
}

// writeNew writes b to base+ext, or base-1+ext, base-2+ext, … whichever
// does not exist yet, and returns the path written.
func writeNew(base, ext string, b []byte) (string, error) {
	for i := 0; ; i++ {
		p := base + ext
		if i > 0 {
			p = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(b); err != nil {
			_ = f.Close()

			return "", err
		}

		return p, f.Close()
	}
}
