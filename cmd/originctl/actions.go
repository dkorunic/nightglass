// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// actions are the GUI's other telescope actions, sending what the GUI sends
// (app/control) without its idle checks: originctl is for debugging.
var actions = map[string]bool{
	"snapshot": true, "polar": true, "darks": true, "flats": true, "updates": true, "center": true, //nolint:goconst // command names as typed
	"stretch": true, "filter": true, "files": true, "final": true, "logs": true, "set": true,
	"recalibrate-env": true, "poweroff": true, "reboot": true, "restore": true, "update": true, "mosaic": true,
}

// opts are the flags the actions use.
type opts struct {
	outFile              string
	exp                  float64
	iso, count           int
	strength, background float64 // NaN: not given
	manual, all          bool
}

func onOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}

	return false, fmt.Errorf("want on or off, got %q", s)
}

func number(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("want a number, got %q", s)
	}

	return v, nil
}

// send sends one command and prints its response.
func send(ctx context.Context, c *origin.Client, out io.Writer, dest, cmd string, p map[string]any) error {
	m, err := c.Send(ctx, dest, cmd, p)
	if m.Raw != nil {
		fmt.Fprintf(out, "%s\n", m.Raw)
	}

	return err
}

// action runs one of actions; pos are its arguments.
func action(ctx context.Context, c *origin.Client, cmd string, pos []string, o opts, out io.Writer) error {
	arg := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}

		return ""
	}
	switch cmd {
	case "snapshot": // captureSnapShotImage
		var p map[string]any
		if o.manual {
			p = map[string]any{"ExposureTime": o.exp, "ISO": o.iso}
		}

		return send(ctx, c, out, "TaskController", "RunSampleCapture", p)
	case "polar": // stop with halt
		return send(ctx, c, out, "TaskController", "PolarAlign", nil)
	case "darks":
		if o.all { // the complete set, custom darks included
			return send(ctx, c, out, "TaskController", "RunGenerateNewDarks", nil)
		}
		if o.count < 1 || o.count > 100 || o.exp <= 0 || o.iso <= 0 {
			return errors.New("darks needs -n 1..100, -exp S and -iso N, or -all")
		}

		return send(ctx, c, out, "TaskController", "RunGenerateNewDarks", map[string]any{"NbrSubs": o.count, "ExposureTime": o.exp, "ISO": o.iso})
	case "flats":
		return send(ctx, c, out, "TaskController", "RunGenerateNewFlat", nil)
	case "updates":
		switch arg(0) {
		case "auto":
			on, err := onOff(arg(1))
			if err != nil {
				return err
			}

			return send(ctx, c, out, "System", "SetAutomaticUpdates", map[string]any{"Enabled": on})
		case "channel":
			if arg(1) == "" {
				return errors.New("updates channel needs a NAME")
			}

			return send(ctx, c, out, "System", "SetUpdateChannel", map[string]any{"Value": arg(1)})
		case "check":
			return send(ctx, c, out, "System", "HasUpdateAvailable", map[string]any{"ForceCheck": true})
		}

		return errors.New("updates needs auto on|off, channel NAME or check")
	case "center": // reframe: X and Y 0..1 from the image's left and top
		x, errX := number(arg(0))
		y, errY := number(arg(1))
		if errX != nil || errY != nil || x < 0 || x > 1 || y < 0 || y > 1 {
			return errors.New("center needs X Y in 0..1")
		}

		return send(ctx, c, out, "TaskController", "RunCenterTarget", map[string]any{"OffsetX": x, "OffsetY": y})
	case "stretch": // one value per SetStretch, as the app's sliders, then read back
		for _, kv := range []struct {
			key string
			v   float64
		}{{"Strength", o.strength}, {"Background", o.background}} {
			if kv.v != kv.v { // NaN: not given
				continue
			}
			if kv.v < 0 || kv.v > 1 {
				return fmt.Errorf("stretch %s %v: want 0..1", kv.key, kv.v)
			}
			if _, err := c.Send(ctx, "ImageServer", "SetStretch", map[string]any{kv.key: kv.v}); err != nil {
				return err
			}
		}

		return send(ctx, c, out, "ImageServer", "GetStretch", nil)
	case "filter":
		if arg(0) != "Clear" && arg(0) != "Nebula" {
			return errors.New("filter needs Clear or Nebula")
		}

		return c.SetFilter(ctx, arg(0))
	case "files":
		return files(ctx, c, pos, o, out)
	case "final": // an earlier run's final image, as "Download Now"
		if arg(0) == "" || o.outFile == "" {
			return errors.New("final needs -o FILE and a UUID")
		}

		return saveFinal(ctx, c, o.outFile, func() error { return c.GetFinalStackedMaster(ctx, arg(0)) })
	case "logs":
		return logs(ctx, c, o.outFile, out)
	case "set":
		return set(ctx, c, pos, out)
	case "recalibrate-env":
		return send(ctx, c, out, "Environment", "RecalibrateEnvironmentSensor", nil)
	case "poweroff":
		return send(ctx, c, out, "System", "PowerDownOrigin", nil)
	case "restore": // the factory dark or flat frames
		cmd := map[string]string{"darks": "RestoreDarksToFactory", "flats": "RestoreFlatsToFactory"}[arg(0)]
		if cmd == "" {
			return errors.New("restore needs darks or flats")
		}

		return send(ctx, c, out, "TaskController", cmd, nil)
	case "mosaic":
		return mosaic(ctx, c, pos, out)
	case "update": // "Download And Update Origin": the Origin fetches and installs it itself
		return send(ctx, c, out, "System", "Update", map[string]any{"Reboot": true})
	case "reboot":
		return send(ctx, c, out, "System", "Reboot", nil)
	}

	return fmt.Errorf("unknown action %q", cmd)
}

// files is the file manager.
func files(ctx context.Context, c *origin.Client, pos []string, o opts, out io.Writer) error {
	dir := ""
	if len(pos) > 1 {
		dir = pos[1]
	}
	need := func() error {
		if dir == "" {
			return fmt.Errorf("files %s needs a DIR", pos[0])
		}

		return nil
	}
	if len(pos) == 0 {
		return errors.New("files needs ls, get DIR -o FILE, rm DIR, rm-all or usb [DIR]")
	}
	switch pos[0] {
	case "ls":
		return send(ctx, c, out, "ImageServer", "GetListOfAvailableDirectories", nil)
	case "get": // the folder's stacked master arrives as a final image
		if err := need(); err != nil {
			return err
		}
		if o.outFile == "" {
			return errors.New("files get needs -o FILE")
		}

		return saveFinal(ctx, c, o.outFile, func() error {
			m, err := c.Send(ctx, "ImageServer", "GetStackedMasterFromDirectory", map[string]any{"DirectoryName": dir})
			if err != nil {
				return err
			}
			var v struct{ FileExists bool }
			if m.Decode(&v) != nil || !v.FileExists {
				return errors.New("final stacked master does not exist")
			}

			return nil
		})
	case "rm":
		if err := need(); err != nil {
			return err
		}

		return send(ctx, c, out, "ImageServer", "DeleteImageDirectory", map[string]any{"DirectoryName": dir})
	case "rm-all":
		return send(ctx, c, out, "ImageServer", "DeleteAllImageDirectories", nil)
	case "usb": // progress arrives as TaskController/FileTransferStatus (watch)
		if dir == "" {
			return send(ctx, c, out, "TaskController", "CopyAllDirectoriesToFlashDrive", nil)
		}

		return send(ctx, c, out, "TaskController", "CopyDirectoryToFlashDrive", map[string]any{"DirectoryName": dir})
	}

	return fmt.Errorf("unknown files command %q", pos[0])
}

// saveFinal runs ask, then saves the FINAL_STACKED_MASTER that arrives
// (as served: TIFF) to file.
func saveFinal(ctx context.Context, c *origin.Client, file string, ask func() error) error {
	sub := c.Subscribe()
	if err := ask(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return errors.New("no final image arrived")
		case m := <-sub:
			var info origin.ImageReady
			if m.Key() != "ImageServer/NewImageReady" || m.Decode(&info) != nil || info.ImageType != "FINAL_STACKED_MASTER" {
				continue
			}
			f, err := origin.Fetch(ctx, http.DefaultClient, c.Host(), info)
			if err != nil {
				return err
			}

			return os.WriteFile(file, f.Original, 0o644) //nolint:gosec // a downloaded image, readable like any other
		}
	}
}

// logs asks for the Origin's logs and saves the file LogsReady names into
// dir (default: here).
func logs(ctx context.Context, c *origin.Client, dir string, out io.Writer) error {
	if dir == "" {
		dir = "."
	}
	sub := c.Subscribe()
	if _, err := c.Send(ctx, "System", "GetLogs", nil); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return errors.New("no LogsReady arrived")
		case m := <-sub:
			var lr struct{ DownloadLink string }
			if m.Key() != "System/LogsReady" || m.Decode(&lr) != nil || lr.DownloadLink == "" {
				continue
			}
			b, err := origin.FetchFile(ctx, http.DefaultClient, c.Host(), lr.DownloadLink)
			if err != nil {
				return err
			}
			name := path.Base("/" + filepath.ToSlash(lr.DownloadLink))
			if name == "/" || name == "." || name == ".." {
				name = "origin-logs"
			}
			p := filepath.Join(dir, filepath.Base(name))
			if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // a downloaded image, readable like any other
				return err
			}
			fmt.Fprintln(out, p)

			return nil
		}
	}
}

// set changes one Origin setting, as the GUI's Origin settings do, then
// prints what the Origin reports back.
func set(ctx context.Context, c *origin.Client, pos []string, out io.Writer) error {
	if len(pos) < 2 {
		return errors.New("set needs SETTING VALUE")
	}
	v := pos[1]
	apply := func(dest, cmd string, p map[string]any, readBack string) error {
		if _, err := c.Send(ctx, dest, cmd, p); err != nil {
			return err
		}

		return send(ctx, c, out, dest, readBack, nil)
	}
	switch pos[0] {
	case "wedge":
		on, err := onOff(v)
		if err != nil {
			return err
		}

		return apply("Mount", "SetMountConfig", map[string]any{"IsEquatorial": on, "OnWedge": on}, "GetMountConfig")
	case "led", "mountled": // brightness 0..1: the LED ring, the mount's LEDs
		f, err := number(v)
		if err != nil || f < 0 || f > 1 {
			return errors.New("brightness needs 0..1")
		}
		dest := map[string]string{"led": "LedRing", "mountled": "LampController"}[pos[0]]

		return apply(dest, "SetBrightnessLevel", map[string]any{"BrightnessLevel": f}, "GetBrightnessLevel")
	case "fan": // fan ota|cpu on|off
		key := map[string]string{"ota": "OtaFanOn", "cpu": "CpuFanOn"}[v]
		if key == "" || len(pos) < 3 {
			return errors.New("set fan needs ota|cpu on|off")
		}
		on, err := onOff(pos[2])
		if err != nil {
			return err
		}

		return apply("Environment", "SetFans", map[string]any{key: on}, "GetStatus")
	case "af-goto":
		on, err := onOff(v)
		if err != nil {
			return err
		}

		return apply("Focuser", "AutoFocusAfterGoto", map[string]any{"Value": on}, "GetFocuserAdvancedSettings")
	case "af-temp": // af-temp on|off [CHANGE °C 1..10]
		on, err := onOff(v)
		if err != nil {
			return err
		}
		p := map[string]any{"EnableTemperatureShiftAutoFocus": on}
		if on && len(pos) > 2 {
			t, err := number(pos[2])
			if err != nil || t < 1 || t > 10 {
				return errors.New("temperature change needs 1..10 °C")
			}
			p["TemperatureThresholdC"] = t
		}

		return apply("Focuser", "AutoFocusOnTemperatureChange", p, "GetFocuserAdvancedSettings")
	case "dew": // dew auto 1..10 | dew manual 0..1
		if len(pos) < 3 {
			return errors.New("set dew needs auto AGGRESSION or manual POWER")
		}
		f, err := number(pos[2])
		if err != nil {
			return err
		}
		switch {
		case v == "auto" && f >= 1 && f <= 10 && f == float64(int(f)):
			return apply("DewHeater", "EnableAuto", map[string]any{"Aggression": int(f)}, "GetStatus")
		case v == "manual" && f >= 0 && f <= 1:
			return apply("DewHeater", "EnableManual", map[string]any{"PowerLevel": f}, "GetStatus")
		}

		return errors.New("set dew needs auto 1..10 or manual 0..1")
	case "giotto": // the GIOTTO flat generator: on|off, or a brightness 1..100
		if on, err := onOff(v); err == nil {
			cmd := "TurnOffLight"
			if on {
				cmd = "TurnOnLight"
			}

			return apply("ElPanel", cmd, nil, "GetStatus")
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return errors.New("set giotto needs on|off or 1..100")
		}

		return apply("ElPanel", "SetLightLevel", map[string]any{"LightLevel": n}, "GetStatus")
	}

	return fmt.Errorf("unknown setting %q", pos[0])
}

// mosaic is the GUI's mosaics: start one at RA DEC (J2000 degrees) of
// WIDTH × HEIGHT arc minutes; list them; a mosaic's tiles; recapture a
// tile; resume; compose. Payloads as app/control's.
func mosaic(ctx context.Context, c *origin.Client, pos []string, out io.Writer) error {
	arg := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}

		return ""
	}
	switch arg(0) {
	case "start": // mosaic start RA DEC WIDTH HEIGHT [ROTATION_DEG [DURATION_S [EXPOSURE_S [ISO]]]]
		vals := []float64{0, 0, 0, 0, 0, 600, 10, 200}
		for i := range vals {
			if v := arg(i + 1); v != "" {
				f, err := number(v)
				if err != nil {
					return err
				}
				vals[i] = f
			} else if i < 4 {
				return errors.New("mosaic start needs RA DEC WIDTH HEIGHT [ROTATION DURATION EXPOSURE ISO]")
			}
		}
		r := math.Pi / 180
		uuid := strings.ToUpper(origin.NewUUID())
		if err := send(ctx, c, out, "TaskController", "RunCaptureMosaic", map[string]any{
			"Uuid": uuid, "ObjectRa": vals[0] * r, "ObjectDec": vals[1] * r,
			"MosaicWidth": vals[2] / 60 * r, "MosaicHeight": vals[3] / 60 * r, "MosaicOrientation": math.Mod(vals[4], 360) * r,
			"TotalDuration": vals[5], "ExposureTime": vals[6], "ISO": int(vals[7]),
			"SaveRawImage": false, "PowerDownOnCompletion": false, "AutoFocusAfterSlew": false,
		}); err != nil {
			return err
		}
		fmt.Fprintln(out, uuid)

		return nil
	case "list":
		return send(ctx, c, out, "Mosaic", "GetAvailableMosaics", nil)
	case "tiles":
		return send(ctx, c, out, "Mosaic", "GetMosaicTiles", map[string]any{"MosaicUuid": arg(1)})
	case "recapture": // mosaic recapture MOSAIC TILE [ROW COL]
		p := map[string]any{"MosaicUuid": arg(1), "ObjectUuid": arg(2)}
		if arg(3) != "" && arg(4) != "" {
			row, errR := strconv.Atoi(arg(3))
			col, errC := strconv.Atoi(arg(4))
			if errR != nil || errC != nil {
				return errors.New("mosaic recapture: ROW and COL are numbers")
			}
			p["TileRow"], p["TileCol"] = row, col
		}

		return send(ctx, c, out, "TaskController", "RecaptureMosaicTile", p)
	case "resume":
		return send(ctx, c, out, "TaskController", "ResumeImagingList", map[string]any{"Uuid": arg(1)})
	case "compose":
		return send(ctx, c, out, "TaskController", "RunComposeMosaic", map[string]any{
			"Uuid": arg(1), "Scale": 0.5, "FlattenBackgrounds": true, "NormalizeBackgrounds": true, "EqualizeNoise": true,
		})
	}

	return errors.New("mosaic needs start, list, tiles UUID, recapture MOSAIC TILE [ROW COL], resume UUID or compose UUID")
}
