// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Nightglass frontend. Every call goes to app/control.App through Wails.
import { Browser, Events } from "/wails/runtime.js";
import { sliderLabels, $, $$, act, call, choice, confirmBox, showAlert, showPane, toast } from "./ui.js";
import {
  composeButton, mosaicAction, mosaicDetail, mosaicSize, tileStatus,
  adjustPixels, angle, batteryPercent, batteryTemp, cameraExtra, calendar, clock, batteryText, cameraLocks, canCancel, canFinish, captureLabel, chargerText, cropRect, CROP_RATIOS, darkMessage, dewText, dms, envText, fileStatus, filterLabel, focusSteps, freeSpace, gotoMode, guiderText,
  isImagingState, known, latLon, offsetIn, prepare, rateSpeed, reframeText, serial, stretchLabel, targetFor, updateButton, updateText, view,
  sliderEdit, stretchEnabled, tonightRows, zoomAt,
} from "./targets.js";
import { loadFiles, updateFilesStatus } from "./files.js";
import { originStatus, showCalibrationButtons, showLogsButton, showPolar, showUpdate } from "./origin.js";
import { mosaicTaskStatus, renderMosaicCapture } from "./mosaics.js";
import { showQuickStart } from "./quickstart.js";
import { viewImage } from "./gallery.js"; // the gallery and editor register their own handlers

const ROW = 24; // px, matches --row in style.css
const deg = (rad) => (rad * 180) / Math.PI;
export let settings = {};
export let connected = false;
let slewing = false;
let shownKind = ""; // the frame Go chose to show (Activity.Image)
const reframe = { on: false, x: 0.5, y: 0.5 }; // Q: the crosshair, 0–1 in the image
const frames = {}; // kind -> {Seq, Info}

// ---------- connection ----------

function setConnected(c, host, error) {
  connected = c;
  const badge = $("#conn");
  badge.className = `badge ${c ? "on" : "off"}`;
  badge.textContent = c ? `Connected to ${host}` : host ? `Reconnecting to ${host}…` : "Not connected";
  $("#banner").hidden = c || !host;
  $("#st-host").textContent = host || "-";
  $$(".needs-conn").forEach((el) => (el.disabled = !c));
  if (!c) slewing = gotoSent = false; // Go stops the mount when the link returns
  if (!c) stretchKnown = false; // asked again on the next connect
  if (!c) { for (const k of Object.keys(frames)) delete frames[k]; show(); } // Go drops them with the link
  if (!c) { // the next link reports afresh (the app resets its state on disconnect)
    lastMount = {}; lastTask = {}; lastFocus = {}; lastFilter = undefined; lastDisk = {}; lastTransfer = null; lastUpdate = null;
    forced = "";
    updateRunButtons();
    if ($("#polar-dialog").open) $("#polar-dialog").close(); // PolarAlignFragment.telescopeConnectionClosed
  }
  updateStretch();
  updateTargetButtons();
  updateViewerButtons();
  updateFocusUI();
  if (error && host) console.warn("connection:", error); // the app shows nothing of its own as the link drops
}

Events.On("conn", async (ev) => {
  const { Connected, Host, Error } = ev.data;
  setConnected(Connected, Host, Error);
  if (Connected) for (const s of (await act("Status")) || []) applyStatus(s.Key, s.Fields);
  if (Connected && $("#files-dialog").open) loadFiles(); // reloaded on reconnect
});

// locationOK is the app's locationSufficientForOriginConnection, offering
// the location settings when it is not.
export async function locationOK() {
  if (Math.abs(settings.LatDeg ?? 0) >= 0.0057 || Math.abs(settings.LonDeg ?? 0) >= 0.0057) return true;
  if (await choice("Precise Location Unavailable",
    "Nightglass was unable to determine your location accurately enough to display your sky. Please set your location manually and attempt to reconnect.",
    ["Open Location Settings", "Cancel"]) === 0) $("#open-settings").click();
  return false;
}
// connect connects to host from the connect dialog: manual when typed
// rather than picked from a search (the app's manual IP address).
async function connect(host, manual = false) {
  if (!(await locationOK())) return;
  // Frames belong to one telescope; Go forgets them on Connect too.
  for (const k of Object.keys(frames)) delete frames[k];
  $("#save-final").disabled = true;
  show();
  if ((await act("ConnectTo", host, manual)) !== false) $("#connect-dialog").close();
}

$("#open-connect").onclick = () => {
  $("#host").value = settings.Host ?? "";
  picked = settings.ManualHost ? "" : settings.Host ?? ""; // a searched address stays searched
  $("#connect-dialog").showModal();
};
$("#discover").onclick = async () => {
  $("#discover-state").textContent = "Searching (11 s)…";
  $("#found").replaceChildren();
  const ips = (await act("Discover")) || [];
  $("#discover-state").textContent = ips.length ? "" : "None found; enter the address below.";
  for (const ip of ips) {
    const b = document.createElement("button");
    b.textContent = ip;
    b.onclick = () => { $("#host").value = picked = ip; };
    const li = document.createElement("li");
    li.append(b);
    $("#found").append(li);
  }
};
// A typed address is checked first, as the app's manual IP address.
let picked = "";
$("#connect").onclick = async () => {
  const host = $("#host").value;
  try {
    await call("CheckHost", host);
  } catch (e) {
    const [title, ...rest] = String(e?.message ?? e).split(": ");
    if (await choice(title, rest.join(": "), ["Try Automatically", "Cancel"]) === 0) $("#discover").click();
    return;
  }
  connect(host, host !== picked);
};
// Several Origins found: one is picked (OriginScopeSelectionMenu); none:
// Quick Start, as the app's connectOrigin.
Events.On("origins", async (ev) => {
  const hosts = ev.data.Hosts;
  const i = await choice("Choose an Origin", "", [...hosts, "Cancel"]);
  if (connected) return; // connected meanwhile
  if (i >= 0 && i < hosts.length) connect(hosts[i]);
  // No pick: the app's automatic connection failure ($19).
  else showAlert("Automatic Telescope Wi-Fi Connection Failed",
    `Nightglass can't make a wireless network connection to the telescope. Make sure it's on and connected to your local Wi-Fi network, or that you've joined its Wi-Fi network named with the prefix "Origin-".`);
});
// A typed address that does not answer is offered a search
// (origin_manual_telescope_ip_invalid; its Wi-Fi settings button is §7).
Events.On("manualinvalid", async (ev) => {
  if (await choice("Manual IP Address Invalid",
    `Nightglass cannot find your Origin telescope at the IP address you have manually requested (${ev.data.Host}).`,
    ["Try Automatically", "Cancel"]) === 0) act("ConnectOrigin");
});
Events.On("quickstart", () => showQuickStart());
// The connection badge, as the app's status button while disconnected.
let waiting = false;
$("#conn").onclick = async () => {
  if (connected) return;
  const text = "Check the telescope Wi-Fi settings or attempt to connect?";
  if (waiting) {
    if (await choice("Waiting to Reconnect", text, ["Attempt To Connect", "Cancel"]) === 0) act("ReconnectNow");
  } else if (await choice("No Telescope Connection", text, ["Connect", "Cancel"]) === 0) {
    if (await locationOK()) act("ConnectOrigin"); // the app's connectOrigin
  }
};
$("#disconnect").onclick = async () => {
  await act("Disconnect");
  $("#connect-dialog").close();
};
$$("dialog .close").forEach((b) => (b.onclick = () => b.closest("dialog").close()));

// ---------- status ----------

const camera = { params: "–", mode: "" };
export let lastMount = {}, lastTask = {}, lastFocus = {}, lastFilter, lastDisk = {}, lastTransfer = null, lastUpdate = null;
export let lastOrigin = null; // the Origin settings last shown
let focusLimits = { Min: 1000, Max: 35000 }; // the app's defaults until GetPositionLimits

// The buttons whose role follows the telescope, as in the app: Go to/Stop,
// Initialize/Cancel initialization, Finish and Cancel imaging.
function updateRunButtons() {
  // The slew pad works only while the Origin is idle; ■ always stops.
  for (const b of $$(".pad [data-alt]")) b.disabled = !connected || lastTask.State !== "IDLE";
  $("#goto").textContent = gotoMode(lastMount, lastTask, gotoSent) === "stop" ? "Stop" : "Go to";
  // Nightglass's own Halt (the app has none): not while imaging, where it
  // would end the run without its final image (use Finish or Cancel).
  $("#halt").disabled = !connected || isImagingState(lastTask.State);
  updateTargetButtons();
  $("#finish-imaging").disabled = !connected || !canFinish(lastTask.State, !!frames.stacked);
  $("#cancel-imaging").disabled = !connected || !canCancel(lastTask.State);
  // The app tints its run buttons red while a run is on.
  for (const id of ["#finish-imaging", "#cancel-imaging"]) $(id).classList.toggle("running", connected && isImagingState(lastTask.State));
  $("#cancel-imaging").textContent = ["CAPTURING_MOSAIC", "RECAPTURE_MOSAIC_TILE"].includes(lastTask.State) ? "Cancel Mosaic" : "Cancel imaging";
  if ($("#mosaic-capture-dialog").open) renderMosaicCapture();
  updateViewerButtons();
  updateFocusUI();
}
function applyStatus(k, f) {
  const set = (id, text) => ($(id).textContent = text);
  originStatus(k); // the open Origin settings follow the Origin
  switch (k) {
    case "TaskController/GetStatus":
      lastTask = f;
      mosaicTaskStatus();
      updateStretch();
      showPolar(f);
      if (lastOrigin) showCalibrationButtons();
      updateRunButtons();
      break;
    case "Mount/GetStatus":
      // ScopeStatusFragment's rows: "–" for anything not reported.
      set("#st-aligned", f.IsAligned == null ? "–" : f.IsAligned ? "Successful" : "Incomplete");
      // The status screen shows whatever the mount reports.
      set("#st-ra", angle(f.Ra, true, false, settings.CoordFormat));
      set("#st-dec", angle(f.Dec, false, false, settings.CoordFormat));
      set("#st-alt", angle(f.Alt, false, false, settings.CoordFormat));
      set("#st-az", angle(f.Azm, false, true, settings.CoordFormat));
      {
        const pct = batteryPercent(f.BatteryVoltage); // an estimate from the voltage
        set("#st-battery", `${batteryText(f.BatteryLevel)}${f.BatteryVoltage ? `, ${f.BatteryVoltage.toFixed(1)} V` : ""}${pct != null ? ` (~${pct}%)` : ""}${batteryTemp(f.BatteryTemperature)}`);
      }
      set("#st-charger", chargerText(f.ChargerStatus));
      // What the mount itself was told at initialization, as ScopeStatusFragment.
      set("#st-lat", angle(f.Latitude, false, false, settings.CoordFormat));
      set("#st-lon", angle(f.Longitude, false, true, settings.CoordFormat));
      set("#st-date", originDate(f.Date));
      set("#st-time", f.Time ? clock(f.Time, settings.TimeFormat) : "-");
      set("#st-tz", f.TimeZone || "-");
      set("#st-enc-az", angle(f.Enc0, false, true, settings.CoordFormat));
      set("#st-enc-alt", angle(f.Enc1, false, false, settings.CoordFormat));
      lastMount = f;
      gotoSent = false; // the mount's own report replaces the go-to flag
      updateRunButtons();
      break;
    case "Focuser/GetStatus":
      set("#st-focus", f.Position == null ? "-" : `${Math.round(f.Position)}${f.IsMoveToOver === false ? ", moving" : ""}`);
      lastFocus = f;
      updateFocusUI();
      break;
    case "Focuser/GetPositionLimits":
      if (f.Min != null && f.Max != null) focusLimits = { Min: f.Min, Max: f.Max };
      updateFocusUI();
      break;
    case "Camera/GetFilter":
      lastFilter = f.Filter;
      updateViewerButtons();
      break;
    case "ImageServer/GetStretch":
      stretchKnown = f.Strength != null && f.Background != null;
      updateStretch();
      for (const [which, v] of [["Strength", f.Strength], ["Background", f.Background]]) {
        if (v == null) continue;
        $(`#stretch-${which.toLowerCase()}`).value = Math.round(v * 100);
        $(`#${which.toLowerCase()}-label`).textContent = stretchLabel(which, v);
      }
      break;
    case "System/UpdateStatus":
      lastUpdate = f;
      if (lastOrigin) showUpdate();
      break;
    case "TaskController/SafeToRemoveFlashDrive":
    case "ImageServer/GetAvailableStackedMasters":
      // The app's file manager reloads its folders on these.
      if ($("#files-dialog").open) loadFiles();
      break;
    case "TaskController/FileTransferStatus":
      lastTransfer = f;
      showLogsButton();
      updateFilesStatus();
      break;
    case "Camera/GetCaptureParameters":
      camera.params = `${f.Exposure ?? "?"} s, ISO ${f.ISO ?? "?"}${cameraExtra(f)}`;
      set("#st-camera", `${camera.params}${camera.mode}`);
      break;
    case "LiveStream/GetEnableManual":
      camera.mode = f.IsManual ? ", manual live view" : ", automatic";
      set("#st-camera", `${camera.params}${camera.mode}`);
      break;
    case "Environment/GetStatus":
      set("#st-env", envText(f));
      break;
    case "DewHeater/GetStatus":
      set("#st-dew", dewText(f));
      break;
    case "Autoguider/GetStatus":
      set("#st-guider", guiderText(f));
      break;
    case "Disk/GetStatus":
      set("#st-disk", freeSpace(f.FreeBytes));
      lastDisk = f;
      updateFilesStatus();
      break;
  }
}
Events.On("status", (ev) => applyStatus(ev.data.Key, ev.data.Fields));
Events.On("toast", (ev) => toast(ev.data.Level, ev.data.Message));

// ---------- frames and activity ----------

// show draws the frame Go picked, as the app's imageForScopeStatus: the
// stacked image while imaging, else the final image, else the live view.
export function show() {
  const f = frames[shownKind];
  $("#frame").hidden = !f;
  $("#frame-empty").hidden = !!f;
  if (!f) { $("#frame-info").textContent = ""; return; }
  const src = `/frame/${shownKind}?${f.Seq}`; // the query defeats the cache
  if ($("#frame").getAttribute("src") !== src) $("#frame").src = src;
  const i = f.Info;
  $("#frame-info").textContent = [i.ObjectName, i.StackDepth ? `${i.StackDepth} stacked` : "", i.ExposureTime ? `${i.ExposureTime} s` : ""]
    .filter(Boolean).join(" · ");
}
Events.On("frame", (ev) => {
  const { Kind, Seq, Info } = ev.data;
  frames[Kind] = { Seq, Info };
  if (Kind === "final") $("#save-final").disabled = false;
  if (Kind === shownKind) show();
  if (Kind === "stacked") updateRunButtons();
});
// The app's status line and progress bar, and which frame to show; while
// reframing the line says where the telescope will center.
export let activityText = "";
let forced = ""; // a capture sent, not yet reported (forcedTempStatusForUI)
let masterBusy = false; // a master image on its way: Reframe waits (updateUIForScopeStatus)
function applyActivity({ Text, Box, Progress, Image, Warning, Speed, Task, Target, Waiting, Init, Manual, MasterBusy, Forced }) {
  activityText = Text;
  if ((Forced ?? "") !== forced) { forced = Forced ?? ""; updateTargetButtons(); }
  masterBusy = !!MasterBusy;
  // The app's initialize button: shown only while it can act.
  $("#initialize").hidden = !Init;
  $("#initialize").textContent = Init ?? "";
  $("#initialize").disabled = !connected || Init === "Cancelling Initialization…";
  waiting = !!Waiting;
  $("#st-task").textContent = connected && Task ? Task : "-";
  $("#target-name").hidden = !Target;
  $("#target-name").textContent = Target ?? "";
  // The app's status button: its icons, as words.
  $("#warning").hidden = !Warning;
  $("#warning").textContent = Warning;
  $("#warning").classList.toggle("critical", Warning === "Battery Critical!");
  // While polar aligning the badge reopens the polar screen (originStatusButtonTapped).
  $("#warning").classList.toggle("clickable", Warning === "Polar Aligning");
  $("#manual-mode").hidden = !Manual;
  $("#manual-mode").textContent = Manual ?? "";
  $("#st-speed").textContent = connected ? `${(Speed ?? 0).toFixed(2)} (MB/s)` : "-";
  $("#activity").textContent = reframe.on ? reframeText(reframe.x, reframe.y) : Text;
  $("#final-box").hidden = !Box;
  $("#final-box").textContent = Box ?? "";
  // -1 hidden, -2 indeterminate (the app's spinning bar), else 0..1.
  $("#progress").hidden = Progress === -1 || Progress == null;
  if (Progress >= 0) $("#progress").value = Progress;
  else $("#progress").removeAttribute("value");
  shownKind = Image;
  $("#save-shown").disabled = !Image || Image === "final" || Image === "snapshot"; // those have their own saves
  show();
  updateViewerButtons();
}
Events.On("activity", (ev) => applyActivity(ev.data));

// The buttons over the image, as the app shows them: the filter and
// reframe buttons outside snapshot mode, Save/Delete for a snapshot.
function updateViewerButtons() {
  const snap = !!settings.Snapshot;
  const idleReady = connected && lastTask.State === "IDLE" && !!lastTask.IsReady && !masterBusy;
  $("#filter").textContent = filterLabel(lastFilter);
  // Hidden in snapshot mode and while imaging (updateImagingButtonsAnimated).
  const imaging = isImagingState(lastTask.State);
  $("#filter").hidden = snap || imaging;
  $("#filter").disabled = !connected || lastFilter == null;
  $("#reframe").hidden = snap || imaging;
  $("#reframe").disabled = !(reframe.on || idleReady);
  $("#reframe").textContent = reframe.on ? "End reframe" : "Reframe";
  $("#center-here").hidden = !reframe.on;
  $("#center-here").disabled = !idleReady;
  for (const id of ["#snapshot-save", "#snapshot-delete"]) $(id).hidden = shownKind !== "snapshot";
  if (reframe.on && (snap || !idleReady)) endReframe();
}

// S: changing the filter, as filterButtonTouched, then the offer to refocus.
$("#filter").onclick = async () => {
  let next;
  if (lastFilter === "Nebula") {
    if (!(await confirmBox("Install Clear Filter Now",
      'Please remove the nebula filter from Origin and reinstall the clear filter. Press "Filter Changed" when done.', "Filter Changed"))) return;
    next = "Clear";
  } else if (settings.Snapshot) {
    return showAlert("SnapShot Mode", "Cannot use Nebula Filter in Snapshot mode. Please remove it if it is installed.");
  } else {
    if (!(await confirmBox("Install Nebula Filter Now",
      'Please install the nebula filter into Origin. Press "Filter Changed" when done.', "Filter Changed"))) return;
    next = "Nebula";
  }
  if ((await act("SetFilter", next)) === false) return;
  if (await confirmBox("Autofocus Required After Filter Change",
    "Changing your filter typically requires your telescope to re-focus. Would you like to run autofocus now?", "Autofocus Now", "Don't Autofocus")) {
    act("AutoFocusAfterFilterChange");
  }
};

// Q: reframing. Click the image to put the crosshair where the telescope
// should center, then Center Here (RunCenterTarget).
function placeCrosshair() {
  const img = $("#frame"), box = img.parentElement.getBoundingClientRect(), r = img.getBoundingClientRect();
  $("#crosshair").style.left = `${r.left - box.left + reframe.x * r.width}px`;
  $("#crosshair").style.top = `${r.top - box.top + reframe.y * r.height}px`;
  $("#activity").textContent = reframeText(reframe.x, reframe.y);
}
function endReframe() {
  reframe.on = false;
  $("#crosshair").hidden = true;
  $("#frame").classList.remove("reframing");
  $("#activity").textContent = activityText;
  updateViewerButtons();
}
$("#reframe").onclick = () => {
  if (reframe.on) return endReframe();
  Object.assign(reframe, { on: true, x: 0.5, y: 0.5 });
  $("#crosshair").hidden = false;
  $("#frame").classList.add("reframing");
  placeCrosshair();
  updateViewerButtons();
};
$("#frame").onclick = (e) => {
  if (!reframe.on || zoom.dragged) return;
  Object.assign(reframe, offsetIn($("#frame").getBoundingClientRect(), e.clientX, e.clientY));
  placeCrosshair();
};

// Zooming the image, as the app's TouchImageView pinch and double-tap:
// the wheel zooms around the pointer, a drag pans, a double-click zooms in
// or back out.
const zoom = { z: 1, x: 0, y: 0, drag: null, dragged: false };
function applyZoom(v) {
  Object.assign(zoom, v);
  $("#frame").style.transform = zoom.z === 1 ? "" : `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.z})`;
  $("#frame").classList.toggle("zoomed", zoom.z > 1);
  if (reframe.on) placeCrosshair();
}
// at is the pointer's place from the image's untranslated top left.
function at(e) {
  const r = $("#frame").getBoundingClientRect();
  return [e.clientX - (r.left - zoom.x), e.clientY - (r.top - zoom.y)];
}
$(".frame-box").addEventListener("wheel", (e) => {
  if ($("#frame").hidden) return;
  e.preventDefault();
  applyZoom(zoomAt(zoom, ...at(e), Math.exp(-e.deltaY / 300)));
}, { passive: false });
$("#frame").ondblclick = (e) => applyZoom(zoom.z > 1 ? { z: 1, x: 0, y: 0 } : zoomAt(zoom, ...at(e), 2.5));
$("#frame").onpointerdown = (e) => {
  zoom.dragged = false;
  if (zoom.z === 1) return;
  zoom.drag = { x: e.clientX - zoom.x, y: e.clientY - zoom.y, sx: e.clientX, sy: e.clientY };
  $("#frame").setPointerCapture(e.pointerId);
};
$("#frame").onpointermove = (e) => {
  const d = zoom.drag;
  if (!d) return;
  // A few pixels of jitter is still a click (placing the crosshair).
  zoom.dragged ||= Math.hypot(e.clientX - d.sx, e.clientY - d.sy) > 4;
  if (zoom.dragged) applyZoom({ x: e.clientX - d.x, y: e.clientY - d.y });
};
$("#frame").onpointerup = $("#frame").onpointercancel = () => (zoom.drag = null);
$("#frame").ondragstart = (e) => e.preventDefault();
window.addEventListener("resize", () => reframe.on && placeCrosshair());
$("#center-here").onclick = async () => {
  const { x, y } = reframe;
  endReframe();
  await act("CenterHere", x, y);
};

// P: a snapshot is saved or deleted, as the app's SaveDeleteImageFragment.
$("#snapshot-save").onclick = async () => {
  // The app's alerts ("…to the Android Photo Gallery" there).
  if (await act("SaveSnapshot")) showAlert("Image Saved", "Image Saved to the Nightglass Gallery.");
};
$("#snapshot-delete").onclick = () => act("DiscardSnapshot");

// R: the Origin's stretch, sent when a slider is let go; only while
// imaging, once the Origin has reported it, as the app's Process page.
let stretchKnown = false;
function updateStretch() {
  for (const id of ["#stretch-strength", "#stretch-background"]) $(id).disabled = !stretchEnabled(connected, isImagingState(lastTask.State), stretchKnown);
}
for (const which of ["Strength", "Background"]) {
  const el = $(`#stretch-${which.toLowerCase()}`);
  el.oninput = () => ($(`#${which.toLowerCase()}-label`).textContent = stretchLabel(which, el.value / 100));
  el.onchange = () => act("SetStretch", which, el.value / 100);
}
// originDate is the Origin's "dd MM yyyy" in the chosen date order.
function originDate(d) {
  const m = /^(\d{2}) (\d{2}) (\d{4})$/.exec(d ?? "");
  return m ? calendar(m[3], m[2], m[1], settings.DateOrder) : d || "-";
}
// applyDisplay applies the night vision brightness.
function applyDisplay() {
  document.body.style.setProperty("--night-brightness", String((settings.NightBrightness || 65) / 100));
}
// Opening the status details asks again, as the app's status screen does.
$("#more-status").ontoggle = () => { if ($("#more-status").open && connected) act("RefreshStatus"); };
$("#save-shown").onclick = async () => {
  const p = await act("SaveShown");
  if (p) toast("info", `Saved ${p}`);
};
$("#save-final").onclick = async () => {
  const p = await act("SaveFinal");
  if (p) toast("info", `Saved ${p}`);
};

// ---------- telescope controls ----------

$("#initialize").onclick = () => act("Initialize");
$("#halt").onclick = () => act("HaltTasks");
$("#autofocus").onclick = () => act("AutoFocus");

// T: focus as the app's FocusOptionsFragment: fine and rough steps from the
// shown position within the Origin's limits, sent 500 ms after the last
// tap; only while the Origin is idle.
const clampFocus = (v) => Math.min(focusLimits.Max, Math.max(focusLimits.Min, v));
let focusTarget = null, focusTimer = 0;
function updateFocusUI() {
  const [fine, rough] = focusSteps(!!settings.Snapshot);
  for (const b of $$("[data-step]")) {
    const n = Number(b.dataset.step);
    b.textContent = `${n < 0 ? "−" : "+"}${stepSize(n, fine, rough)}`;
  }
  $("#focus-label").textContent = lastTask.State === "FOCUSING" ? "Focusing…" : "Focuser Position";
  const pos = focusTarget ?? (lastFocus.Position != null ? clampFocus(lastFocus.Position) : null);
  $("#focus-value").textContent = pos ?? "–";
  for (const el of $$(".focus-ctl")) el.disabled = !connected || lastTask.State !== "IDLE";
  // The value box stays usable and says why it cannot be set (focusValueBox).
  $("#focus-value").disabled = !connected;
}
// stepSize: the app's fine (1) and rough (2) steps, and a single step (3)
// as Origin Live View offers.
const stepSize = (n, fine, rough) => ({ 1: fine, 2: rough, 3: 1 })[Math.abs(n)];
function focusStep(n) {
  if (!connected || lastTask.State !== "IDLE") return;
  const [fine, rough] = focusSteps(!!settings.Snapshot);
  const base = focusTarget ?? clampFocus(lastFocus.Position ?? focusLimits.Min);
  focusTarget = clampFocus(base + Math.sign(n) * stepSize(n, fine, rough));
  updateFocusUI();
  clearTimeout(focusTimer);
  focusTimer = setTimeout(async () => {
    const t = focusTarget;
    focusTarget = null;
    await act("FocusTo", t);
    updateFocusUI();
  }, 500);
}
$$("[data-step]").forEach((b) => (b.onclick = () => focusStep(Number(b.dataset.step))));
$("#focus-value").onclick = () => {
  if (lastTask.State !== "IDLE") return showAlert("Origin Is Busy", "Focus cannot be set unless Origin is idle.");
  $("#focus-dialog-text").textContent = `If you believe your Origin to be far out of focus, you can manually set a new value here between [ ${focusLimits.Min} and ${focusLimits.Max} ].`;
  $("#focus-input").value = $("#focus-value").textContent;
  $("#focus-dialog").showModal();
};
$("#focus-set").onclick = async () => {
  const v = parseInt($("#focus-input").value, 10);
  if (Number.isNaN(v)) return;
  if (clampFocus(v) !== v) { $("#focus-input").value = clampFocus(v); return; } // as the app: show the nearest allowed value
  $("#focus-dialog").close();
  await act("FocusTo", v);
};
$("#finish-imaging").onclick = async () => (await act("NotMoving")) !== false && act("FinishImaging");
// Discarding asks first, as the app's showDiscardImagingConfirmation.
$("#cancel-imaging").onclick = async () => {
  if ((await act("NotMoving")) === false) return; // "Telescope in Motion" first, as the app
  if (["CAPTURING_MOSAIC", "RECAPTURE_MOSAIC_TILE"].includes(lastTask.State)) {
    // The app's two confirmations for ending a mosaic.
    if (await choice("End Mosaic Capture?", "", ["Continue Capture", "End Mosaic Capture"]) !== 1) return;
    if (await choice("Are you sure?", "This will immediately cancel your mosaic capture. Tiles captured so far remain saved on Origin.",
      ["Continue Capture", "End Capture Now"]) !== 1) return;
    act("CancelImaging");
    return;
  }
  if (!(await confirmBox("Are you sure?",
    "The current image capture data will be lost forever. Are you sure you want to discard it?",
    "Stop Imaging and Discard", "Continue Imaging"))) return;
  if ((await act("CancelImaging")) === false) return;
  delete frames.stacked; // the run is discarded
  show();
  updateRunButtons();
};

// Slew and stop go out one at a time, in order: on a quick tap the stop
// must not overtake the slew.
const motion = serial();
export async function slew(alt, azm) {
  const r = rateSpeed(Number($("#rate").value));
  slewing = true;
  await motion(() => act("Slew", alt * r, azm * r));
}
async function stopSlew() {
  if (!slewing) return;
  slewing = false;
  await motion(() => act("StopSlew"));
}
$$(".pad [data-alt]").forEach((b) => {
  b.onpointerdown = () => slew(Number(b.dataset.alt), Number(b.dataset.azm));
  b.onpointerup = b.onpointerleave = b.onpointercancel = stopSlew;
});
$("#slew-stop").onclick = () => { slewing = true; stopSlew(); };
// Losing focus mid-slew must not leave the mount running (spec §5).
window.addEventListener("blur", stopSlew);
document.addEventListener("visibilitychange", () => document.hidden && stopSlew());
// The focuser runs while a jog button is held, and stops as a slew does.
let jogging = false;
async function stopJog() {
  if (!jogging) return;
  jogging = false;
  await motion(() => act("FocusJog", 0));
}
$$("[data-jog]").forEach((b) => {
  b.onpointerdown = () => {
    if (!connected || lastTask.State !== "IDLE") return;
    jogging = true;
    motion(() => act("FocusJog", Number(b.dataset.jog)));
  };
  b.onpointerup = b.onpointerleave = b.onpointercancel = stopJog;
});
window.addEventListener("blur", stopJog);
document.addEventListener("visibilitychange", () => document.hidden && stopJog());

const ARROWS = { ArrowUp: [1, 0], ArrowDown: [-1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] };
export const typing = (e) => e.target.closest("input, select, textarea, dialog");
document.addEventListener("keydown", (e) => {
  if (typing(e)) return;
  if (ARROWS[e.key] && connected && lastTask.State === "IDLE") {
    e.preventDefault();
    if (!e.repeat) slew(...ARROWS[e.key]);
  } else if (e.key === "+" || e.key === "=" || e.key === "-" || e.key === "_") {
    // Focus from the keyboard: one step, or the rough step with Shift.
    const up = e.key === "+" || e.key === "=";
    focusStep((up ? 1 : -1) * (e.shiftKey ? 2 : 3));
  } else if (e.key === "n" || e.key === "N") {
    toggleNight();
  }
});
document.addEventListener("keyup", (e) => ARROWS[e.key] && stopSlew());

// ---------- targets ----------

export let rows = [];    // control.Row from Targets, with search keys
let pos = [];     // alt, az interleaved from Positions; empty without a location
export let shown = [];   // indices into rows, filtered and sorted
export let selected = -1;
let sort = { col: "alt", dir: -1 };

const alt = (i) => (pos.length ? pos[2 * i] : NaN);
// Go-to is refused below the lowest go-to altitude only, as in the app.
const canGoto = (i) => alt(i) >= (settings.SlewLimitMinDeg ?? 0);

async function loadCatalog() {
  rows = ((await act("Targets", $("#catalog").value)) || []).map(prepare);
  selected = -1;
  act("SetSelection", "", "");
  await refreshPositions();
  $("#rows").scrollTop = 0;
}
async function refreshPositions() {
  pos = (await act("Positions", $("#catalog").value)) || [];
  refilter();
}
function refilter() {
  shown = view(rows, pos, $("#search").value, $("#above").checked, sort);
  $("#count").textContent = `${shown.length} of ${rows.length}`;
  $$(".thead [data-sort]").forEach((h) => (h.className = h.dataset.sort === sort.col ? (sort.dir > 0 ? "asc" : "desc") : ""));
  renderRows();
}
// renderRows draws only the rows in sight: OpenNGC alone has 8,373.
function renderRows() {
  const box = $("#rows");
  $("#spacer").style.height = `${shown.length * ROW}px`;
  const first = Math.max(0, Math.floor(box.scrollTop / ROW) - 5);
  const last = Math.min(shown.length, first + Math.ceil(box.clientHeight / ROW) + 10);
  const frag = document.createDocumentFragment();
  for (let v = first; v < last; v++) {
    const i = shown[v], r = rows[i];
    const el = document.createElement("div");
    el.className = `trow${i === selected ? " sel" : ""}${canGoto(i) ? "" : " down"}`;
    const a = alt(i), z = pos.length ? pos[2 * i + 1] : NaN;
    for (const c of [r.Name, r.Description, r.Type, known(r.Mag)?.toFixed(1) ?? "–", known(r.Size)?.toFixed(1) ?? "–",
      r.Constellation, Number.isNaN(a) ? "–" : a.toFixed(1), Number.isNaN(z) ? "–" : z.toFixed(1)]) {
      const s = document.createElement("span");
      s.textContent = c;
      el.append(s);
    }
    el.title = `${r.Catalog}: ${r.Name}`;
    el.onclick = () => select(i);
    el.ondblclick = () => canGoto(i) && gotoRow(r);
    frag.append(el);
  }
  $("#window").style.transform = `translateY(${first * ROW}px)`;
  $("#window").replaceChildren(frag);
}
export function select(i) {
  selected = i;
  const r = rows[i];
  act("SetSelection", r.Catalog, r.Name); // the target label names it when pointed at
  $("#ra").value = dms(r.RA / 15, true).replace(/[h′″]/g, "");
  $("#dec").value = dms(r.Dec, false).replace(/[°′″+]/g, "").replace("−", "-");
  $("#selected").textContent = `${r.Name} — ${r.Description || r.Type} (${r.Catalog})`;
  updateTargetButtons();
  renderRows();
  showPane("cp-target"); // the chosen target's controls, as the web client's Target tab
}
// Go to needs a selected row above the lowest go-to altitude (typed
// coordinates are checked by Go). Image images where the telescope points
// and needs no target: Go names the run after what is there.
function updateTargetButtons() {
  const typed = !!($("#ra").value && $("#dec").value);
  const stop = gotoMode(lastMount, lastTask, gotoSent) === "stop"; // Stop needs no target
  $("#goto").disabled = !connected || (!stop && !(selected >= 0 ? canGoto(selected) : typed));
  // The capture button needs an idle Origin, initialized unless it takes a snapshot.
  $("#image").textContent = captureLabel(!!settings.Snapshot);
  $("#image").disabled = !connected || !!forced || lastTask.State !== "IDLE" || !(settings.Snapshot || lastTask.IsReady);
  // Create Mosaic Image needs a target and a known Origin state, as the app's.
  $("#mosaic").disabled = !connected || !lastTask.State || !(selected >= 0 || typed);
}
// gotoRow goes to a row; the Moon and planets go where they are now.
const gotoRow = (r) => (r.Catalog === "Solar System" ? act("GotoBody", r.Name) : act("GotoTarget", r.Catalog, r.Name));
export function target() {
  const t = targetFor(selected >= 0 ? rows[selected] : null, $("#ra").value, $("#dec").value);
  if (!t) toast("error", "Enter RA as h m s and Dec as ° ′ ″");
  return t;
}
let gotoSent = false;
$("#goto").onclick = async () => {
  if (gotoMode(lastMount, lastTask, gotoSent) === "stop") return act("StopSlew");
  const t = selected >= 0 ? null : target();
  if (selected < 0 && !t) return;
  if ((await (t ? act("Goto", t.RA, t.Dec) : gotoRow(rows[selected]))) === false) return;
  gotoSent = true; // as native SlewCelestronOrigin, until the next mount status
  updateRunButtons();
};
$("#image").onclick = async () => {
  showPane("mp-liveview");
  const r = selected >= 0 && !settings.Snapshot ? rows[selected] : null;
  const minutes = Number($("#duration").value) || 0; // a timed run, or until Finish
  if ((await act("Image", { Catalog: r?.Catalog ?? "", Name: r?.Name ?? "", DurationMin: minutes })) === false || settings.Snapshot) return;
  // A new run starts without stacked or final images, as in Go.
  delete frames.stacked;
  delete frames.final;
  $("#save-final").disabled = true;
  show();
  updateRunButtons();
};
for (const id of ["#ra", "#dec"]) {
  $(id).oninput = () => {
    if (selected >= 0) act("SetSelection", "", "");
    selected = -1;
    $("#selected").textContent = "Typed coordinates";
    updateTargetButtons();
    renderRows();
  };
}
$("#rows").onscroll = renderRows;
// The table draws only its visible rows: again when its size changes (a
// window resize, or its tab shown after being hidden at size 0).
new ResizeObserver(() => renderRows()).observe($("#rows"));
$("#search").oninput = () => { $("#rows").scrollTop = 0; refilter(); };
$("#above").onchange = refilter;
$("#catalog").onchange = loadCatalog;
$$(".thead [data-sort]").forEach((h) => (h.onclick = () => {
  const col = h.dataset.sort;
  sort = { col, dir: sort.col === col ? -sort.dir : col === "alt" ? -1 : 1 };
  refilter();
}));
setInterval(refreshPositions, 30_000);

// ---------- settings, night mode, about ----------

async function toggleNight() {
  settings.Night = !settings.Night;
  document.body.classList.toggle("night", settings.Night);
  applyDisplay();
  await act("SaveSettings", settings);
}
$("#night-toggle").onclick = toggleNight;

// The camera settings follow the app's camera menu: locked while imaging,
// ISO and exposure only in manual mode.
let isoKnown = false;
function updateCameraLocks() {
  const f = $("#settings-form");
  const l = cameraLocks({ connected, imaging: isImagingState(lastTask.State), manual: f.ManualImaging.checked, isoKnown });
  f.ManualImaging.disabled = l.mode;
  f.Snapshot.disabled = l.snapshot;
  f.ISO.disabled = l.iso;
  f.ExposureSec.disabled = f.SnapshotExposureSec.disabled = l.exposure;
}
$("#open-settings").onclick = async () => {
  const form = $("#settings-form");
  const iso = (await act("ISOChoices")) || { Values: [], Value: 0, Known: false };
  isoKnown = iso.Known;
  form.ISO.replaceChildren(...iso.Values.map((v) => new Option(String(v), String(v))));
  for (const el of form.elements) {
    if (!el.name) continue;
    if (el.type === "checkbox") el.checked = !!settings[el.name];
    else el.value = settings[el.name] ?? "";
  }
  form.ISO.value = String(iso.Value); // the saved ISO, else the camera's default
  if (!form.TimeZone.value) form.TimeZone.value = Intl.DateTimeFormat().resolvedOptions().timeZone;
  updateCameraLocks();
  updateDither();
  $("#settings-dialog").showModal();
};
// The dither sliders work only with dithering on (enableDitherSubOptions).
function updateDither() {
  const f = $("#settings-form");
  f.DitherDistance.disabled = f.DitherInterval.disabled = !f.Dither.checked;
  sliderLabels(f);
}
$("#settings-form").Dither.onchange = updateDither;
$("#settings-form").addEventListener("input", () => sliderLabels($("#settings-form")));
$("#settings-form").ManualImaging.onchange = updateCameraLocks;
$("#settings-form").onsubmit = async (e) => {
  e.preventDefault(); // close only once Go has accepted the values
  if (e.submitter?.value !== "save") { $("#settings-dialog").close(); return; }
  const next = { ...settings };
  for (const el of e.target.elements) {
    if (!el.name) continue;
    next[el.name] = el.type === "checkbox" ? el.checked : el.type === "number" || el.type === "range" || el.name === "ISO" ? Number(el.value) : el.value;
  }
  if ((await act("SaveSettings", next)) === false) return; // stay open to fix the value
  const before = settings;
  settings = (await act("Settings")) || next; // Go may have limited the exposures
  applyDisplay();
  const cameraChanged = ["ManualImaging", "Snapshot", "ExposureSec", "SnapshotExposureSec", "ISO"].some((k) => before[k] !== settings[k]);
  $("#settings-dialog").close();
  updateTargetButtons();
  updateViewerButtons();
  updateFocusUI();
  await refreshPositions();
  if (connected && cameraChanged && !isImagingState(lastTask.State)) act("ApplyCapture");
};
// System/CriticalError, as the app shows it: power cycle, and email the
// message to Celestron support when there is one.
let criticalMessage = "";
Events.On("critical", (ev) => {
  criticalMessage = ev.data.Message;
  $("#critical-email").hidden = !criticalMessage;
  $("#critical-dialog").showModal();
});
$("#critical-email").onclick = () => {
  // The app's subject and body (SkySafariActivityOrigin$11).
  const q = new URLSearchParams({ subject: "[Origin]: Critical Error", body: `I received the following critical error message from Origin:\n${criticalMessage}` });
  Browser.OpenURL(`mailto:origin@celestron.com?${q.toString().replaceAll("+", "%20")}`);
};

// Earlier runs whose final image is still on the Origin, offered as the app
// does: one image to download now, or several to pick from.
let orphans = [];
Events.On("orphans", (ev) => {
  orphans = ev.data.Uuids;
  const one = orphans.length === 1;
  $("#orphans-title").textContent = one ? "Image Available for Download" : "Images Available for Download";
  $("#orphans-text").textContent = one
    ? "There is one image available on Origin that you have not downloaded. Would you like to download it now?"
    : "There are several images available on Origin that you have not downloaded. Would you like to manage them?";
  // Several are managed in the file manager, as the app's.
  $("#orphans-download").textContent = one ? "Download Now" : "Manage Image Downloads";
  $("#orphans-forget").textContent = one ? "Don't Warn About This Image" : "Don't Warn Again";
  $("#orphans-dialog").showModal();
});
$("#orphans-download").onclick = () => {
  $("#orphans-dialog").close();
  if (orphans.length === 1) act("DownloadPending", orphans[0]);
  else $("#open-files").click();
};
$("#orphans-forget").onclick = () => {
  act("ForgetPending", orphans.length === 1 ? orphans[0] : "");
  $("#orphans-dialog").close();
};

Events.On("nebula", () => $("#nebula-dialog").showModal());
// A saved final image opens, as the app's ImageDetailFragment after a save.
Events.On("saved", (ev) => viewImage(ev.data.Name));
Events.On("alert", (ev) => showAlert(ev.data.Title, ev.data.Message));
$("#nebula-no").onclick = () => { act("SetFilter", "Clear"); $("#nebula-dialog").close(); };

// Tonight: the app's Sun & Moon card for the saved location and time zone.
$("#open-tonight").onclick = async () => {
  const c = await act("Tonight");
  if (!c) return;
  const r = tonightRows(c, settings.TimeFormat);
  const fill = (el, rows) => $(el).replaceChildren(...rows.flatMap(([k, v]) => {
    const dt = document.createElement("dt"), dd = document.createElement("dd");
    dt.textContent = k;
    dd.textContent = v;
    return [dt, dd];
  }));
  fill("#tonight-sun", r.sun);
  fill("#tonight-moon", r.moon);
  $("#tonight-moon-phase").textContent = c.MoonPhase;
  $("#tonight-moon-text").textContent = c.MoonText;
  $("#tonight-dialog").showModal();
};

$("#open-about").onclick = async () => {
  $("#about-text").textContent = (await act("About")) || "";
  $("#about-dialog").showModal();
};

// ---------- start ----------

export async function start() {
  settings = (await act("Settings")) || {};
  document.body.classList.toggle("night", !!settings.Night);
  applyDisplay();
  for (const w of (await act("Warnings")) || []) toast("warn", w);
  for (const z of Intl.supportedValuesOf?.("timeZone") ?? []) $("#zones").append(new Option(z));
  const cats = (await act("Catalogs")) || [];
  const total = cats.reduce((n, c) => n + c.Count, 0);
  $("#catalog").replaceChildren(new Option(`All catalogs (${total})`, ""), ...cats.map((c) => new Option(`${c.Name} (${c.Count})`, c.Name)));
  $("#catalog").value = settings.Catalog ?? "Messier";
  $("#rate").value = String(settings.SlewRate || 4);
  // The app remembers the rate (EquipmentSettings.setScopeMotionRate).
  $("#rate").onchange = () => { settings.SlewRate = Number($("#rate").value); act("SaveSettings", settings); };
  setConnected(false, "", "");
  applyActivity((await act("Activity")) || { Text: "No Connection", Progress: -1, Image: "" });
  await loadCatalog();
  act("Ready"); // Keep Screen On takes effect
  // connectOrigin: a typed address, else a search.
  const autoConnect = async () => { if (await locationOK()) act("ConnectOrigin"); };
  // The first three launches show Quick Start, and connect once it closes
  // (showCelestronOriginQuickStart, then doPostDownloadSkyBoxTasks).
  if (await act("QuickStartDue")) {
    showQuickStart();
    $("#quickstart-dialog").addEventListener("close", autoConnect, { once: true });
  } else {
    autoConnect();
    showAnnouncement(); // when Quick Start is not shown, as the app
  }
  act("CheckRemoteVersions"); // as the app when it starts: Celestron's version list
}
start();

// showAnnouncement is the app's announcement, when chosen in Settings: View
// opens its link.
async function showAnnouncement() {
  const n = await act("CheckAnnouncement");
  if (!n) return;
  if (!n.URL) return showAlert(n.Title, n.Body);
  if ((await choice(n.Title, n.Body, ["View", "Cancel"])) === 0) Browser.OpenURL(n.URL);
}

// Modules read the shared state through live imports; they write it here.
export function setSettings(s) { settings = s; }
export function setLastOrigin(o) { lastOrigin = o; }
