// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Command originctl drives a Celestron Origin from the terminal. Every GUI
// action has a command here, for debugging and the hardware checklist.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
	_ "time/tzdata" // IANA zones for -tz on Windows

	"github.com/coder/websocket"
	"github.com/dkorunic/nightglass/catalog"
	"github.com/dkorunic/nightglass/origin"
)

//nolint:dupword // column-aligned: "halt   halt every task"
const usage = `usage: originctl [-host ip[:port]] [-timeout 10s] command [flags] [args]

  discover                          list telescopes on the network (11 s)
  status                            print every module's status
  watch                             print every message until interrupted, and
                                    how the connection closes (close code)
  init -lat D -lon D -tz ZONE [-demo]
                                    initialize and align (degrees, IANA zone);
                                    -demo is the app's demo mode (indoors)
  goto  -target NAME | -ra D -dec D [-lat D -lon D]
                                    slew to a catalog target, the Moon or a planet
                                    (where it is now, from -lat/-lon), or J2000 degrees
  image [-target NAME] [-name N] [-manual -exp S -iso N] [-raw=false] [-continue]
        [-dither -dither-distance 10 -dither-interval 5] [-lat D -lon D]
        [-duration 30m]             (-duration: the Origin ends the run itself)
                                    image where the telescope points (goto first);
                                    -target only names it, with its size/magnitude,
                                    and for the Moon or a planet sends its track
  finish                            end imaging and fetch the final image
  cancel                            discard the imaging run (no final image)
  halt                              halt every task
  slew -alt R -azm R [-for 1s]      move at a rate 0 to ±9 (the Origin's), then stop
  focus -by N | -to N | -auto [-terrestrial | -from-origin]
                                    move or autofocus the focuser (-from-origin:
                                    infinity autofocus where the Origin chooses,
                                    as after a filter change)
  track on|off                      sidereal tracking
  capture [-manual [-snapshot] -exp S -iso N]
                                    camera mode: automatic, or manual (snapshot:
                                    manual live view), as the app sets it
  fetch -o FILE LOCATION            download a NewImageReady FileLocation
  snapshot [-manual -exp S -iso N]  take a snapshot (SAMPLE_CAPTURE image)
  center X Y                        recenter on a point of the image (0..1 from
                                    its left and top), as Reframe
  stretch [-strength V] [-background V]
                                    set the image stretch (0..1), print it
  filter Clear|Nebula               tell the Origin which filter is installed
  polar                             start polar alignment (end it with halt)
  darks -iso N -exp S -n N | -all   capture dark frames, or the complete set
  flats                             capture flat frames
  restore darks|flats               restore the factory dark or flat frames
  updates auto on|off | channel NAME | check
                                    automatic updates, update channel, check now
  files ls | get DIR -o FILE | rm DIR | rm-all | usb [DIR]
                                    the Origin's image folders
  final -o FILE UUID                download an earlier run's final image
  logs [-o DIR]                     download the Origin's logs
  set wedge on|off | led V | mountled V | fan ota|cpu on|off | af-goto on|off
      | af-temp on|off [C] | dew auto N | dew manual P | giotto on|off|N
                                    change an Origin setting, print it back
  recalibrate-env                   recalibrate the environment sensor
  poweroff                          power the Origin off
  reboot                            restart the Origin
  mosaic start RA DEC W H [ROT DUR EXP ISO] | list | tiles UUID
       | recapture MOSAIC TILE [ROW COL] | resume UUID | compose UUID
                                    mosaics (degrees, arc minutes, seconds)
  update                            update the Origin's software from its channel
                                    (it downloads, installs and restarts itself)
  send DEST CMD [JSON]              send any command, print the response
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("originctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	host := fs.String("host", "", "telescope address (default: discover)")
	timeout := fs.Duration("timeout", 10*time.Second, "connect and per-command timeout")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()

		return 2
	}
	cmd, rest := fs.Arg(0), fs.Args()[1:]
	if cmd == "discover" {
		ips, err := origin.Discover(ctx, origin.DiscoveryTime)
		for _, ip := range ips {
			fmt.Fprintln(stdout, ip)
		}
		if len(ips) == 0 {
			fmt.Fprintln(stderr, "originctl: no telescope found", err)

			return 1
		}

		return 0
	}
	if !commands[cmd] && !actions[cmd] {
		fmt.Fprintf(stderr, "originctl: unknown command %q\n", cmd)
		fs.Usage()

		return 2
	}
	h := *host
	if h == "" {
		ips, _ := origin.Discover(ctx, origin.DiscoveryTime)
		if len(ips) == 0 {
			fmt.Fprintln(stderr, "originctl: no telescope found; use -host")

			return 1
		}
		h = ips[0]
	}
	h, err := origin.NormalizeHost(h)
	if err != nil {
		fmt.Fprintln(stderr, "originctl:", err)

		return 2
	}
	c := origin.New(h)
	c.Timeout, c.PollInterval = *timeout, -1
	// Malformed frames are queued here and printed by watch, from its own
	// goroutine (the read loop must not block or share a writer with it).
	bad := make(chan string, 16)
	c.OnMalformed = func(raw []byte, err error) {
		select {
		case bad <- fmt.Sprintf("malformed frame (%v): %s", err, raw):
		default:
		}
	}
	up := make(chan struct{})
	var once sync.Once
	c.OnState = func(ok bool, err error) {
		if ok {
			once.Do(func() { close(up) })

			return
		}
		// For watch: how the link ended, with the WebSocket close code
		// (-1: no close frame, the link dropped).
		if err != nil {
			select {
			case bad <- fmt.Sprintf("connection closed (code %d): %v", websocket.CloseStatus(err), err):
			default:
			}
		}
	}
	// The link outlives Ctrl-C (ctx), so an interrupted slew can still be
	// stopped; it closes when run returns.
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	go func() { _ = c.Run(rctx) }()
	select {
	case <-up:
	case <-time.After(*timeout):
		fmt.Fprintf(stderr, "originctl: cannot connect to %s\n", h)

		return 1
	case <-ctx.Done():
		return 1
	}
	if err := do(ctx, c, cmd, rest, stdout, stderr, bad); err != nil {
		fmt.Fprintln(stderr, "originctl:", err)

		return 1
	}

	return 0
}

var commands = map[string]bool{
	"status": true, "watch": true, "init": true, "goto": true, "image": true, "finish": true, "cancel": true, "halt": true,
	"slew": true, "focus": true, "track": true, "capture": true, "fetch": true, "send": true,
}

//nolint:maintidx // one case per command
func do(ctx context.Context, c *origin.Client, cmd string, args []string, out, errOut io.Writer, bad <-chan string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lat := fs.Float64("lat", math.NaN(), "latitude, degrees")
	lon := fs.Float64("lon", math.NaN(), "longitude, degrees east")
	tz := fs.String("tz", os.Getenv("TZ"), "IANA time zone")
	target := fs.String("target", "", "catalog name, e.g. M42")
	ra := fs.Float64("ra", math.NaN(), "J2000 RA, degrees")
	dec := fs.Float64("dec", math.NaN(), "J2000 Dec, degrees")
	name := fs.String("name", "", "object name for image")
	exp := fs.Float64("exp", 0, "exposure, seconds")
	iso := fs.Int("iso", 0, "ISO")
	alt := fs.Int("alt", 0, "altitude rate")
	azm := fs.Int("azm", 0, "azimuth rate")
	dur := fs.Duration("for", time.Second, "slew duration")
	by := fs.Int("by", 0, "focus steps")
	to := fs.Int("to", -1, "focus position")
	auto := fs.Bool("auto", false, "autofocus")
	terrestrial := fs.Bool("terrestrial", false, "terrestrial autofocus (with -auto)")
	outFile := fs.String("o", "", "output file")
	manual := fs.Bool("manual", false, "manual exposure and ISO")
	snapshot := fs.Bool("snapshot", false, "manual live view (with -manual)")
	raw := fs.Bool("raw", true, "keep raw images on the Origin (-raw=false: do not)")
	cont := fs.Bool("continue", false, "continue imaging when plate solving fails")
	dither := fs.Bool("dither", false, "dither between exposures")
	ditherDist := fs.Int("dither-distance", 10, "dither distance, pixels")
	ditherIv := fs.Int("dither-interval", 5, "dither every N exposures")
	duration := fs.Duration("duration", 0, "image: end the run after this long (TotalDuration)")
	demo := fs.Bool("demo", false, "demo mode initialization (init)")
	fromOrigin := fs.Bool("from-origin", false, "autofocus where the Origin chooses (with -auto)")
	all := fs.Bool("all", false, "the complete set of dark frames (darks)")
	count := fs.Int("n", 10, "number of dark frames")
	strength := fs.Float64("strength", math.NaN(), "stretch strength, 0..1")
	background := fs.Float64("background", math.NaN(), "stretch background, 0..1")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if actions[cmd] {
		return action(ctx, c, cmd, pos, opts{
			manual: *manual, all: *all, exp: *exp, iso: *iso, count: *count,
			strength: *strength, background: *background, outFile: *outFile,
		}, out)
	}
	site := func() (float64, float64) { // where the Moon is seen from; 0, 0 without -lat/-lon
		if math.IsNaN(*lat) || math.IsNaN(*lon) {
			return 0, 0
		}

		return *lat, *lon
	}
	switch cmd {
	case "status":
		for _, q := range origin.StatusQueries {
			m, err := c.Send(ctx, q[0], q[1], nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", m.Key(), m.Raw)
		}
	case "watch":
		sub := c.Subscribe()
		for {
			select {
			case <-ctx.Done():
				return nil
			case m := <-sub:
				fmt.Fprintf(out, "%s %s\n", time.Now().Format("15:04:05"), m.Raw)
			case line := <-bad:
				fmt.Fprintln(errOut, "originctl:", line)
			}
		}
	case "init":
		if math.IsNaN(*lat) || math.IsNaN(*lon) {
			return errors.New("init needs -lat and -lon")
		}

		return c.Initialize(ctx, origin.Site{LatDeg: *lat, LonDeg: *lon, Zone: *tz, Fake: *demo}, time.Now())
	case "goto":
		la, lo := site()
		_, r, d, _, _, err := resolve(*target, *ra, *dec, la, lo)
		if err != nil {
			return err
		}

		return c.GotoRaDec(ctx, r, d)
	case "image":
		im := origin.Imaging{
			Name: *name, Manual: *manual, ExposureSec: *exp, ISO: *iso, SaveRaw: *raw, StopOnPlateSolveFail: !*cont,
			Dither: *dither, DitherDistance: *ditherDist, DitherInterval: *ditherIv,
			TotalDurationSec: duration.Seconds(),
		}
		if *target != "" {
			la, lo := site()
			n, _, _, mag, size, err := resolve(*target, *ra, *dec, la, lo)
			if err != nil {
				return err
			}
			if im.Name == "" {
				im.Name = n
			}
			if _, _, _, body := catalog.Body(n, time.Now(), la, lo); body {
				// The Moon or a planet: its magnitude, however bright, and its track.
				im.Mag, im.Track = &mag, catalog.Track(n, time.Now(), la, lo)
			} else if catalog.KnownMag(mag) {
				im.Mag = &mag
			}
			im.SizeArcmin = size
		}

		return c.RunImaging(ctx, im)
	case "finish":
		// The run's ImageUuid comes from the task status; ask for it first.
		if _, err := c.Send(ctx, "TaskController", "GetStatus", nil); err != nil {
			return err
		}

		return c.FinishImaging(ctx, "")
	case "cancel":
		return c.CancelImaging(ctx)
	case "halt":
		return c.HaltTasks(ctx)
	case "slew":
		err := c.Slew(ctx, *alt, *azm)
		if err == nil {
			select {
			case <-time.After(*dur):
			case <-ctx.Done():
			}
		}
		// Always stop, on a context Ctrl-C has not cancelled: a Slew whose
		// response was lost may still have started the mount.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		return errors.Join(err, c.StopSlew(sctx))
	case "focus":
		switch {
		case *auto:
			if *terrestrial {
				return c.AutoFocus(ctx, true, 0)
			}
			if *fromOrigin {
				return c.AutoFocus(ctx, false, 0)
			}
			m, err := c.Send(ctx, "Focuser", "GetStatus", nil)
			if err != nil {
				return err
			}
			var fs origin.FocuserStatus
			if err := m.Decode(&fs); err != nil {
				return err
			}

			return c.AutoFocus(ctx, false, fs.Position)
		case *to >= 0:
			return c.FocusTo(ctx, *to)
		case *by != 0:
			return c.FocusBy(ctx, *by)
		}

		return errors.New("focus needs -by, -to or -auto")
	case "track":
		if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
			return errors.New("track needs on or off")
		}

		return c.SetTracking(ctx, pos[0] == "on")
	case "capture":
		return c.ApplyCapture(ctx, origin.Capture{Manual: *manual, Snapshot: *snapshot, ExposureSec: *exp, ISO: *iso})
	case "fetch":
		if len(pos) != 1 || *outFile == "" {
			return errors.New("fetch needs -o FILE and a LOCATION")
		}
		f, err := origin.Fetch(ctx, http.DefaultClient, c.Host(), origin.ImageReady{FileLocation: pos[0]})
		if err != nil {
			return err
		}

		return os.WriteFile(*outFile, f.Original, 0o644) //nolint:gosec // a downloaded image, readable like any other
	case "send":
		if len(pos) < 2 || len(pos) > 3 {
			return errors.New("send needs DEST CMD [JSON]")
		}
		var payload map[string]any
		if len(pos) == 3 {
			if err := json.Unmarshal([]byte(pos[2]), &payload); err != nil {
				return fmt.Errorf("payload: %w", err)
			}
		}
		m, err := c.Send(ctx, pos[0], pos[1], payload)
		if m.Raw != nil {
			fmt.Fprintf(out, "%s\n", m.Raw)
		}

		return err
	}

	return nil
}

// resolve returns a catalog target (name, J2000 degrees, magnitude, size in
// arc minutes), the Moon or a planet where it is now seen from lat, lon, or
// the -ra/-dec position.
func resolve(target string, ra, dec, lat, lon float64) (string, float64, float64, float64, float64, error) {
	for _, b := range catalog.Bodies {
		if catalog.Key(b) == catalog.Key(target) {
			r, d, mag, _ := catalog.Body(b, time.Now(), lat, lon)

			return b, r, d, mag, 0, nil
		}
	}
	if target != "" {
		cat, err := catalog.Load()
		if err != nil {
			return "", 0, 0, 0, 0, err
		}
		t, _, ok := cat.Find(target)
		if !ok {
			return "", 0, 0, 0, 0, fmt.Errorf("no catalog target %q", target)
		}

		return t.Name, t.RADeg, t.DecDeg, t.Mag, t.Size, nil
	}
	if math.IsNaN(ra) || math.IsNaN(dec) {
		return "", 0, 0, 0, 0, errors.New("need -target or both -ra and -dec")
	}

	return fmt.Sprintf("RA %.3f Dec %.3f", ra, dec), ra, dec, 0, 0, nil
}
