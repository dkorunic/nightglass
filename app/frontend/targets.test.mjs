// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Run: node --test app/frontend/targets.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { CROP_RATIOS, celsius, composeButton, mosaicAction, mosaicDetail, mosaicSize, shareExposure, tileKind, tileStatus, adjustPixels, batteryPercent, batteryText, cameraLocks, canCancel, canFinish, captureLabel, chargerText, cropRect, darkMessage, dewText, dms, fileStatus, filterLabel, focusSteps, gotoMode, guiderText, isImagingState, key, latLon, offsetIn, polarText, prepare, reframeText, serial, sexa, sliderEdit, stretchEnabled, stretchLabel, targetFor, updateButton, updateText, view, zoomAt , polarOffset, angle, clock, calendar, tonightRows, imageStats, wifiMode, wifiBars, wifiSecurity, exposureAt, exposurePos, isoAt, isoPos, exposureText, visibilityText, scheduleStarts, byVisibility, zoneClock, skyProject, skyScale, starRadius, starColor } from "./targets.js";

test("sexa", () => {
  assert.equal(sexa("5 35 17"), 5 + 35 / 60 + 17 / 3600);
  assert.equal(sexa("5:35:17"), 5 + 35 / 60 + 17 / 3600);
  assert.equal(sexa(" 83.82 "), 83.82);
  assert.equal(sexa("-0 49"), -49 / 60); // the sign of a -0 degree survives
  for (const bad of ["", "x", "1 2 3 4", "1 60", "1 -2"]) assert.ok(Number.isNaN(sexa(bad)), bad);
});

test("dms round-trips through sexa", () => {
  assert.equal(dms(5 + 35 / 60 + 17 / 3600, true), "5h 35′ 17″");
  assert.equal(dms(-(5 + 27 / 60), false), "−5° 27′ 00″");
  assert.equal(dms(59.6 / 3600, false), "+0° 01′ 00″"); // rounds up, never "60″"
});

test("key", () => assert.equal(key(" NGC  1976 "), "ngc1976"));

const rows = [
  { Name: "M 42", Description: "NGC 1976 Great Orion Nebula", Type: "Nebula", Mag: 4, Size: 85 },
  { Name: "M 31", Description: "NGC 224 Andromeda Galaxy", Type: "Galaxy", Mag: 3.4, Size: 190 },
  { Name: "LDN 1", Description: "", Type: "Dark Nebula", Mag: -9999, Size: 0 },
].map(prepare);
const pos = [38.7, 182.7, -10, 300, 60, 90]; // alt, az per row

test("search matches name, cross-ID and common name without spaces", () => {
  for (const q of ["m42", "M 42", "NGC1976", "orion"]) assert.deepEqual(view(rows, pos, q, false, { col: "Name", dir: 1 }), [0], q);
  assert.deepEqual(view(rows, pos, "nebula", false, { col: "Name", dir: 1 }), [2, 0]);
});

test("above horizon and altitude sort", () => {
  assert.deepEqual(view(rows, pos, "", true, { col: "alt", dir: -1 }), [2, 0]);
  assert.deepEqual(view(rows, [], "", true, { col: "alt", dir: -1 }), []); // no location: nothing is known up
});

test("unknown magnitude sorts last in both directions", () => {
  assert.deepEqual(view(rows, pos, "", false, { col: "Mag", dir: 1 }), [1, 0, 2]);
  assert.deepEqual(view(rows, pos, "", false, { col: "Mag", dir: -1 }), [0, 1, 2]);
});

test("a selected row is sent at its exact catalog position", () => {
  const row = { Name: "M 42", RA: 83.8221, Dec: -5.3911, Mag: 4, Size: 85 };
  assert.deepEqual(targetFor(row, "5 35 17", "-5 23 28"), { Name: "M 42", RA: 83.8221, Dec: -5.3911, Mag: 4, Size: 85 });
  assert.deepEqual(targetFor(null, "5 35 17", "-5 23 28"),
    { Name: "RA 5 35 17 Dec -5 23 28", RA: sexa("5 35 17") * 15, Dec: sexa("-5 23 28"), Mag: 0, Size: 0 });
  assert.equal(targetFor(null, "x", "1"), null);
  assert.equal(targetFor({ Name: "Dark", RA: 1, Dec: 2, Mag: -9999, Size: -9999 }, "", "").Mag, 0); // unknown magnitude
});

test("serial runs calls in order, even past a slow or failing one", async () => {
  const run = serial();
  const log = [];
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  run(async () => { await sleep(20); log.push("slew"); });
  run(async () => { throw new Error("boom"); });
  await run(async () => log.push("stop"));
  assert.deepEqual(log, ["slew", "stop"]);
});

test("imaging states are the app's Origin.isImagingState (2, 4, 5, 6, 7)", () => {
  for (const s of ["CAPTURING_MOSAIC", "IMAGING_LIST", "IMAGING_OBJECT", "RECAPTURE_OBJECT", "RECAPTURE_MOSAIC_TILE"]) assert.ok(isImagingState(s), s);
  for (const s of ["SAMPLE_CAPTURE", "IDLE", "FOCUSING", undefined]) assert.ok(!isImagingState(s), String(s));
});

test("Go to becomes Stop while slewing or centering (the app's slewScopeOrCancelInProgress)", () => {
  assert.equal(gotoMode({ IsGotoOver: false }, { State: "IDLE" }), "stop");
  assert.equal(gotoMode({ IsGotoOver: true }, { State: "CENTERING_TARGET" }), "stop");
  assert.equal(gotoMode({ IsGotoOver: true }, { State: "IDLE" }, true), "stop"); // a go-to just sent
  assert.equal(gotoMode({ IsGotoOver: true }, { State: "IDLE" }), "goto");
  assert.equal(gotoMode({ IsGotoOver: true }, { State: "IDLE" }), "goto");
  assert.equal(gotoMode({}, {}), "goto");
});

test("Initialize cancels while initializing", () => {
});

test("Finish needs an object or list run and a stacked image (finishImageCapture)", () => {
  assert.ok(canFinish("IMAGING_OBJECT", true));
  assert.ok(!canFinish("IMAGING_LIST", true));
  assert.ok(canCancel("IMAGING_LIST") && canCancel("CAPTURING_MOSAIC") && !canCancel("RECAPTURE_OBJECT") && !canCancel("IDLE"));
  assert.ok(!canFinish("IMAGING_OBJECT", false));
  assert.ok(!canFinish("RECAPTURE_OBJECT", true));
});


test("Focus steps: ±10/±100, ±100/±1000 in snapshot mode (FocusOptionsFragment)", () => {
  assert.deepEqual(focusSteps(false), [10, 100]);
  assert.deepEqual(focusSteps(true), [100, 1000]);
});

test("Capture and filter buttons say what the app's do", () => {
  assert.equal(captureLabel(false), "Start Imaging");
  assert.equal(captureLabel(true), "Capture Image");
  assert.equal(filterLabel("Nebula"), "Nebula Filter");
  assert.equal(filterLabel("Clear"), "Clear Filter");
  assert.equal(filterLabel(undefined), "Clear Filter");
});

test("Reframe: the click's place in the image, and the app's status text", () => {
  const r = { left: 100, top: 50, width: 400, height: 200 };
  assert.deepEqual(offsetIn(r, 200, 100), { x: 0.25, y: 0.25 });
  assert.deepEqual(offsetIn(r, 0, 999), { x: 0, y: 1 }); // clamped to the image
  assert.equal(reframeText(0.25, 0.5), "Re-Center At Image: X=0.25, Y=0.50");
});

test("Stretch labels", () => {
  assert.equal(stretchLabel("Strength", 0.5), "Strength (0.50)");
  assert.equal(stretchLabel("Background", 0.123), "Background (0.12)");
});

test("File manager status (updateSpaceAndStatusLabels)", () => {
  assert.deepEqual(fileStatus({ Level: "OK", FreeBytes: 12.345e9 }, null),
    { text: "Disk Space Ok.", free: "12.35 GB", copying: false, copyLabel: "Copy All To USB" });
  assert.deepEqual(fileStatus({ Level: "LOW" }, { PercentageComplete: 40 }),
    { text: "Copying - 40% Complete", free: "-", copying: true, copyLabel: "Cancel File Copy" });
  assert.equal(fileStatus({ Level: "OK" }, { PercentageComplete: 100 }).copying, false);
});

test("Update status (ScopeOriginVersionFragment)", () => {
  assert.equal(updateText(false, true, null), "Not Connected");
  assert.equal(updateText(true, false, null), "[Unknown]: No Internet Access");
  assert.equal(updateText(true, true, { Status: "UPDATE_DOWNLOADING", PercentComplete: 0 }), "Downloading Update…");
  assert.equal(updateText(true, true, { Status: "UPDATE_DOWNLOADING", PercentComplete: 42 }), "Update 42% downloaded.");
  assert.equal(updateText(true, true, { Status: "UPDATE_INSTALLING" }), "Installing Update…");
  assert.equal(updateText(true, true, { Status: "UPDATE_INSTALLING", PercentComplete: 7 }), "Install 7% complete…");
  // UPDATE_FAILED has no text of its own in the app: it falls through.
  assert.equal(updateText(true, true, { Status: "UPDATE_FAILED" }), "Already Up-To-Date");
});

test("Polar alignment errors (PolarAlignFragment)", () => {
  assert.equal(polarText({ State: "IDLE" }), "Az. Err.: -, Alt. Err: -");
  assert.equal(polarText({ State: "POLAR_ALIGNING" }), "Az. Err.: -, Alt. Err: -");
  assert.equal(polarText({ State: "POLAR_ALIGNING", PolarAlignInfo: { AzimuthError: 0, AltitudeError: 0 } }), "Please Wait…");
  assert.equal(polarText({ State: "POLAR_ALIGNING", PolarAlignInfo: { AzimuthError: Math.PI / 180, AltitudeError: -Math.PI / 360 } }),
    "Az. Err.: 1.00, Alt. Err: -0.50");
});

test("Dark frame confirmation gives the time it takes", () => {
  assert.equal(darkMessage(10, 10), "This will take about 2 minute(s). Are you sure you want to capture new dark frames now?");
  assert.equal(darkMessage(100, 30), "This will take about 50 minute(s). Are you sure you want to capture new dark frames now?");
});

test("status words as ScopeStatusFragment", () => {
  assert.equal(batteryText("MED"), "Medium");
  assert.equal(batteryText("CRITICALLY_LOW"), "Critically Low");
  assert.equal(batteryText(undefined), "–");
  assert.equal(chargerText("CHARGED"), "Fully Charged");
  assert.equal(chargerText("FAULT"), "Fault");
  assert.equal(guiderText(undefined), "No Autoguider");
  assert.equal(guiderText({ IsGuiding: false }), "Idle");
  assert.equal(guiderText({ State: "CALIBRATING" }), "CALIBRATING");
  assert.equal(guiderText({ IsGuiding: true, RaError: 0.123, DecError: -0.5, GuideStars: 3, Quality: 80 }),
    "Guiding, RA err 0.12, Dec err -0.50, 3 stars, quality 80");
  assert.equal(guiderText({ IsGuiding: false, State: "CALIBRATING" }), "CALIBRATING");
  assert.equal(latLon((45.5 * Math.PI) / 180, (-16.25 * Math.PI) / 180), "45° 30′ 00″ N, 16° 15′ 00″ W");
});

test("zoomAt keeps the point under the cursor", () => {
  const v = zoomAt({ z: 1, x: 0, y: 0 }, 100, 50, 2);
  assert.deepEqual(v, { z: 2, x: -100, y: -50 });
  // The image point under (100, 50) is still (100 - x) / z = 100.
  const w = zoomAt(v, 100, 50, 1.5);
  assert.equal((100 - w.x) / w.z, 100);
  assert.equal((50 - w.y) / w.z, 50);
  assert.deepEqual(zoomAt(v, 10, 10, 0.1), { z: 1, x: 0, y: 0 });
  assert.equal(zoomAt({ z: 8, x: 0, y: 0 }, 0, 0, 2).z, 8);
});

test("status screen dew and weather", () => {
  assert.equal(dewText({ Mode: "Auto", ManualPowerLevel: 0.5, HeaterLevel: 0.2 }), "Automatic, power –");
  assert.equal(dewText({ Mode: "Manual", ManualPowerLevel: 0.42 }), "Manual, power 42 %");
  // The panes' temperature rows.
  assert.equal(celsius(42.84), "42.8 °C");
  assert.equal(celsius(-4.96), "-5.0 °C");
  assert.equal(celsius(undefined), "–");
});

test("status extras from firmware 1.5.6286", () => {
  // The autoguider's last corrections.
  assert.equal(guiderText({ IsGuiding: true, RaError: 0.1, DecError: 0.2, RaCorrection: 0.31, DecCorrection: -0.05 }),
    "Guiding, RA err 0.10, Dec err 0.20, RA corr 0.31, Dec corr -0.05");
});

// The same hand-worked values as Go's TestApplyEditLikeApp.
test("adjustPixels like the app", () => {
  const one = (rgb, e) => [...adjustPixels(new Uint8ClampedArray([...rgb, 255]), e)].slice(0, 3);
  const none = sliderEdit({ brightness: 50, contrast: 50, saturation: 50, red: 0, green: 0, blue: 0 });
  assert.deepEqual(none, { Brightness: 0, Contrast: 1, Saturation: 1, Red: 0, Green: 0, Blue: 0 });
  assert.deepEqual(one([200, 200, 200], none), [200, 200, 200]);
  assert.deepEqual(one([200, 200, 200], { ...none, Contrast: 1.5 }), [236, 236, 236]);
  assert.deepEqual(one([255, 0, 0], { ...none, Saturation: 0.5 }), [155, 27, 27]);
  assert.deepEqual(one([128, 128, 128], { ...none, Red: 1, Blue: -1 }), [160, 128, 112]);
});

test("camera settings locked as AdvancedCameraSettingsMenu", () => {
  assert.deepEqual(cameraLocks({ connected: true, imaging: false, manual: true, isoKnown: true }), { mode: false, snapshot: false, iso: false, exposure: false });
  assert.deepEqual(cameraLocks({ connected: true, imaging: true, manual: true, isoKnown: true }), { mode: true, snapshot: true, iso: true, exposure: true });
  assert.deepEqual(cameraLocks({ connected: true, imaging: false, manual: false, isoKnown: true }), { mode: false, snapshot: false, iso: true, exposure: true });
  assert.deepEqual(cameraLocks({ connected: true, imaging: false, manual: true, isoKnown: false }), { mode: false, snapshot: false, iso: true, exposure: false });
  assert.deepEqual(cameraLocks({ connected: false, imaging: false, manual: true, isoKnown: false }), { mode: false, snapshot: true, iso: true, exposure: true });
  assert.equal(stretchEnabled(true, true, true), true);
  assert.equal(stretchEnabled(true, false, true), false);
  assert.equal(stretchEnabled(true, true, false), false);
});

test("batteryPercent as Origin Live View estimates it", () => {
  assert.equal(batteryPercent(9.7), 50);
  assert.equal(batteryPercent(10.2), 100);
  assert.equal(batteryPercent(12.1), 100);
  assert.equal(batteryPercent(9.0), 0);
  assert.equal(batteryPercent(9.2), 0);
  assert.equal(batteryPercent(undefined), null);
  assert.equal(batteryPercent(0), null);
});

test("cropRect keeps the preset's shape", () => {
  const r = (c) => Object.fromEntries(Object.entries(c).map(([k, v]) => [k, Math.round(v * 1000) / 1000]));
  // A 400 × 200 image, square crop: a drag of 0.5 × 0.5 is 200 × 100 px, so
  // the square fitting it is 100 px: W 0.25, H 0.5.
  assert.deepEqual(r(cropRect({ x: 0, y: 0 }, { x: 0.5, y: 0.5 }, CROP_RATIOS.Square, 400, 200)), { X: 0, Y: 0, W: 0.25, H: 0.5 });
  // Original on the same image: the drag box itself when it has the image's shape.
  assert.deepEqual(r(cropRect({ x: 0, y: 0 }, { x: 0.5, y: 0.5 }, 400 / 200, 400, 200)), { X: 0, Y: 0, W: 0.5, H: 0.5 });
  // Free keeps the drag; dragging up-left anchors at the start.
  assert.deepEqual(r(cropRect({ x: 0.8, y: 0.9 }, { x: 0.5, y: 0.3 }, null, 400, 200)), { X: 0.5, Y: 0.3, W: 0.3, H: 0.6 });
});

test("update status and button as ScopeOriginVersionFragment", () => {
  assert.equal(updateText(true, true, null, true, "1.5.6240"), "Update Available (1.5.6240)");
  assert.equal(updateText(true, true, null, true, ""), "Update Available (Latest version)");
  assert.equal(updateText(true, true, null, false), "Already Up-To-Date");
  assert.deepEqual(updateButton(false, true, null, true), { text: "Not Connected", enabled: false });
  assert.deepEqual(updateButton(true, null, null, true), { text: "Download And Update Origin", enabled: true });
  assert.deepEqual(updateButton(true, false, null, true), { text: "[Unknown]: No Internet Access", enabled: false });
  assert.deepEqual(updateButton(true, true, { Status: "UPDATE_INSTALLING" }, true), { text: "Installing Update…", enabled: false });
  assert.deepEqual(updateButton(true, true, null, false), { text: "No Updates Available", enabled: false });
});

test("mosaic labels and buttons as the app's mosaic screens", () => {
  assert.equal(mosaicSize(45.25), "45.3′");
  assert.equal(mosaicSize(90), "1.5°");
  assert.equal(tileStatus("COMPLETED"), "Capture Successful");
  assert.equal(tileStatus("FOCUSING_AND_MOVING_TO_OBJECT"), "Preparing…");
  assert.equal(tileStatus("X"), "Unknown");
  assert.deepEqual(["COMPLETED", "FAILED", "DITHERING", "MOVING_TO_OBJECT", "WAITING", undefined].map(tileKind), ["done", "failed", "busy", "busy", "waiting", "waiting"]);
  assert.equal(mosaicDetail({ Status: "ABORTED", TilesCompleted: 2, TilesTotal: 6 }), "Aborted (2 of 6 tiles)");
  assert.equal(mosaicDetail(null), "Mosaic");
  assert.equal(mosaicDetail({ Status: "COMPLETED", DateTime: "2026/10/22 21:05:33", TilesTotal: 4 }, "DMY", "24hs"), "22/10/2026 21:05");
  assert.equal(mosaicDetail({ Status: "COMPLETED", DateTime: "2026/10/22 21:05:33", TilesTotal: 4 }, "YMD", "12h"), "2026-10-22 9:05 PM");
  assert.equal(mosaicDetail({ Status: "COMPLETED", DateTime: "yesterday", TilesTotal: 4 }), "Complete (4 tiles)");
  const meta = { ListUuid: "M", Status: "ABORTED", TilesCompleted: 2, TilesTotal: 6 };
  assert.deepEqual(mosaicAction(meta, { State: "IDLE" }, true), { text: "Re-Capture Failed or Uncaptured Tiles", enabled: true, act: "resume" });
  assert.equal(mosaicAction(meta, { State: "CAPTURING_MOSAIC", ImagingInfo: { MosaicMetadata: { ListUuid: "M" } } }, false).act, "cancel");
  assert.equal(mosaicAction({ ...meta, TilesCompleted: 6 }, { State: "IDLE" }, true), null);
  assert.deepEqual(composeButton({ State: "COMPOSING_MOSAIC", ComposingMosaicInfo: { PercentComplete: 99.6, ListUuid: "M" } }, "M"), { text: "Composing 99% — Cancel", enabled: true, act: "halt", spin: true });
  // Another mosaic composing: this one's button waits for an idle Origin.
  assert.deepEqual(composeButton({ State: "COMPOSING_MOSAIC", ComposingMosaicInfo: { ListUuid: "N" } }, "M", false, 3, false), { text: "Compose Mosaic", enabled: false, act: "compose" });
  assert.deepEqual(composeButton({ State: "IDLE" }, "M", false, 0, true), { text: "Compose Mosaic", enabled: false, act: "compose" });
  assert.deepEqual(composeButton({ State: "IDLE" }, "M", false, 3, true, true), { text: "Show Image", enabled: true, act: "show" });
  assert.deepEqual(composeButton({ State: "IDLE" }, "M", true, 3, true, true), { text: "Composing…", enabled: false, spin: true }); // pending first
});

test("polarOffset: degrees right and up, clamped to the 3° ring", () => {
  const r = Math.PI / 180;
  assert.equal(polarOffset({ State: "IDLE" }), null);
  assert.deepEqual(polarOffset({ State: "POLAR_ALIGNING", PolarAlignInfo: { AzimuthError: 1.5 * r, AltitudeError: -0.5 * r } }), { x: 1.5, y: -0.5 });
  assert.deepEqual(polarOffset({ State: "POLAR_ALIGNING", PolarAlignInfo: { AzimuthError: -9 * r, AltitudeError: 4 * r } }), { x: -3, y: 3 });
});

test("formats: coordinates, time and date as chosen", () => {
  const r = Math.PI / 180;
  assert.equal(angle(83.8221 * r, true, false, "dms"), "5h 35′ 17″");
  assert.equal(angle(-5.391 * r, false, false, "dm"), "−5° 23.5′");
  assert.equal(angle(350.5 * r, false, true, "deg"), "350.500°");
  assert.equal(angle(null), "-");
  assert.equal(clock("21:05:09", "24hs"), "21:05:09");
  assert.equal(clock("21:05:09", "12h"), "9:05 PM");
  assert.equal(clock("00:30:00", "12hs"), "12:30:00 AM");
  assert.equal(calendar(2026, 1, 15, "DMY"), "15/01/2026");
  assert.equal(calendar(2026, 1, 15, "YMD"), "2026-01-15");
});

test("share exposure line, as SkyObjectImage", () => {
  assert.equal(shareExposure({ ExposureSec: 600, StackDepth: 60 }), "exposed 600 secs(60x10s)");
  assert.equal(shareExposure({ ExposureSec: 45.9 }), "exposed 45 secs");
  assert.equal(shareExposure({ ExposureSec: 300, StackDepth: 30, GridW: 3, GridH: 2 }), "3x2 mosaic, exposed 300 secs(30x10s)");
  assert.equal(shareExposure({ GridW: 1, GridH: 1, ExposureSec: 10, StackDepth: 1 }), "exposed 10 secs(1x10s)");
  assert.equal(shareExposure({ GridW: 2, GridH: 1 }), "2x1 mosaic");
  assert.equal(shareExposure(null), "");
});

test("tonightRows", () => {
  const c = { SunSet: "16:38", AstroDusk: "18:24", AstroDawn: "05:48", SunRise: "07:33", MoonRise: "", MoonSet: "13:10" };
  assert.deepEqual(tonightRows(c, "24hs"), {
    sun: [["Sunset:", "16:38"], ["Astro Dusk:", "18:24"], ["Astro Dawn:", "05:48"], ["Sunrise:", "07:33"]],
    moon: [["Does not rise", ""], ["Sets", "13:10"]],
  });
  const r = tonightRows({ ...c, SunSet: "", MoonRise: "06:14" }, "12h");
  assert.deepEqual(r.sun[0], ["Sunset:", "-"]); // the midnight sun
  assert.deepEqual(r.moon[0], ["Rises", "6:14 AM"]);
});

test("imageStats is over each pixel's luma", () => {
  // Black, white, pure red (luma 76) and pure green (150).
  const s = imageStats(new Uint8ClampedArray([0, 0, 0, 255, 255, 255, 255, 255, 255, 0, 0, 255, 0, 255, 0, 255]));
  assert.deepEqual([s.min, s.max, s.mean], [0, 255, 120.25]);
  assert.ok(Math.abs(s.stdev - Math.sqrt((120.25 ** 2 + 134.75 ** 2 + 44.25 ** 2 + 29.75 ** 2) / 4)) < 1e-9);
  assert.equal(imageStats(new Uint8ClampedArray()), null);
});

test("Wi-Fi tab texts", () => {
  assert.equal(wifiMode(4, true), "Network Client (Internet)");
  assert.equal(wifiMode(5, 0), "Direct Connect (No Internet)");
  assert.equal(wifiMode(2), "Initializing…");
  assert.equal(wifiMode(-1), "Disconnected");
  assert.deepEqual([-50, -60, -70, -90, 0].map(wifiBars), [4, 3, 2, 1, 0]);
  assert.equal(wifiSecurity("INCOMPATIBLE"), "WPA3");
  assert.equal(wifiSecurity("X"), "—");
});

test("camera log sliders", () => {
  assert.equal(exposureAt(0), 0.001);
  assert.equal(exposureAt(1000), 1000);
  assert.equal(exposurePos(10), 667);
  assert.equal(exposurePos(1e9), 1000);
  const isos = [100, 200, 500, 1000, 2000, 5000, 10000, 20000];
  assert.equal(isoAt(0, isos), 100);
  assert.equal(isoAt(1000, isos), 20000);
  assert.equal(isoAt(480, isos), 1000);
  for (const v of isos) assert.equal(isoAt(isoPos(v, isos), isos), v);
  assert.equal(exposureText(0.123456), "0.123");
  assert.equal(exposureText(12.3456), "12.35");
});

test("schedule visibility, starts and order", () => {
  const at = (h) => new Date(Date.UTC(2026, 10, 28, h)).toISOString();
  const fmt = (s) => `${Number(zoneClock(s).slice(0, 2))}h`;
  assert.equal(visibilityText({ Rise: at(18), Set: at(23) }, fmt), "Rises 18h / Sets 23h");
  assert.equal(visibilityText({ Rise: "0001-01-01T00:00:00Z", Set: at(23) }, fmt), "Rises — / Sets 23h");
  assert.equal(visibilityText({ Circumpolar: true }, fmt), "Does not set");
  assert.equal(visibilityText({ NeverRises: true }, fmt), "Does Not Rise");
  const now = Date.parse(at(17));
  // Up now (set 20h), rising at 21h, never setting, up now (set 19h).
  const vis = [{ Rise: at(10), Set: at(20) }, { Rise: at(21), Set: at(23) }, { Circumpolar: true }, { Rise: at(9), Set: at(19) }];
  assert.deepEqual(byVisibility(vis, now), [3, 0, 1, 2]);
  // 10 min, then one that waits for its rise at 21h, then 30 min after it.
  const starts = scheduleStarts([{ DurationSec: 600 }, { DurationSec: 600 }, { DurationSec: 1800 }], vis.slice(0, 3), now);
  assert.deepEqual(starts, [now, Date.parse(at(21)), Date.parse(at(21)) + 600000]);
});

test("zoneClock reads the observer's clock", () => {
  assert.equal(zoneClock("2026-11-28T18:22:05+01:00"), "18:22");
});

test("planetarium projection", () => {
  const v = { alt: 90, az: 180, scale: 100, cx: 500, cy: 400 };
  // The zenith at the centre; north up, east left, the horizon at radius 2.
  assert.deepEqual(skyProject(90, 0, v).map(Math.round), [500, 400]);
  const [nx, ny] = skyProject(0, 0, v), [ex] = skyProject(0, 90, v);
  assert.ok(Math.abs(nx - 500) < 1e-9 && Math.abs(ny - 200) < 1e-9, "north up");
  assert.ok(Math.abs(ex - 300) < 1e-9, "east left");
  // Facing south at the horizon: the antipode (north, below) is not drawn.
  const s = { alt: 0, az: 180, scale: 100, cx: 0, cy: 0 };
  assert.equal(skyProject(0, 0, s), null);
  assert.ok(skyProject(0, 0, s, true));
  // Clamped points lie far out in their own direction: below the zenith
  // view's horizon (radius 2), never back near its centre.
  for (const az of [0, 77, 200]) {
    const [x, y] = skyProject(-89.9, az, v, true);
    assert.ok(Math.hypot(x - 500, y - 400) > 900, `nadir at az ${az}`);
  }
  // A 180° field spans the width exactly.
  const w = 800, sc = skyScale(180, w), [x0] = skyProject(0, 90, { alt: 0, az: 180, scale: sc, cx: w / 2, cy: 0 });
  assert.ok(Math.abs(x0) < 1e-9);
  assert.ok(starRadius(-1.5, 180) > starRadius(6, 180));
  assert.equal(starColor(-0.3), "rgb(170,195,255)");
});
