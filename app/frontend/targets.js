// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Pure helpers for the target panel, kept apart from the DOM so
// targets.test.mjs can run them under node --test.

// key normalizes a designation for matching: "M 42", "m42" and "M42" agree.
export const key = (s) => s.toLowerCase().replace(/\s+/g, "");

// known returns v when the catalog knows it (> 0), else null.
export const known = (v) => (v > 0 ? v : null);

// sexa parses "5 35 17", "5:35:17", "-0 49" or "83.82" into a number, NaN
// when malformed. The sign comes from the text, so "-0 49" is negative.
export function sexa(s) {
  s = s.trim();
  const neg = s.startsWith("-");
  const f = s.replace(/^[+-]/, "").split(/[\s:]+/).filter(Boolean).map(Number);
  if (!f.length || f.length > 3 || f.some((x) => !Number.isFinite(x) || x < 0)) return NaN;
  if (f.slice(1).some((x) => x >= 60)) return NaN;
  const v = f.reduce((acc, x, i) => acc + x / 60 ** i, 0);
  return neg ? -v : v;
}

// dms formats degrees (or hours) as "5h 35′ 17″" / "−5° 27′ 00″".
export function dms(v, hours) {
  const sign = v < 0 ? "−" : hours ? "" : "+";
  let s = Math.round(Math.abs(v) * 3600);
  const a = Math.floor(s / 3600); s -= a * 3600;
  const b = Math.floor(s / 60); const c = s - b * 60;
  return `${sign}${a}${hours ? "h" : "°"} ${String(b).padStart(2, "0")}′ ${String(c).padStart(2, "0")}″`;
}

// angle is a status row's angle (radians in) in the chosen coordinate
// format (SettingsFormatsFragment): fmt "dms" (5h 35′ 17″ / +5° 27′ 00″),
// "dm" (decimal minutes) or "deg" (decimal); "–" when not reported;
// unsigned for azimuths and longitudes as 0–360.
export function angle(rad, hours = false, unsigned = false, fmt = "dms") {
  if (rad == null || !Number.isFinite(rad)) return "-";
  let v = (rad * 180) / Math.PI;
  if (hours) v = (((v / 15) % 24) + 24) % 24;
  else if (unsigned) v = ((v % 360) + 360) % 360;
  const unit = hours ? "h" : "°";
  const sign = v < 0 ? "−" : hours || unsigned ? "" : "+";
  if (fmt === "deg") return `${sign}${Math.abs(v).toFixed(hours ? 4 : 3)}${unit}`;
  if (fmt === "dm") {
    let m = Math.round(Math.abs(v) * 600) / 10;
    const a = Math.floor(m / 60);
    m -= a * 60;
    return `${sign}${a}${unit} ${m.toFixed(1).padStart(4, "0")}′`;
  }
  return hours || !unsigned ? dms(v, hours) : dms(v, false).replace(/^\+/, "");
}

// clock formats "HH:mm:ss" as the chosen time format: "24hs", "24h",
// "12hs" or "12h" (s: with seconds).
export function clock(hms, fmt = "24hs") {
  const m = /^(\d{1,2}):(\d{2})(?::(\d{2}))?$/.exec(hms ?? "");
  if (!m) return hms || "-";
  const [h, mi, s = "00"] = [Number(m[1]), m[2], m[3]];
  const secs = fmt.endsWith("s") ? `:${s}` : "";
  if (fmt.startsWith("12")) return `${((h + 11) % 12) + 1}:${mi}${secs} ${h < 12 ? "AM" : "PM"}`;
  return `${String(h).padStart(2, "0")}:${mi}${secs}`;
}

// calendar formats a date (year, month, day) in the chosen order: "YMD",
// "DMY", "MDY", "YDM", "DYM" or "MYD".
export function calendar(y, mo, d, order = "YMD") {
  const parts = { Y: String(y), M: String(mo).padStart(2, "0"), D: String(d).padStart(2, "0") };
  return [...order].map((c) => parts[c]).join(order.startsWith("Y") ? "-" : "/");
}

// freeSpace is the status screen's Free Disk Space: "%.2f GB" (10^9).
export const freeSpace = (bytes) => (bytes == null ? "-" : `${(bytes / 1e9).toFixed(2)} GB`);

// prepare adds the search keys a row is matched on.
export const prepare = (r) => ({ ...r, k: key(r.Name), d: key(`${r.Description} ${r.Type}`) });

// view returns the indices of rows matching query (and above the horizon
// when above is set), sorted by sort.col ("alt", "az", "Mag", "Size" or a
// text column) in sort.dir (1 or -1). pos holds alt, az interleaved, or is
// empty without a location. Unknown values sort last either way.
export function view(rows, pos, query, above, sort) {
  const q = key(query);
  const alt = (i) => (pos.length ? pos[2 * i] : NaN);
  const value = (i) => {
    const r = rows[i];
    switch (sort.col) {
      case "alt": return alt(i);
      case "az": return pos.length ? pos[2 * i + 1] : NaN;
      case "Mag": return known(r.Mag) ?? NaN;
      case "Size": return known(r.Size) ?? NaN;
      default: return r[sort.col].toLowerCase();
    }
  };
  const out = [];
  for (let i = 0; i < rows.length; i++) {
    if (q && !rows[i].k.includes(q) && !rows[i].d.includes(q)) continue;
    if (above && !(alt(i) > 0)) continue;
    out.push(i);
  }
  const vals = new Map(out.map((i) => [i, value(i)]));
  return out.sort((a, b) => {
    const x = vals.get(a), y = vals.get(b);
    const xu = typeof x === "number" && Number.isNaN(x), yu = typeof y === "number" && Number.isNaN(y);
    if (xu || yu) return xu - yu; // unknown last, whatever the direction
    return (x < y ? -1 : x > y ? 1 : 0) * sort.dir;
  });
}

// targetFor is what Go to / Image send: a selected row at its exact catalog
// position, else the typed RA (hours) and Dec (degrees); null when either
// does not parse.
export function targetFor(row, raText, decText) {
  if (row) return { Name: row.Name, RA: row.RA, Dec: row.Dec, Mag: known(row.Mag) ?? 0, Size: known(row.Size) ?? 0 };
  const ra = sexa(raText) * 15, dec = sexa(decText);
  if (Number.isNaN(ra) || Number.isNaN(dec)) return null;
  return { Name: `RA ${raText.trim()} Dec ${decText.trim()}`, RA: ra, Dec: dec, Mag: 0, Size: 0 };
}

// isImagingState is the app's Origin.isImagingState: the TaskController
// states of an imaging run, during which go-to and slew are refused.
const IMAGING_STATES = new Set(["CAPTURING_MOSAIC", "IMAGING_LIST", "IMAGING_OBJECT", "RECAPTURE_OBJECT", "RECAPTURE_MOSAIC_TILE"]);
export const isImagingState = (state) => IMAGING_STATES.has(state);

// serial returns a runner that starts each call only after the previous one
// has settled, so a stop can never overtake the slew it follows.
export function serial() {
  let last = Promise.resolve();
  return (fn) => (last = last.then(fn, fn));
}

// gotoMode is the Go to button's role: Stop while the mount slews or the
// task centers the target, as the app's slewScopeOrCancelInProgress.
// sent is the native slewing flag: set as a go-to is sent, so the button
// stops at once, and replaced by the next mount status.
export const gotoMode = (mount, task, sent = false) =>
  sent || mount?.IsGotoOver === false || task?.State === "CENTERING_TARGET" ? "stop" : "goto";

// canFinish is the app's finishImageCapture condition: a single object's
// run with a stacked image (a schedule has no Finish).
export const canFinish = (state, hasStacked) => state === "IMAGING_OBJECT" && hasStacked;
// canCancel is when the app offers Cancel: any run but a schedule's recapture.
export const canCancel = (state) => isImagingState(state) && state !== "RECAPTURE_OBJECT";


// focusSteps are the fine and rough focus steps (FocusOptionsFragment).
export const focusSteps = (snapshot) => (snapshot ? [100, 1000] : [10, 100]);

// captureLabel and filterLabel are the app's capture and filter buttons.
export const captureLabel = (snapshot) => (snapshot ? "Capture Image" : "Start Imaging");
export const filterLabel = (filter) => (filter === "Nebula" ? "Nebula Filter" : "Clear Filter");

// offsetIn is where a click falls in the image's box, 0–1 from its left and
// top: the reframe crosshair's place, sent as RunCenterTarget's offsets.
export function offsetIn(rect, clientX, clientY) {
  const f = (v) => Math.min(1, Math.max(0, v));
  return { x: f((clientX - rect.left) / rect.width), y: f((clientY - rect.top) / rect.height) };
}
export const reframeText = (x, y) => `Re-Center At Image: X=${x.toFixed(2)}, Y=${y.toFixed(2)}`;

// stretchLabel is the app's slider label, e.g. "Strength (0.50)".
export const stretchLabel = (which, v) => `${which} (${v.toFixed(2)})`;

// fileStatus is the file manager's status line, free space and Copy All
// button (RemoteFilesManagerFragment.updateSpaceAndStatusLabels).
export function fileStatus(disk, transfer) {
  const pct = transfer?.PercentageComplete;
  const copying = pct > 0 && pct < 99;
  return {
    text: copying ? `Copying - ${pct}% Complete` : disk?.Level === "LOW" ? "Disk Space LOW!" : "Disk Space Ok.",
    free: disk?.FreeBytes != null ? `${(disk.FreeBytes / 1e9).toFixed(2)} GB` : "-",
    copying,
    copyLabel: copying ? "Cancel File Copy" : "Copy All To USB",
  };
}

// updateText is the version screen's update status, as far as it does not
// need Celestron's servers (so never "Already Up-To-Date").
export function updateText(connected, internet, status, differs, remote) {
  if (!connected) return "Not Connected";
  if (internet !== true) return "[Unknown]: No Internet Access";
  const pct = status?.PercentComplete;
  if (status?.Status === "UPDATE_DOWNLOADING") return pct > 0 ? `Update ${pct}% downloaded.` : "Downloading Update…";
  if (status?.Status === "UPDATE_INSTALLING") return pct > 0 ? `Install ${pct}% complete…` : "Installing Update…";
  if (differs) return `Update Available (${remote || "Latest version"})`;
  return "Already Up-To-Date";
}

// updateButton is the version screen's button (ScopeOriginVersionFragment):
// its text and whether it works. Unlike the status, only an Origin
// reporting no internet disables it.
export function updateButton(connected, internet, status, differs) {
  if (!connected) return { text: "Not Connected", enabled: false };
  if (status?.Status === "UPDATE_DOWNLOADING") return { text: "Downloading Update…", enabled: false };
  if (status?.Status === "UPDATE_INSTALLING") return { text: "Installing Update…", enabled: false };
  if (internet === false) return { text: "[Unknown]: No Internet Access", enabled: false };
  if (differs) return { text: "Download And Update Origin", enabled: true };
  return { text: "No Updates Available", enabled: false };
}

// polarText is the polar alignment screen's errors, in degrees.
export function polarText(task) {
  const p = task?.State === "POLAR_ALIGNING" ? task.PolarAlignInfo : null;
  if (!p) return "Az. Err.: -, Alt. Err: -";
  const az = (p.AzimuthError * 180) / Math.PI, alt = (p.AltitudeError * 180) / Math.PI;
  return az === 0 && alt === 0 ? "Please Wait…" : `Az. Err.: ${az.toFixed(2)}, Alt. Err: ${alt.toFixed(2)}`;
}

// polarOffset is where the polar screen's error crosshair sits, in degrees
// right (azimuth) and up (altitude), clamped to its 3° ring; null when no
// errors are known yet.
export function polarOffset(task) {
  const p = task?.State === "POLAR_ALIGNING" ? task.PolarAlignInfo : null;
  if (!p) return null;
  const c = (rad) => Math.max(-3, Math.min(3, (rad * 180) / Math.PI));
  return { x: c(p.AzimuthError), y: c(p.AltitudeError) };
}

// darkMessage is the dark frame confirmation, with the minutes it takes.
export const darkMessage = (count, exposureSec) =>
  `This will take about ${((count * exposureSec) / 60).toFixed(0)} minute(s). Are you sure you want to capture new dark frames now?`;

// batteryText and chargerText are ScopeStatusFragment's battery level and
// charger status words.
const BATTERY = { HIGH: "High", MED: "Medium", LOW: "Low", CRITICALLY_LOW: "Critically Low" };
const CHARGER = { CHARGING: "Charging", DISCHARGING: "Discharging", CHARGED: "Fully Charged", FAULT: "Fault" };
export const batteryText = (level) => BATTERY[level] ?? "–";
export const chargerText = (status) => CHARGER[status] ?? "–";

// guiderText is ScopeStatusFragment's autoguider section: the state ("No
// Autoguider" when none answered), and while guiding the RA and Dec error,
// guide stars and quality.
export function guiderText(f) {
  // A reported State shows even without IsGuiding (updateAutoguiderStatus).
  if (!f || (f.State == null && f.IsGuiding == null)) return "No Autoguider";
  const state = f.State ?? (f.IsGuiding ? "Guiding" : "Idle");
  if (!f.IsGuiding) return state;
  const parts = [state];
  if (f.RaError != null) parts.push(`RA err ${f.RaError.toFixed(2)}`);
  if (f.DecError != null) parts.push(`Dec err ${f.DecError.toFixed(2)}`);
  // The last corrections, as Celestron's web app shows them (not in the app).
  if (f.RaCorrection != null) parts.push(`RA corr ${f.RaCorrection.toFixed(2)}`);
  if (f.DecCorrection != null) parts.push(`Dec corr ${f.DecCorrection.toFixed(2)}`);
  if (f.GuideStars != null) parts.push(`${f.GuideStars} stars`);
  if (f.Quality != null) parts.push(`quality ${f.Quality}`);
  return parts.join(", ");
}

// celsius is a temperature row of the control panes; "–" when not reported.
export const celsius = (v) => (v == null ? "–" : `${v.toFixed(1)} °C`);

// latLon formats a latitude and longitude in radians, as the mount reports
// them, with N/S and E/W.
export function latLon(lat, lon) {
  const d = (rad) => Math.abs((rad * 180) / Math.PI);
  return `${dms(d(lat), false).slice(1)} ${lat < 0 ? "S" : "N"}, ${dms(d(lon), false).slice(1)} ${lon < 0 ? "W" : "E"}`;
}

// zoomAt is the image's zoom (the app's pinch zoom, here the mouse wheel):
// view {z, x, y} is a scale and a translation from the image's top left;
// px, py is the point to keep in place, from the untranslated top left.
// The scale stays within 1–8, and at 1 the image is back in its place.
export function zoomAt(view, px, py, factor) {
  const z = Math.min(8, Math.max(1, view.z * factor));
  if (z === 1) return { z: 1, x: 0, y: 0 };
  const k = z / view.z;
  return { z, x: px - k * (px - view.x), y: py - k * (py - view.y) };
}

// dewText is ScopeStatusFragment's dew heater row: the heater's power only
// in Manual mode.
export function dewText(f) {
  const mode = f.Mode == null ? "–" : f.Mode === "Auto" ? "Automatic" : f.Mode === "Manual" ? "Manual" : "?";
  const power = f.Mode === "Manual" && f.ManualPowerLevel != null ? `${(f.ManualPowerLevel * 100).toFixed(0)} %` : "–";
  return `${mode}, power ${power}`;
}

// sliderEdit turns the editor's sliders (brightness, contrast and
// saturation 0–100, red, green and blue -100–100) into the app's values
// (ImageAdjustmentsFragment.onValueChange).
export const sliderEdit = (v) => ({
  Brightness: (v.brightness - 50) / 100, Contrast: v.contrast / 100 + 0.5, Saturation: v.saturation / 100 + 0.5,
  Red: v.red / 100, Green: v.green / 100, Blue: v.blue / 100,
});

// adjustPixels applies the app's ImageFilterUtils.applyAllAdjustments to
// RGBA pixels in place, as Go's applyEdit does to the saved image: the
// saturation and contrast/brightness colour matrix, then per-channel curves.
export function adjustPixels(px, e) {
  const clampTo = (lo, hi, v) => Math.min(hi, Math.max(lo, v));
  const bri = clampTo(-0.5, 0.5, e.Brightness), con = clampTo(0.5, 1.5, e.Contrast), sat = clampTo(0.5, 1.5, e.Saturation);
  const inv = 1 - sat, wr = 0.213 * inv, wg = 0.715 * inv, wb = 0.072 * inv;
  const off = ((1 - con) * 0.5 + bri) * 255;
  const curve = (f) => {
    const k = 2 ** clampTo(-1, 1, f), a = 2 - 2 * k, c = k - 1, lut = new Uint8Array(256);
    for (let i = 0; i < 256; i++) {
      const x = i / 255;
      lut[i] = Math.round(clampTo(0, 1, k * x + a * x * x + c * x * x * x) * 255);
    }
    return lut;
  };
  const lr = curve(e.Red), lg = curve(e.Green), lb = curve(e.Blue);
  const c8 = (v) => Math.round(clampTo(0, 255, v));
  for (let i = 0; i < px.length; i += 4) {
    const r = px[i], g = px[i + 1], b = px[i + 2];
    px[i] = lr[c8(con * ((wr + sat) * r + wg * g + wb * b) + off)];
    px[i + 1] = lg[c8(con * (wr * r + (wg + sat) * g + wb * b) + off)];
    px[i + 2] = lb[c8(con * (wr * r + wg * g + (wb + sat) * b) + off)];
  }
  return px;
}

// cameraLocks is AdvancedCameraSettingsMenu.updateUI: which camera
// settings are disabled. Mode and snapshot are locked while imaging; ISO
// and exposure need manual mode and a connected Origin that is not imaging,
// and ISO also the camera's reported range (ISOSettings).
export function cameraLocks({ connected, imaging, manual, isoKnown }) {
  const manualFree = manual && connected && !imaging;
  return { mode: imaging, snapshot: imaging || !connected, iso: !(manualFree && isoKnown), exposure: !manualFree };
}
// stretchEnabled: the app's Process page works only while imaging, once
// the Origin has reported its stretch.
export const stretchEnabled = (connected, imaging, known) => connected && imaging && known;

// batteryPercent estimates the charge from BatteryVoltage, as Origin Live
// View does (not the app, which shows only BatteryLevel): 9.2 V is empty,
// 10.2 V full, linear between.
export function batteryPercent(volts) {
  if (!(volts > 0)) return null;
  return Math.round(Math.min(1, Math.max(0, (volts - 9.2) / (10.2 - 9.2))) * 100);
}

// CROP_RATIOS are the app's crop presets (CropImageFragment): Original keeps
// the image's own shape; Free none.
export const CROP_RATIOS = { Original: "image", Free: null, Square: 1, "3:2": 3 / 2, "5:3": 5 / 3, "4:3": 4 / 3, "5:4": 5 / 4, "7:5": 7 / 5, "16:9": 16 / 9 };

// cropRect turns a drag from start to p (both 0–1 of an image cw × ch
// pixels) into a crop {X, Y, W, H} whose pixel shape is ratio (width /
// height; null: free), the largest that fits the drag, anchored at start.
export function cropRect(start, p, ratio, cw, ch) {
  const sx = p.x >= start.x ? 1 : -1, sy = p.y >= start.y ? 1 : -1;
  let w = Math.abs(p.x - start.x), h = Math.abs(p.y - start.y);
  if (ratio) {
    const hFor = (ww) => (ww * cw) / (ch * ratio);
    if (hFor(w) <= h) h = hFor(w);
    else w = (h * ch * ratio) / cw;
  }
  return { X: sx > 0 ? start.x : start.x - w, Y: sy > 0 ? start.y : start.y - h, W: w, H: h };
}

// Mosaics (MosaicAreaFragment, MosaicCaptureFragment, ImagingSchedulesFragment).

// mosaicSize is the area label's angle: arc minutes, degrees from 60′.
export const mosaicSize = (arcmin) => (arcmin >= 60 ? `${(arcmin / 60).toFixed(1)}°` : `${arcmin.toFixed(1)}′`);

// tileStatus is a tile row's status text by its StackingStatus.
export function tileStatus(s) {
  return { WAITING: "Waiting", MOVING_TO_OBJECT: "Preparing…", FOCUSING_AND_MOVING_TO_OBJECT: "Preparing…", RUNNING: "Capturing…",
    DITHERING: "Dithering…", COMPLETED: "Capture Successful", FAILED: "Capture Failed!" }[s] ?? "Unknown";
}

// shareExposure is the share screen's exposure line (SkyObjectImage
// getImageCaptureExposureTime, with a composed mosaic's grid in front):
// "exposed T secs(Nx(T/N)s)", the total over the stack depth in whole
// seconds; "" when there is nothing to say.
export function shareExposure(r) {
  const grid = r?.GridW >= 1 && r?.GridH >= 1 && r.GridW * r.GridH >= 2 ? `${r.GridW}x${r.GridH} mosaic` : "";
  let exp = "";
  if (r?.ExposureSec) {
    const t = Math.trunc(r.ExposureSec);
    exp = `exposed ${t} secs`;
    if (r.StackDepth > 0) exp += `(${r.StackDepth}x${Math.trunc(t / r.StackDepth)}s)`;
  }
  return grid && exp ? `${grid}, ${exp}` : grid || exp;
}

// tileKind is a tile row's icon and colour class by its StackingStatus
// (MosaicTileAdapter): done, failed, busy (with a spinner) or waiting.
export function tileKind(s) {
  if (s === "COMPLETED") return "done";
  if (s === "FAILED") return "failed";
  if (["MOVING_TO_OBJECT", "FOCUSING_AND_MOVING_TO_OBJECT", "RUNNING", "DITHERING"].includes(s)) return "busy";
  return "waiting";
}

// mosaicDetail is a mosaic row's detail by the Origin's metadata; a
// finished one shows when it was captured ("yyyy/MM/dd HH:mm:ss" from the
// Origin) in the chosen formats, without seconds as the app's SHORT style.
export function mosaicDetail(meta, dateOrder = "YMD", timeFormat = "24h") {
  if (!meta) return "Mosaic";
  switch (meta.Status) {
    case "NOT_STARTED": return "Waiting";
    case "IN_PROGRESS": return `Captured ${meta.TilesCompleted} of ${meta.TilesTotal} Tiles`;
    case "COMPLETED": {
      const d = /^(\d{4})\/(\d{2})\/(\d{2}) (\d{2}:\d{2}):\d{2}$/.exec(meta.DateTime ?? "");
      return d ? `${calendar(d[1], d[2], d[3], dateOrder)} ${clock(d[4], timeFormat.replace(/s$/, ""))}` : `Complete (${meta.TilesTotal} tiles)`;
    }
    case "ABORTED": return `Aborted (${meta.TilesCompleted} of ${meta.TilesTotal} tiles)`;
  }
  return "Unknown";
}

// mosaicAction is the capture screen's action button for a mosaic: cancel
// while it is being captured or a tile recaptured, resume when tiles are
// missing, else none (null).
export function mosaicAction(meta, task, idle) {
  const running = task?.State === "CAPTURING_MOSAIC" && (task?.ImagingInfo?.MosaicMetadata?.ListUuid ?? task?.ImagingInfo?.ListUuid) === meta?.ListUuid;
  if (running) return { text: "Cancel Capture", enabled: true, act: "cancel" };
  if (task?.State === "RECAPTURE_MOSAIC_TILE") return { text: "Cancel Tile Re-Capture", enabled: true, act: "cancel" };
  switch (meta?.Status) {
    case "NOT_STARTED": return { text: "Waiting", enabled: false };
    case "IN_PROGRESS": return { text: "In Progress", enabled: false };
    case "COMPLETED":
    case "ABORTED":
      if (meta.TilesCompleted < meta.TilesTotal) return { text: "Re-Capture Failed or Uncaptured Tiles", enabled: idle, act: "resume" };
  }
  return null;
}

// composeButton is the capture screen's compose button; spin shows the
// spinner beside it. Only this mosaic's composition can be cancelled here.
export function composeButton(task, uuid, pending, completed, idle, composed = false) {
  if (task?.State === "COMPOSING_MOSAIC" && task.ComposingMosaicInfo?.ListUuid === uuid) {
    const pct = task.ComposingMosaicInfo?.PercentComplete;
    return { text: pct != null ? `Composing ${Math.trunc(pct)}% — Cancel` : "Composing — Cancel", enabled: true, act: "halt", spin: true };
  }
  if (pending) return { text: "Composing…", enabled: false, spin: true };
  if (composed) return { text: "Show Image", enabled: true, act: "show" }; // the downloaded composition
  return { text: "Compose Mosaic", enabled: idle && completed > 0, act: "compose" };
}

// tonightRows is the app's Tonight → Sun & Moon card
// (TonightListRowSunMoonVH) as label, value pairs, times without seconds:
// a Moon that does not rise or set says so with no time.
export function tonightRows(c, timeFormat = "24h") {
  const t = (v) => clock(v, timeFormat.replace(/s$/, ""));
  return {
    sun: [["Sunset:", t(c.SunSet)], ["Astro Dusk:", t(c.AstroDusk)], ["Astro Dawn:", t(c.AstroDawn)], ["Sunrise:", t(c.SunRise)]],
    moon: [
      c.MoonRise ? ["Rises", t(c.MoonRise)] : ["Does not rise", ""],
      c.MoonSet ? ["Sets", t(c.MoonSet)] : ["Does not set", ""],
    ],
  };
}

// imageStats is the live view strip's Min, Max, Mean and Stdev of an RGBA
// frame (canvas ImageData), over each pixel's luma, as Celestron's web client.
export function imageStats(rgba) {
  const n = rgba.length / 4;
  if (!n) return null;
  let min = 255, max = 0, sum = 0, sum2 = 0;
  for (let i = 0; i < rgba.length; i += 4) {
    const v = Math.round(0.299 * rgba[i] + 0.587 * rgba[i + 1] + 0.114 * rgba[i + 2]);
    if (v < min) min = v;
    if (v > max) max = v;
    sum += v;
    sum2 += v * v;
  }
  const mean = sum / n;
  return { min, max, mean, stdev: Math.sqrt(Math.max(0, sum2 / n - mean * mean)) };
}

// ---- The tabs of Celestron's web client the app has not ----

// wifiMode is the Wi-Fi tab's mode line from Network/GetStatus's WifiStatus.
export function wifiMode(mode, internet) {
  const base = { 1: "Initializing…", 2: "Initializing…", 3: "Initializing…", 4: "Network Client", 5: "Direct Connect" }[mode] ?? "Disconnected";
  return mode === 4 || mode === 5 ? `${base} (${internet ? "Internet" : "No Internet"})` : base;
}
// wifiBars is a network's signal as 0–4 bars (0: out of range).
export function wifiBars(dbm) {
  if (!dbm) return 0;
  return dbm >= -55 ? 4 : dbm >= -65 ? 3 : dbm >= -75 ? 2 : 1;
}
export const wifiSecurity = (auth) => ({ OPEN: "Open", COMPATIBLE: "WPA", INCOMPATIBLE: "WPA3" })[auth] ?? "—";

// The camera's exposure and ISO sliders run 0–1000 on a log scale, as the
// web client's: exposure 0.001–1000 s, ISO over the camera's range.
export const exposureAt = (pos) => 10 ** ((pos / 1000) * 6 - 3);
export const exposurePos = (s) => Math.round(((Math.log10(Math.min(Math.max(s, 0.001), 1000)) + 3) / 6) * 1000);
// isoAt snaps to the nearest of the camera's ISO values (the Origin takes
// only those), isoPos places one.
export function isoAt(pos, values) {
  const lo = values[0], hi = values.at(-1), raw = lo * (hi / lo) ** (pos / 1000);
  return values.reduce((b, v) => (Math.abs(Math.log(v / raw)) < Math.abs(Math.log(b / raw)) ? v : b));
}
export function isoPos(iso, values) {
  const lo = values[0], hi = values.at(-1);
  return hi === lo ? 0 : Math.round((Math.log(Math.min(Math.max(iso, lo), hi) / lo) / Math.log(hi / lo)) * 1000);
}
// exposureText shows an exposure to 3 significant digits (4 from 10 s).
export const exposureText = (s) => String(Number(s.toPrecision(s >= 10 ? 4 : 3)));

// visibilityText is a target's rise and set as the schedule shows them;
// fmt formats one of Go's times (RFC 3339 in the observer's zone).
export function visibilityText(v, fmt) {
  if (!v) return "–";
  if (v.NeverRises) return "Does Not Rise";
  if (v.Circumpolar) return "Does not set";
  return `Rises ${eventTime(v.Rise, fmt)} / Sets ${eventTime(v.Set, fmt)}`;
}
// eventTime formats one of Go's event times, "—" for none (Go's zero time).
export const eventTime = (s, fmt) => (s && !s.startsWith("0001-") ? fmt(s) : "—");
// zoneClock is the "hh:mm" an RFC 3339 time reads in its own zone (the
// observer's, as Go sends it), not the computer's.
export const zoneClock = (s) => s.slice(11, 16);
const ms = (s) => (s && !s.startsWith("0001-") ? Date.parse(s) : NaN);
// scheduleStarts estimates when each scheduled target starts: after the one
// before it ends, not before it rises nor before now (the Origin waits).
export function scheduleStarts(entries, vis, now) {
  let t = now;
  return entries.map((e, i) => {
    const rise = ms(vis[i]?.Rise);
    const start = Math.max(t, vis[i]?.Circumpolar || Number.isNaN(rise) || rise < now ? t : rise);
    t = start + e.DurationSec * 1000;
    return start;
  });
}
// byVisibility orders a schedule as the web client's "Sort by Visibility":
// targets up now by when they set, then those yet to rise by when they
// rise, then those that never set; it returns the indexes in order.
export function byVisibility(vis, now) {
  const key = (v) => {
    const rise = ms(v?.Rise), set = ms(v?.Set);
    if (!v || v.Circumpolar) return [2, 0];
    if (!Number.isNaN(rise) && rise > now) return [1, rise];
    return [0, Number.isNaN(set) ? Infinity : set];
  };
  return vis.map((v, i) => [key(v), i]).sort((a, b) => a[0][0] - b[0][0] || a[0][1] - b[0][1] || a[1] - b[1]).map((x) => x[1]);
}

// ---- The Planetarium: a stereographic view of the sky from inside ----

const RAD = Math.PI / 180;
// skyProject places altitude and azimuth (degrees) on the canvas for a view
// v = { alt, az, scale, cx, cy } (its centre, pixels per unit, the centre
// pixel): east is left of north, as the sky seen from below. A point near
// the view's antipode is null, or with clamp pushed far out in its
// direction (for filling the ground).
export function skyProject(alt, az, v, clamp = false) {
  const a = alt * RAD, d = (az - v.az) * RAD, a0 = v.alt * RAD;
  const cosc = Math.sin(a0) * Math.sin(a) + Math.cos(a0) * Math.cos(a) * Math.cos(d);
  let x = Math.cos(a) * Math.sin(d), y = Math.cos(a0) * Math.sin(a) - Math.sin(a0) * Math.cos(a) * Math.cos(d);
  if (cosc < -0.98) {
    if (!clamp) return null;
    // Out along its own direction to where cos c = −0.98 lies (radius 2 tan(c/2)).
    const len = Math.hypot(x, y) || 1;
    const r = 2 * Math.sqrt((1 + 0.98) / (1 - 0.98));
    x = (x / len) * r;
    y = (y / len) * r;
  } else {
    const k = 2 / (1 + cosc);
    x *= k;
    y *= k;
  }
  return [v.cx + x * v.scale, v.cy - y * v.scale];
}
// skyScale is the pixels per unit for a field of view fov degrees across
// width pixels.
export const skyScale = (fov, width) => width / 2 / (2 * Math.tan((fov * RAD) / 4));
// starRadius sizes a star by its magnitude, larger as the view narrows.
export const starRadius = (mag, fov) => Math.max(0.5, (6.8 - mag) * 0.55 * Math.min(2.5, Math.max(1, 60 / fov) ** 0.3));
// starColor tints a star by its B−V index: blue-white to orange-red.
export function starColor(bv) {
  const t = Math.min(Math.max((bv + 0.3) / 2.1, 0), 1);
  const lerp = (p, q) => Math.round(p + (q - p) * t);
  return `rgb(${lerp(170, 255)},${lerp(195, 170)},${lerp(255, 110)})`;
}

// commonName is a target's common name from its description, "" when it
// has none, as Go's catalog.CommonName: the first comma-separated part
// left once its leading designations are taken off.
const DESIGNATION = /^[A-Za-z]{1,8}\d?[ -]?\d[\w.+-]*\s*/;
export function commonName(description) {
  for (let p of (description ?? "").split(",")) {
    p = p.trim();
    for (let m; (m = DESIGNATION.exec(p)); ) p = p.slice(m[0].length).trim();
    if (/[A-Za-z]/.test(p)) return p;
  }
  return "";
}
// zoneOffset is a time zone's offset from UTC at an instant, in minutes.
export function zoneOffset(ms, zone) {
  const f = new Intl.DateTimeFormat("en-US", { timeZone: zone, hourCycle: "h23", year: "numeric", month: "numeric", day: "numeric", hour: "numeric", minute: "numeric", second: "numeric" });
  const p = Object.fromEntries(f.formatToParts(new Date(ms)).map((x) => [x.type, Number(x.value)]));
  return Math.round((Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - Math.floor(ms / 1000) * 1000) / 60000);
}
// zonedTime is the instant a local date ("YYYY-MM-DD") and time
// ("hh:mm[:ss]") are in a time zone.
export function zonedTime(date, time, zone) {
  const [y, mo, d] = date.split("-").map(Number), [h, mi, s = 0] = time.split(":").map(Number);
  const local = Date.UTC(y, mo - 1, d, h, mi, s);
  const first = local - zoneOffset(local, zone) * 60000;
  return local - zoneOffset(first, zone) * 60000; // again, across a change of offset
}
