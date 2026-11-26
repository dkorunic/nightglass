// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Settings is everything the app remembers, stored as JSON in the OS config
// directory (app/main.go picks the path).
type Settings struct {
	// Images are the saved images' targets and positions, by file name (the
	// app's SkyObjectImage records).
	Images   map[string]ImageRecord
	TimeZone string // IANA, e.g. "Europe/Zagreb"
	Host     string // last telescope address
	Catalog  string // selected list; "" is all catalogs
	SaveDir  string

	// RemoteVersions is the last Origin software version list fetched from
	// Celestron (KeyOriginLatestCoreVersionInfo), raw JSON.
	RemoteVersions string

	// OriginModel and OriginVersion are the last ones reported
	// (storeCelestronOriginModelAndVersion), shown while disconnected.
	OriginModel, OriginVersion string

	LastAnnouncement string
	// Formats (SettingsFormatsFragment): TimeFormat "24h", "24hs", "12h" or
	// "12hs" (s: with seconds); DateOrder "YMD", "DMY", "MDY", "YDM", "DYM"
	// or "MYD"; CoordFormat "dms" (degrees, minutes, seconds), "dm" (decimal
	// minutes) or "deg" (decimal degrees).
	TimeFormat, DateOrder, CoordFormat string

	// Kept by Go, never taken from SaveSettings (the frontend's copy may be
	// older):
	// PendingImages are the Uuids of imaging runs whose final image has not
	// been downloaded (ORIGIN_ORPHAN_IMAGE_UUIDS), offered again on connect.
	PendingImages []string
	Favorites     []string       // saved images marked Favorite in the gallery, by file name
	Mosaics       []MosaicRecord // the mosaics started here (the app's mosaic schedules)
	// PendingLists are the mosaics not yet downloaded or dismissed
	// (ORIGIN_ORPHAN_IMAGE_LISTS_UUIDS), asked about on connect.
	PendingLists []string

	LatDeg, LonDeg      float64 // observer, degrees, east positive
	ExposureSec         float64 // Origin.java ORIGIN_DEFAULT_MANUAL_EXPOSURE_TIME
	ISO                 int     // manual ISO; 0 is the camera's default (ISOSettings.defaultISO)
	SnapshotExposureSec float64 // snapshot exposure, 0.1 s by default
	SlewLimitMinDeg     float64 // scopeCESlewLimitMin: lowest go-to altitude
	SlewRate            int     // the app's "Rate:" 1–4 (speeds 2, 4, 6, 9); 0 is the default
	DitherDistance      int     // ORIGIN_DITHER_AMOUNT
	DitherInterval      int     // ORIGIN_DITHER_FREQUENCY

	LaunchCount int // the app's currentLaunchCount, for the Quick Start
	// NightBrightness dims the window in night mode, percent
	// (NightVisionBrightness).
	NightBrightness int
	// MaxExposureSec is the last exposure limit the Origin allowed
	// (Cached.Origin.MaxExposureTime, 30 s until known), so a manual exposure
	// is limited while disconnected too. Kept by Go.
	MaxExposureSec float64
	// ManualHost: Host was typed (ORIGIN_USE_MANUAL_IP_ADDRESS_KEY), so it is
	// probed instead of searched for; set by ConnectTo.
	ManualHost bool
	// EverConnected: an Origin was connected to at least once
	// (the app's last successful Origin Wi-Fi); a search finding none reports a
	// failure instead of opening Quick Start. Kept by Go.
	EverConnected bool
	Night         bool
	// KeepScreenOn keeps the display awake while Nightglass runs
	// (Settings.PREVENT_SLEEP_KEY, off by default).
	KeepScreenOn bool
	// Announcements: show the app's Origin announcements at start, fetched
	// from news.skysafariastronomy.com (checkForAnnouncement); off unless
	// chosen. LastAnnouncement is the last one shown (kept by Go).
	Announcements bool

	// The app's camera and imaging preferences, with its defaults.
	ManualImaging        bool // Origin.UseManualImaging: else the telescope picks exposure and ISO
	Snapshot             bool // Origin snapshot mode: manual live view
	SaveRaw              bool // save raw images on the Origin
	StopOnPlateSolveFail bool // stop imaging when plate solving fails
	Dither               bool

	// The app's mosaic composition options (Origin settings, Mosaics), all
	// on by default.
	MosaicFlatten, MosaicNormalize, MosaicEqualize bool
	// PostProcess is the app's "AI Post Process" (ScopeAutomaticPostProcessImages,
	// default on): off, masters are shown unstretched and uncropped.
	PostProcess bool
	// AutoCrop cuts 32 pixels off the masters' edges (KeyOriginAutoCropImage),
	// on by default.
	AutoCrop bool
}

// DefaultSettings has no location: the user must enter one.
func DefaultSettings() Settings {
	home, _ := os.UserHomeDir()

	return Settings{
		TimeZone: DefaultTimeZone(), Catalog: "Messier", ExposureSec: 10,
		SaveDir:             filepath.Join(home, "Pictures", "Origin"),
		SnapshotExposureSec: 0.1, StopOnPlateSolveFail: true, DitherDistance: 15, DitherInterval: 3,
		NightBrightness: 65, TimeFormat: "24hs", DateOrder: "YMD", CoordFormat: "dms",
		SlewRate: 4, MosaicFlatten: true, MosaicNormalize: true, MosaicEqualize: true, AutoCrop: true, PostProcess: true, MaxExposureSec: 30,
	}
}

// HasLocation is false for 0°, 0°, which the telescope treats as unset.
func (s Settings) HasLocation() bool {
	return math.Abs(s.LatDeg) >= 1e-7 || math.Abs(s.LonDeg) >= 1e-7
}

// Validate rejects values the telescope or the alt/az math cannot use.
func (s Settings) Validate() error {
	var errs []error
	if math.Abs(s.LatDeg) > 90 || math.IsNaN(s.LatDeg) {
		errs = append(errs, fmt.Errorf("latitude %v outside ±90°", s.LatDeg))
	}
	if math.Abs(s.LonDeg) > 180 || math.IsNaN(s.LonDeg) {
		errs = append(errs, fmt.Errorf("longitude %v outside ±180°", s.LonDeg))
	}
	if s.TimeZone != "" {
		if _, err := time.LoadLocation(s.TimeZone); err != nil {
			errs = append(errs, fmt.Errorf("time zone %q: %w", s.TimeZone, err))
		}
	}
	if s.ExposureSec < 0 || s.SnapshotExposureSec < 0 || s.ISO < 0 {
		errs = append(errs, errors.New("exposure and ISO must not be negative"))
	}
	if s.SlewLimitMinDeg < 0 || s.SlewLimitMinDeg >= 90 {
		errs = append(errs, fmt.Errorf("minimum slew altitude %v outside 0–90°", s.SlewLimitMinDeg))
	}
	if s.SlewRate < 0 || s.SlewRate > 4 {
		errs = append(errs, fmt.Errorf("slew rate %d outside 1–4", s.SlewRate))
	}
	if s.DitherDistance < 0 || s.DitherInterval < 0 {
		errs = append(errs, errors.New("dither distance and interval must not be negative"))
	}
	if s.NightBrightness < 20 || s.NightBrightness > 100 {
		errs = append(errs, fmt.Errorf("night vision brightness %d%% outside 20–100%%", s.NightBrightness))
	}
	if !slices.Contains(timeFormats, s.TimeFormat) ||
		!slices.Contains(dateOrders, s.DateOrder) ||
		!slices.Contains(coordFormats, s.CoordFormat) {
		errs = append(errs, fmt.Errorf("unknown format %q, %q or %q", s.TimeFormat, s.DateOrder, s.CoordFormat))
	}

	return errors.Join(errs...)
}

// The formats the settings offer.
var (
	timeFormats  = []string{"24hs", "24h", "12hs", "12h"}
	dateOrders   = []string{"YMD", "DMY", "MDY", "YDM", "DYM", "MYD"}
	coordFormats = []string{"dms", "dm", "deg"}
)

// LoadSettings reads path. A missing file gives the defaults. A bad value
// costs only itself, as each of the app's preferences stands alone: a value
// of the wrong type or out of range gets its default and everything else is
// kept; only a file that is not JSON at all gives the defaults. Either way
// the original is kept as path.bak and an error says so, never a crash.
func LoadSettings(path string) (Settings, error) {
	s := DefaultSettings()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("settings %s unreadable, defaults used: %w", path, err)
	}
	var typeErr *json.UnmarshalTypeError
	err = json.Unmarshal(b, &s)
	switch {
	case errors.As(err, &typeErr): // the other fields were read
		s = repair(s)
	case err != nil:
		s = DefaultSettings()
	default:
		if err = s.Validate(); err != nil {
			s = repair(s)
		}
	}
	if err != nil {
		bak := path + ".bak"
		if werr := os.WriteFile(bak, b, 0o600); werr != nil { //nolint:gosec // beside the settings file itself; holds the site location
			bak = "not kept: " + werr.Error()
		}

		return s, fmt.Errorf("settings %s repaired, the original kept as %s: %w", path, bak, err)
	}

	return s, nil
}

// repair gives each value Validate refuses its default, keeping the rest.
func repair(s Settings) Settings {
	d := DefaultSettings()
	if math.Abs(s.LatDeg) > 90 || math.IsNaN(s.LatDeg) || math.Abs(s.LonDeg) > 180 || math.IsNaN(s.LonDeg) {
		s.LatDeg, s.LonDeg = d.LatDeg, d.LonDeg // a position is both or neither
	}
	if s.TimeZone != "" {
		if _, err := time.LoadLocation(s.TimeZone); err != nil {
			s.TimeZone = d.TimeZone
		}
	}
	if s.ExposureSec < 0 {
		s.ExposureSec = d.ExposureSec
	}
	if s.SnapshotExposureSec < 0 {
		s.SnapshotExposureSec = d.SnapshotExposureSec
	}
	if s.ISO < 0 {
		s.ISO = d.ISO
	}
	if s.SlewLimitMinDeg < 0 || s.SlewLimitMinDeg >= 90 {
		s.SlewLimitMinDeg = d.SlewLimitMinDeg
	}
	if s.SlewRate < 0 || s.SlewRate > 4 {
		s.SlewRate = d.SlewRate
	}
	if s.DitherDistance < 0 {
		s.DitherDistance = d.DitherDistance
	}
	if s.DitherInterval < 0 {
		s.DitherInterval = d.DitherInterval
	}
	if s.NightBrightness < 20 || s.NightBrightness > 100 {
		s.NightBrightness = d.NightBrightness
	}
	if !slices.Contains(timeFormats, s.TimeFormat) {
		s.TimeFormat = d.TimeFormat
	}
	if !slices.Contains(dateOrders, s.DateOrder) {
		s.DateOrder = d.DateOrder
	}
	if !slices.Contains(coordFormats, s.CoordFormat) {
		s.CoordFormat = d.CoordFormat
	}

	return s
}

// writeSettings replaces path atomically, so a crash mid-write cannot leave
// a half-written file.
func writeSettings(path string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil { // holds the site location
		return err
	}

	return os.Rename(tmp, path)
}

// DefaultTimeZone is the system's IANA zone where Go can tell (TZ, or the
// /etc/localtime link on Linux and macOS), else "": the frontend then
// offers the WebView's Intl zone, which Windows knows too.
func DefaultTimeZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(p, "zoneinfo/"); ok {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}

	return ""
}

// ImageRecord is what a saved image was of and where (the app's
// SkyObjectImage): the target, when it was taken (the Origin's local clock),
// its centre, field and rotation (degrees), its exposure and the site.
type ImageRecord struct {
	Title, Taken string // Taken as "2006-01-02 15:04:05"
	// Uuid is the run's (the image's EXIF ImageUniqueID).
	Uuid                            string
	TakenUnix                       int64
	RaDeg, DecDeg, FovXDeg, FovYDeg float64
	RotationDeg, ExposureSec        float64
	LatDeg, LonDeg                  float64
	ISO, StackDepth                 int
	// GridW, GridH: a composed mosaic's tile grid (the app's MosaicGrid
	// image metadata), 0 otherwise.
	GridW, GridH int
}
