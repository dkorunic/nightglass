# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Nightglass is a Go desktop controller (macOS, Linux, Windows) for the Celestron Origin smart telescope, built with Wails v3. It is a clean-room reimplementation of the Android Origin app's telescope protocol and behaviour: never copy Celestron code, artwork or sky data into the repo. The former design spec is no longer in the repo: code comments citing "spec §N" refer to it, and the reasoning behind each parity decision lives in the comments and tests next to the code (`app/control/parity*_test.go`).

## Commands

`Taskfile.yml` wraps most of these (`task build`, `task test`, `task check` for everything CI runs, `task flake`, `task run-fake -- -sky …`; `task --list`).

```sh
task fmt                                     # gci, gofumpt, go fix, betteralign -apply (rewrites files)
task lint                                    # task fmt, then golangci-lint (.golangci.yml); must report 0 issues
gofmt -l .                                   # CI fails on any output
go vet ./...
go test -race ./...                          # all Go tests
go test ./app/control -run TestFileManager   # one test
go test -race -count=10 ./app/control        # flake check; tests use a real fake server, timing matters
node --test app/frontend/targets.test.mjs    # frontend pure helpers
sh scripts/smoke.sh                          # GUI in Wails server mode vs origin-fake (no GTK/WebView needed); expect patterns are grep -E
sh scripts/e2e.sh                            # GUI tests: the frontend in headless Chrome (CHROME=path if needed) vs origin-fake
sh scripts/icons.sh                          # re-render app/build/icon.svg into icon.png/.ico/.icns (rsvg-convert, ImageMagick, iconutil)

go build -o dist/ ./cmd/...                  # originctl, origin-fake
go build -o dist/nightglass ./app            # GUI (needs libgtk-4-dev libwebkitgtk-6.0-dev on Linux)
CGO_ENABLED=0 go build -tags server -o dist/app-server ./app   # headless server-mode build, as smoke.sh uses

go run ./cmd/origin-fake                     # simulated Origin at 127.0.0.1:8080 for GUI work
go run ./cmd/origin-fake -sky CDS/P/DSS2/color   # frames cut from a real HiPS survey (CDS hips2fits, network)
go run ./cmd/origin-fake -sky legacy:ls-dr10     # or from the DESI Legacy Survey viewer (Dec > ~-68°, no Milky Way)
go run ./cmd/origin-fake -sky CDS/P/PanSTARRS/DR1/color-z-zg-g,CDS/P/DSS2/color   # first survey covering each tile (gaps checked in PNG; legacy: only last)
go run ./cmd/originctl -host 127.0.0.1:8080 status
```

Catalogs: `catalog/targets/*.yaml` are verbatim copies from astro-recommender and must not be edited here. After copying new YAML, run `go test ./catalog -run TestEmbeddedLists -args -update` (add `-tags catalogbootstrap` once if no `*.gob` exists) and commit YAML and `*.gob` together; `TestEmbeddedLists` fails when they disagree.

betteralign reorders struct fields, so JSON key order is not stable: tests and `smoke.sh` patterns must not depend on it. Lint exemptions live in `.golangci.yml` with their reasons (the app's capitalised error texts, payload fields named after JSON keys); a one-off exemption is a `//nolint:<linter> // reason` on the line.

## Architecture

**`origin/`: the protocol.** One WebSocket per telescope (`ws://host/SmartScope-1.0/mountControlEndpoint`), flat JSON frames: header fields (`Command`, `Destination`, `SequenceID`, `Source`, `Type`, `ErrorCode`, `ErrorMessage`) plus payload keys at the same level. Replies and notifications name their module in `Source`, commands in `Destination`; `Message.Module()`/`Key()` (e.g. `"Mount/GetStatus"`) encode that rule and everything keys off it. Angles on the wire are radians, RA/Dec are J2000 and never precessed. `Client`:
- reconnects with backoff after a dropped link, but stops (`ErrClosed`) when the telescope closes normally, as the app; it sends no pings (the app has none);
- matches responses to `Send` by `SequenceID`; `Post` sends without waiting, and its response reaches subscribers with `Waited()` false, so `app/control` shows any error in it;
- fans every message out to `Subscribe()` channels;
- caches the newest error-free message per key in `Last(key)`;
- on each connect sends `StatusQueries`, which is how most state is populated. Add a query there rather than sending it ad hoc when the app or native library asks for it at connect.

Images are fetched over HTTP from `/SmartScope-1.0/dev2/<FileLocation>`; TIFF is converted to PNG for display. The GUI serves frames at `/frame/<kind>` and saved images (gallery) at `/frame/saved/<name>`, names checked by `safeName`. Payload struct fields are named after the JSON keys (no tags) and use pointers where "not reported" must be distinguished from zero.

**`origin/fake/`** is an in-process fake Origin used by nearly every test: `fake.New("")`, `SetStatus`/`SetInfo` (answers to `GetStatus`/other `Get*`), `Notify`, `Received()`/`Wait(fake.Is(dest, cmd), d)`. **`cmd/origin-fake`** wraps it with simulated behaviour (imaging runs, snapshots, polar alignment, file manager, settings, a moving mount and focuser, autofocus, reboot/power-off, discovery announcements on UDP 55555) for GUI development and `smoke.sh`; its frames are `fake.Sky` procedural star fields of where the mount points, or with `-sky` 2.5° tiles of a HiPS survey fetched from CDS hips2fits (or the Legacy Survey viewer, `legacy:<layer>`) in the background and resampled per frame (`origin/fake/survey.go`; procedural stars stand in until a tile arrives) (tests keep the tiny `fake.JPEG`/`fake.TIFF16`).

**`app/control/`: the GUI's Go side**, deliberately free of Wails imports so it tests anywhere. Every exported `App` method is bound to the frontend. Go pushes state with `emit(event, data)`; the event names are `conn`, `status`, `frame`, `activity`, `toast`, `alert`, `critical`, `orphans`, `nebula`, `polar`, `logs`, `update`, `thumb`, `listready`, `saved`, `logsaved`, `origins`, `quickstart` and `manualinvalid`. Key flows:
- `Connect` creates a `Client` and starts `forward`, which turns client messages into events, image downloads (one `origin.Latest` slot per frame kind) and side effects such as alerts, update restarts and transfer progress. `onConnected` does the app's connect-time work.
- **`activity.go` holds the status line, progress bar and choice of shown image as pure functions** (`statusText`, `finalBox`, `progress`, `shownImage`, `warning`) of a `statusIn` snapshot. These are table-tested against the app's strings. `App.refresh()` recomputes the result and emits `activity` only when it changed. Call it after any state change that affects it. Lock order is `saveMu` (settings writes), then `actMu`, then `mu`.
- Refusals use the app's alert text as `"Title: message"` errors, prefixed `alert:` when the app shows them in an alert box rather than a toast, or `flash:` for its self-closing alert without buttons. UI strings are taken from the app's `strings.xml`.
- Origin errors follow the app's relay: one with an app message is an "Origin Error" alert from `forward`, whether or not a caller waited for it, and never reaches the screen logic; the client's `MapError` hook (`shownErr`) then turns the waiting caller's error into `errShown` (`"shown:"`), which the frontend's `act` shows nothing for. Use `errShown` too where the app refuses silently.
- `Settings` fields Go maintains (`PendingImages`, `PendingLists`, `Favorites`, `LaunchCount`, `RemoteVersions`, `Mosaics`, `OriginModel`, `OriginVersion`, `Images`, `LastAnnouncement`, `ManualHost`, `EverConnected`, `Host`, `Catalog`, `MaxExposureSec`) are kept across `SaveSettings`; change them with `a.update`, not from the frontend.
- Side effects on the machine go through `App` hooks that tests replace: `discover` (the network search, also run when the saved address stops answering) and `inhibit` (the OS sleep inhibitor held while imaging or downloading, `awake_*.go`).
- The only outside network calls are the app's version check (`update.go`, `remoteURL`, Celestron's public channel list) and, only when chosen in Settings, its Origin announcements (`announce.go`, `announcementURL`, a third party's); tests replace both URLs.
- Connecting follows the app's `connectOrigin` (`App.ConnectOrigin`): a typed address (`ManualHost`) is probed, else the network is searched (one found is connected to, several emit `origins`, none `quickstart` before the first connection and the automatic-failure alert after; a typed address that fails emits `manualinvalid`). It runs at start, after a drop and after the restart wait; `Connect` first probes TCP port 80 (failing: "Connection Failure"), then the client redials the WebSocket until it opens but gives up once an established link drops (`Client.GiveUp`), and the app then connects anew. `originctl` keeps the client's own reconnect loop.

**`app/` frontend** (`app/frontend/`, plain ES modules embedded into the binary): `ui.js` holds the shared helpers (`act`, `$`, toasts, alert boxes) and calls Go with `Call.ByName("github.com/dkorunic/nightglass/app/control.App.<Method>")`; the module path is part of every binding name, so renaming the module means updating `SVC` in `ui.js` and `scripts/smoke.sh`. `app.js` is the core (connection, status, frames, controls, targets, settings) and owns the shared state, exported as live read-only bindings with `setSettings`/`setLastOrigin` for writes; feature modules (`files.js`, `origin.js`, `mosaics.js`, `quickstart.js`, `gallery.js`) import from `ui.js` and `app.js`. They must not touch `app.js` bindings at load time (import cycle), and a module the core does not import from needs a side-effect `import "./x.js"`. Pure, testable logic goes in `targets.js` with tests in `targets.test.mjs`; the DOM wiring is exercised by `scripts/e2e.sh` (headless Chrome via the dependency-free `scripts/cdp.mjs`) and `smoke.sh` (bindings, routes, assets).

**`catalog/`**: the 34 target lists, decoded from embedded packed gob at startup, with alt/az including precession for the target table, plus the computed "Solar System" list (`solar.go`: Moon and planets in J2000, the Moon with parallax), which `app/control` lists first and images with a `TargetTrack`. `tonight.go` and `sun.go` give the Tonight Sun & Moon times and a target's imaging window; `framing.go` aims large nebulae (Rosette, Pelican, IC 1396) at the middle of their catalogued parts instead of their nominal position. Telescope commands always send unprecessed J2000.

## Working on app parity

New behaviour is usually ported from the Android app. The protocol details (payload keys, enum strings, which commands are sent when) come from the decompiled Java (`OriginApi*`, `SkySafariActivity*Origin*`, the fragments) and the native `libskychart.so`. The usual pattern is:
1. Add a test against `fake` that fails because the behaviour is missing.
2. Implement it in `app/control` (plus a fake default or `origin-fake` behaviour if the GUI needs it).
3. Wire the frontend.
4. Explain the parity decision in a comment beside the code or test, and add a hardware checklist item to `README.md` for anything only real hardware can confirm.
