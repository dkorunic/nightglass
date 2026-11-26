# Nightglass

A desktop controller for the Celestron Origin smart telescope, for macOS,
Linux and Windows: discovery, status, initialize/align, go-to from 34 deep-sky
catalogs, manual slew, focus, imaging with live and stacked frames, and saving
the final image. It is a clean-room Go reimplementation of the Origin app's
telescope protocol; it contains no Celestron code, artwork or sky data.

## Layout

| Path | What |
|---|---|
| `origin/` | protocol: WebSocket client, commands, image download, discovery |
| `origin/fake/` | in-process fake Origin for tests, procedural and survey sky frames |
| `catalog/` | the astro-recommender target lists (packed gob), alt/az, Solar System, Sun & Moon times, framing of large nebulae |
| `app/control/` | the GUI's Go side (no Wails import, tests anywhere) |
| `app/` | Wails v3 window and the HTML/CSS/JS frontend |
| `cmd/originctl/` | CLI with every GUI action |
| `cmd/origin-fake/` | simulated Origin for GUI work without a telescope |

## Build

Go 1.27.1 or newer. The GUI needs the platform WebView toolchain:

- **Linux** (Debian 13 / Ubuntu 24.04): `sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev`
- **macOS**: Xcode command line tools (`xcode-select --install`)
- **Windows**: nothing extra; the WebView2 runtime ships with Windows 10/11

```sh
go build -o dist/ ./cmd/...                     # originctl, origin-fake
go build -o dist/nightglass ./app           # Linux, macOS
go build -ldflags -H=windowsgui -o dist/nightglass.exe ./app   # Windows
```

With [Task](https://taskfile.dev): `task build` formats, regenerates the
catalog lists and builds everything into `dist/`; `task build-cli` builds only
the static CLI tools; `task --list` shows the rest.

## Develop without a telescope

```sh
go run ./cmd/origin-fake            # prints: fake Origin at 127.0.0.1:8080
go run ./app                        # finds it by its UDP announcement, or Connect… → 127.0.0.1:8080
go run ./cmd/originctl -host 127.0.0.1:8080 status
```

The fake announces itself for discovery (`-announce 0` turns it off; a second
one on another port, e.g. `-listen 127.0.0.1:8081`, brings up the picker),
moves its mount for go-tos, the slew pad and Reframe, focuses (stars blur
off focus), reboots or powers down when told to (`-reboot` sets how long a
reboot takes), offers a flat panel with `-elpanel`, and serves star fields of
where it points (with the position in each image's notification), less noisy
as a stack deepens.

For real sky in the frames, `-sky` cuts 2.5° tiles from a HiPS survey via CDS
hips2fits, or from the DESI Legacy Survey viewer with `legacy:<layer>`, in the
background (network needed; procedural stars stand in until a tile arrives):

```sh
go run ./cmd/origin-fake -sky CDS/P/DSS2/color
go run ./cmd/origin-fake -sky legacy:ls-dr10      # Dec > ~-68°, no Milky Way
go run ./cmd/origin-fake -sky CDS/P/PanSTARRS/DR1/color-z-zg-g,CDS/P/DSS2/color   # first covering each tile
```

## Test

```sh
go test -race ./...
node --test app/frontend/targets.test.mjs
sh scripts/smoke.sh     # the GUI in Wails server mode against origin-fake
sh scripts/e2e.sh       # GUI tests: the frontend in headless Chrome (CHROME=… if not found)
task check              # all of the above, as CI runs them
task lint               # format, then golangci-lint (must report 0 issues)
```

## Releases

Push a tag such as `v0.1.0`: `.github/workflows/release.yml` builds
Nightglass with `originctl` and `origin-fake` for macOS (Apple Silicon and
Intel, as `Nightglass.app`), Linux and Windows, and attaches them to a
GitHub release. macOS builds are signed and notarised when the
`MACOS_CERT_P12`, `MACOS_CERT_PASSWORD`, `APPLE_ID`, `APPLE_TEAM_ID` and
`APPLE_APP_PASSWORD` secrets are set, and are unsigned otherwise.

The icon is Nightglass's own (`app/build/icon.svg`); after changing it run
`sh scripts/icons.sh` and commit the regenerated `icon.png`, `icon.ico` and
`icon.icns`.

## Updating the catalogs

`catalog/targets/*.yaml` are verbatim copies from
[astro-recommender](https://github.com/dkorunic/astro-recommender)
(`internal/catalog/targets/`); never edit them here. To update: copy the YAML
files over, update the commit in `catalog/targets/NOTICE.md`, then

```sh
go test ./catalog -run TestEmbeddedLists -args -update
```

and commit the YAML and the regenerated `*.gob` together. CI fails if they
disagree. If no `*.gob` exists at all (a fresh tree), add
`-tags catalogbootstrap` to that command once.

## Hardware acceptance checklist

Run against a real Origin, from the GUI and once with `originctl`. Items marked
**verify** are protocol details only the hardware can confirm.

1. `originctl discover` lists the telescope (UDP 55555; **verify** the
   announcement text with `originctl watch` if not). On Origin's own Wi-Fi
   it also lists `1.2.3.4` (**verify** the Origin answers there on port 80).
2. Connect; status shows mount, battery, task state, weather, storage.
   **Verify** with `originctl watch` that replies name their module in
   `Source` (or else in `Destination`); an empty status pane means neither
   matched (`origin/message.go` `Module`).
3. Settings: enter location and time zone. Initialize & align completes and
   *Aligned* turns yes. **Verify** latitude/longitude are taken as radians
   (a wrong unit fails alignment or reports a wrong site in Mount status).
4. Slew pad and arrow keys move the mount at each rate (1–4); releasing stops it.
   Switching to another window mid-slew stops it. **Verify** up/right match
   the mount's altitude-up / azimuth-increasing.
5. Pull the Wi-Fi mid-slew: the mount stops when the link returns.
6. Focus ±10/±100 (±100/±1000 in snapshot mode) and a typed position move
   the focuser within its limits, only while idle; Auto runs autofocus.
   **Verify** `Focuser/GetPositionLimits` and the stretch sliders change the
   stacked images.
7. Pick a target above the lowest go-to altitude (Settings, 0° by default):
   Go to slews there (J2000 coordinates, unprecessed); below it Go to is
   disabled. Go to and slewing are refused while the telescope is imaging.
8. Image (as in the app, it images where the telescope points, so go to the
   target first; the run is named after the selection only within about 1°
   of where the telescope points, else after what is there): live frames,
   then stacked frames update; Finish imaging
   (HaltTasks + GetFinalStackedMaster) produces the final image, shown and
   saved to the save folder as a PNG with EXIF (check it in an EXIF viewer);
   the status line and progress bar follow the run as in the app. Cancel
   imaging asks, then discards the run.
   Disconnect mid-run and reconnect: the app offers the run's image for
   download.
   Below 0° Image is refused; above 85° off a wedge it warns about field
   rotation. With manual exposure off the Origin picks exposure and ISO;
   **verify** both modes and snapshot (manual live view).
9. Night mode (N) turns the whole window red.
10. Snapshot mode: Capture Image takes a snapshot to save or delete.
    Reframe: click the image, Center Here recentres there (**verify** the
    offsets' direction). The filter button changes filters and offers
    autofocus.
11. Files: list, re-download, copy to a USB stick (progress, cancel) and
    delete image folders. Origin: each advanced setting changes the
    telescope (**verify** the dew heater, fans and lights respond).
12. On a wedge: initialize, accept the polar alignment recommendation, adjust
    the mount until the errors are small, End Polar Alignment (**verify**
    the error signs match the knobs). Capture dark and flat frames; check
    Version & Updates against the Origin's own app; demo mode indoors.
13. On connect the Origin is told its Wi-Fi regulatory domain (**verify**
    with `originctl watch` that `Network/SetRegulatoryDomain` is accepted
    for your country); an uncalibrated Origin shows "Factory Calibration
    Incomplete" once.
14. Solar System: go to the Moon and a planet, then image them. **Verify**
    the run's `TargetTrack` (16 J2000 places one second apart from 30 s on,
    the Moon corrected for parallax, Julian dates in UT) keeps the target
    centred, and that `ObjectSize` (twice the object's size, as the app
    sends it) gives the same exposures as the Origin's own app.
15. Status: charger, the site and clock the mount reports (they should match
    Settings after Initialize), encoders and, with a StarSense Autoguider,
    its state. The warning badge shows a low battery, charger fault or full
    disk. Origin → Download Origin Logs saves the log bundle (**verify** the
    `System/LogsReady` link downloads). Power Off disconnects. An
    out-of-range manual exposure is limited with the app's explanation.
16. Gallery: favorite, view, delete and share (the file manager opens at the
    image) saved images; Edit an image, crop and rotate it, Overwrite and
    Export Copy, and check the result in an image viewer (the EXIF stays).
    A typed address with no Origin offers Try Automatically.
17. Start a run, close Nightglass and reconnect mid-run: **verify** what the
    Origin sends for `GetFinalStackedMaster` during a run (stacked or final;
    a final one is saved as it arrives), and that Finish then works.
18. Pull the Wi-Fi briefly: Nightglass reconnects, clears the images and
    rejoins a run in progress. **Verify** which close code the Origin sends
    when it shuts the connection itself (a normal close now disconnects
    without reconnecting).
19. A timed run (Stop after N min): **verify** the Origin ends it after that
    long, and that the final image then arrives; compare the battery
    percentage with the Origin's battery level over a session.
20. Origin → Reboot: **verify** the Origin restarts and how long it takes
    (Nightglass waits 180 s, as after an update; Attempt To Connect from the
    connection badge reconnects earlier).
21. On a wedge, accept the polar alignment recommendation at once: **verify**
    the Origin starts polar alignment while it is still finishing
    initialization (the app sends it immediately).
22. Mosaic: from a target, Mosaic… with a 2 × 2 grid; **verify** the tiles,
    their thumbnails, the status line's tile and frame, Re-Capture, and
    Compose Mosaic (the composed image is saved); `originctl watch` shows
    whether the Origin pushes Mosaic notifications.
23. Version & Updates: **verify** the update offer against Celestron's
    channel list, the release notes, and "Download And Update Origin" (the
    Origin downloads, installs and restarts; Nightglass reconnects).
24. With `originctl watch`: **verify** the UDP announcement text, that
    replies name their module in `Source` (as the captures show), and the
    close code when the Origin ends a session.
25. Start a mosaic, disconnect until it finishes, reconnect: "Image List
    Available for Download" is offered (**verify** that
    `TaskController/GetObservingListInfo` answers `ImagingInfo.ListStatus`
    `FINISHED`); Download Images… opens it.
26. During a run, the computer does not idle-sleep (macOS `pmset -g
    assertions` lists caffeinate; Linux `systemd-inhibit --list`; Windows
    `powercfg /requests`), and may sleep again once the Origin is idle.
27. Give the Origin a new address (e.g. a DHCP change) and restart it:
    Nightglass finds it at the new address instead of redialling the old one.
28. Check a stacked and a final master's bit depth (`originctl fetch`): if
    16-bit, **verify** Nightglass's auto-stretch looks like the app's; with
    Auto-Crop Images on, the saved image is 64 pixels smaller each way.
29. Provoke an Origin error (e.g. go to while it initializes): **verify** the
    "Origin Error" alert, and that an error the app ignores (-2, -301) shows
    nothing.
30. Go to the Rosette (NGC 2237 / C 49), the Pelican (IC 5067) and IC 1396:
    **verify** the frame holds the nebula (Nightglass's own framing points,
    `catalog.Framing`).
31. Switch the Origin off while connected: Nightglass searches once ("Searching
    for Origin…") and, finding none, shows "Automatic Telescope Wi-Fi Connection
    Failed" (Quick Start only if it never connected); with a typed address it
    shows "Manual IP Address Invalid". With Keep Screen On the display does not
    blank (macOS, Windows; Linux as the desktop allows).
32. Double-click Capture: one run starts and the button stays disabled until
    the Origin reports it. **Verify** a frame announced while a stacked master
    downloads is never needed (Nightglass drops it, as the app), and that a
    schedule's run reports `ComposingMosaicInfo.ListUuid` while composing.
33. Start a timed run, pull the Wi-Fi for a minute and restore it: the run
    still ends by itself with its final image downloaded.
34. Start Nightglass while the Origin is still booting: no "Connection
    Failure" once it answers on the network; the connection opens by itself.
    **Verify** go-to, slew, initialise and halt answer promptly (Nightglass no
    longer waits for them) and that a single Origin is found at once via
    origin.local.

## License

MIT, see [LICENSE](LICENSE), with these exceptions:

- The error code names and English error messages in `origin/errcodes.go`
  are reproduced from Celestron's Origin app, so that Origin errors read as
  the app reports them. They remain the property of their owner and are not
  covered by the MIT license.
- The vendored catalog lists in `catalog/targets/` keep their own sources and
  licenses, listed in `catalog/targets/NOTICE.md`.
