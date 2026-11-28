// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Camera tab of Celestron's web client: the camera's figures, the live
// view (on or off, automatic or manual with its exposure and ISO), binning,
// bit depth, white balance, the filter, a region for video, a single
// capture, stacking and video. The live view's manual exposure and ISO are
// Nightglass's Snapshot settings, so the app's rules for them still hold.
import { Events } from "/wails/runtime.js";
import { $, $$, act, choice, showAlert, showPane } from "./ui.js";
import { connected, isIdle, lastFilter, lastTask, rows, selected, setSettings, settings } from "./app.js";
import { exposureAt, exposurePos, exposureText, isImagingState, isoAt, isoPos } from "./targets.js";

let isos = [100, 200, 500, 1000, 2000, 5000, 10000, 20000];
let reported = {}; // Camera/GetCaptureParameters
const manual = () => !!(settings.ManualImaging && settings.Snapshot);

// save stores changed camera settings and applies them as the settings
// dialog does; Go may limit the exposure, so its copy is taken back.
async function save(apply = true) {
  if ((await act("SaveSettings", settings)) === false) return;
  setSettings((await act("Settings")) || settings);
  if (apply && connected && !isImagingState(lastTask.State)) await act("ApplyCapture");
  render();
}

function render() {
  const idle = connected && isIdle();
  const man = manual();
  for (const r of $$("input[name='cam-mode']")) r.checked = r.value === (man ? "manual" : "auto");
  const exp = man ? settings.SnapshotExposureSec : reported.Exposure, iso = man ? settings.ISO : reported.ISO;
  for (const [el, val, text] of [["#cam-exp", exp != null ? exposurePos(exp) : 0, exp != null ? exposureText(exp) : "–"],
    ["#cam-iso", iso ? isoPos(iso, isos) : 0, iso ?? "–"]]) {
    if (document.activeElement !== $(el)) $(el).value = val;
    if (document.activeElement !== $(`${el}-val`)) $(`${el}-val`).value = text;
    $(el).disabled = $(`${el}-val`).disabled = !idle || !man;
  }
  for (const el of $$(".cam-idle")) el.disabled = !idle;
  $("#roi-select").disabled ||= Number($("#cam-bin").value) !== 1; // a region at full resolution only
  $("#cam-calibrate").checked = settings.Calibrate !== false;
  const stacking = ["IMAGING_OBJECT", "IMAGING_LIST"].includes(lastTask.State);
  $("#start-stacking").textContent = stacking ? "Stop Stacking" : "Start Stacking…";
  $("#start-stacking").disabled = !connected || !(idle || stacking);
  $("#capture-single").disabled = !idle;
  const video = lastTask.State === "VIDEO_CAPTURE";
  $("#capture-video").textContent = video ? "Stop Video" : "Capture Video";
  $("#capture-video").disabled = !connected || !(video || (idle && exp != null && exp < 0.1));
}

function onStatus(key, f) {
  switch (key) {
    case "Camera/GetCameraInfo":
      $("#cam-model").textContent = f.CameraModel || "—";
      $("#cam-serial").textContent = f.CameraID ? f.CameraID.replace(/^[^-]*-/, "") : "—";
      $("#cam-firmware").textContent = f.FirmwareVersion || "—";
      $("#cam-sdk").textContent = f.SdkVersion || "—";
      $("#cam-size").textContent = f.MaxImageWidth ? `${f.MaxImageWidth} x ${f.MaxImageHeight}` : "—";
      $("#cam-pixel").textContent = f.PixelSize ? `${f.PixelSize.toFixed(2)} µm` : "—";
      $("#cam-wb").hidden = f.IsColor === false;
      for (const el of $$(".cool-row")) el.hidden = !f.HasCooling; // a cooled camera's cooler power
      for (const o of $$("#cam-depth .rgb")) o.hidden = f.IsColor === false;
      $("#cam-bin").replaceChildren(...Array.from({ length: Math.max(f.MaxBinning || 1, 1) }, (_, i) => new Option(String(i + 1))));
      if (reported.Binning) $("#cam-bin").value = String(reported.Binning);
      act("SupportedISOs").then((v) => { if (v?.length) isos = v; render(); });
      break;
    case "Camera/GetCaptureParameters":
      reported = f;
      if (f.Binning != null) $("#cam-bin").value = String(f.Binning);
      if (f.BitDepth != null) $("#cam-depth").value = String(f.BitDepth);
      for (const el of $$("#cam-wb input")) {
        const v = f[el.dataset.wb];
        if (v != null && document.activeElement !== el) { el.value = v; el.nextElementSibling.textContent = v; }
      }
      break;
    case "LiveStream/GetDisableLiveStream":
      $("#cam-live").checked = !f.IsDisabled;
      break;
    case "Camera/GetFilter":
      if (f.Filter) $("#cam-filter").value = f.Filter;
      break;
  }
  render();
}
// After app.js has taken each report (its listener runs after this one).
Events.On("status", (ev) => setTimeout(() => onStatus(ev.data.Key, ev.data.Fields)));
Events.On("conn", () => setTimeout(render));

$("#cam-live").onchange = () => act("SetLiveStream", $("#cam-live").checked);
for (const r of $$("input[name='cam-mode']")) {
  r.onchange = () => {
    // Manual live view is the app's Snapshot mode, which needs manual imaging.
    if (r.value === "manual") Object.assign(settings, { ManualImaging: true, Snapshot: true });
    else settings.Snapshot = false;
    save();
  };
}
// The sliders show their value as they move and set it when let go; the
// text fields set theirs when committed.
$("#cam-exp").oninput = () => ($("#cam-exp-val").value = exposureText(exposureAt($("#cam-exp").value)));
$("#cam-exp").onchange = () => { settings.SnapshotExposureSec = Number(exposureText(exposureAt($("#cam-exp").value))); save(); };
$("#cam-exp-val").onchange = () => {
  const v = Number($("#cam-exp-val").value);
  if (!(v >= 0.001 && v <= 1000)) { showAlert("Exposure", "Please enter an exposure from 0.001 to 1000 seconds."); return render(); }
  settings.SnapshotExposureSec = v;
  save();
};
$("#cam-iso").oninput = () => ($("#cam-iso-val").value = isoAt($("#cam-iso").value, isos));
$("#cam-iso").onchange = () => { settings.ISO = isoAt($("#cam-iso").value, isos); save(); };
$("#cam-iso-val").onchange = () => { settings.ISO = isoAt(isoPos(Number($("#cam-iso-val").value) || isos[0], isos), isos); save(); };
$("#cam-bin").onchange = () => {
  if ($("#cam-bin").value !== "1") clearRoi();
  act("SetCameraParameter", "Binning", Number($("#cam-bin").value));
};
$("#cam-depth").onchange = () => act("SetCameraParameter", "BitDepth", Number($("#cam-depth").value));
for (const el of $$("#cam-wb input")) {
  el.oninput = () => (el.nextElementSibling.textContent = el.value);
  el.onchange = () => act("SetCameraParameter", el.dataset.wb, Number(el.value));
}
// The filter is the live view's Clear/Nebula switch, with the app's checks.
$("#cam-filter").onchange = () => {
  if ($("#cam-filter").value !== lastFilter) $("#filter").click();
  setTimeout(() => lastFilter && ($("#cam-filter").value = lastFilter), 1500);
};
$("#cam-calibrate").onchange = () => { settings.Calibrate = $("#cam-calibrate").checked; save(false); };
$("#capture-single").onclick = () => { showPane("mp-liveview"); act("CaptureSingle"); };

// ---- the region of interest, drawn on the live view (for video) ----
const roi = { selecting: false, start: null, rect: null };
function framePoint(e) {
  const img = $("#frame"), r = img.getBoundingClientRect();
  return [Math.round(((e.clientX - r.left) / r.width) * img.naturalWidth), Math.round(((e.clientY - r.top) / r.height) * img.naturalHeight)];
}
function placeRoi() {
  const box = $("#roi-box"), img = $("#frame"), q = roi.rect;
  box.hidden = !q || img.hidden || !img.naturalWidth;
  if (box.hidden) return;
  const r = img.getBoundingClientRect(), b = $(".frame-box").getBoundingClientRect();
  const k = r.width / img.naturalWidth;
  Object.assign(box.style, { left: `${r.left - b.left + q.x * k}px`, top: `${r.top - b.top + q.y * k}px`, width: `${q.w * k}px`, height: `${q.h * k}px` });
}
function setRoi(q) {
  roi.rect = q;
  $("#roi-start").textContent = q ? `(${q.x}, ${q.y})` : "None";
  $("#roi-size").textContent = q ? `${q.w} × ${q.h}` : "None";
  placeRoi();
}
function clearRoi() { roi.selecting = false; $(".frame-box").classList.remove("roi-mode"); setRoi(null); }
$("#roi-select").onclick = () => {
  showPane("mp-liveview");
  roi.selecting = true;
  $(".frame-box").classList.add("roi-mode");
};
$("#roi-clear").onclick = clearRoi;
// Before the live view's own pan and click handlers, while selecting.
$(".frame-box").addEventListener("pointerdown", (e) => {
  if (!roi.selecting || e.target !== $("#frame")) return;
  e.stopPropagation();
  e.preventDefault();
  roi.start = framePoint(e);
  $("#frame").setPointerCapture(e.pointerId);
}, true);
$(".frame-box").addEventListener("pointermove", (e) => {
  if (!roi.selecting || !roi.start) return;
  e.stopPropagation();
  const img = $("#frame"), [x, y] = framePoint(e), [sx, sy] = roi.start;
  const cx = (v, hi) => Math.min(Math.max(v, 0), hi);
  const x0 = cx(Math.min(x, sx), img.naturalWidth), y0 = cx(Math.min(y, sy), img.naturalHeight);
  setRoi({ x: x0, y: y0, w: cx(Math.max(x, sx), img.naturalWidth) - x0, h: cx(Math.max(y, sy), img.naturalHeight) - y0 });
}, true);
$(".frame-box").addEventListener("pointerup", (e) => {
  if (!roi.selecting || !roi.start) return;
  e.stopPropagation();
  roi.start = null;
  roi.selecting = false;
  $(".frame-box").classList.remove("roi-mode");
  if (roi.rect && (roi.rect.w < 4 || roi.rect.h < 4)) setRoi(null); // a click, not a region
}, true);
$(".frame-box").addEventListener("click", (e) => roi.rect && roi.selecting && e.stopPropagation(), true);
// The box follows the image as it zooms, pans and resizes.
new MutationObserver(placeRoi).observe($("#frame"), { attributes: true, attributeFilter: ["style", "src", "hidden"] });
new ResizeObserver(placeRoi).observe($(".frame-box"));

// ---- video ----
$("#capture-video").onclick = () => {
  if (lastTask.State === "VIDEO_CAPTURE") return act("HaltTasks");
  const q = roi.rect;
  act("CaptureVideo", {
    Name: selected >= 0 ? rows[selected].Name : "", Container: $("#video-container").value, BitDepth: Number($("#video-depth").value),
    Frames: $("#video-max").checked ? Math.min(Math.max(Number($("#video-frames").value) || 1000, 1), 9999) : 0,
    Roi: q ? { X: q.x, Y: q.y, W: q.w, H: q.h } : { X: 0, Y: 0, W: 0, H: 0 },
  });
};

// ---- stacking: the web client's Start Stacking dialog over the imaging
// settings (automatic or manual exposure, raw images, stacking failures) ----
const stack = $("#stack-form").elements;
function stackLabels() {
  $("#stack-exp-val").textContent = `${exposureText(exposureAt($("#stack-exp").value))} s`;
  $("#stack-iso-val").textContent = isoAt($("#stack-iso").value, isos);
  const man = stack.mode.value === "manual";
  $("#stack-exp").disabled = $("#stack-iso").disabled = !man;
}
$("#stack-form").addEventListener("input", stackLabels);
$("#start-stacking").onclick = async () => {
  if (["IMAGING_OBJECT", "IMAGING_LIST"].includes(lastTask.State)) {
    const c = await choice("Stop Stacking", "The stacking session is in progress. What would you like to do with the stacked image?",
      ["Cancel", "Discard Image", "Save Image"]);
    if (c === 1) act("CancelImaging");
    if (c === 2) { act("FinishImaging"); showPane("mp-liveview"); }
    return;
  }
  stack.mode.value = settings.ManualImaging ? "manual" : "auto";
  $("#stack-exp").value = exposurePos(settings.ExposureSec || 10);
  $("#stack-iso").value = isoPos(settings.ISO || 1000, isos);
  stack.saveraw.checked = settings.SaveRaw !== false;
  stack.ignorefail.checked = !settings.StopOnPlateSolveFail;
  stackLabels();
  $("#stack-dialog").returnValue = "";
  $("#stack-dialog").showModal();
};
$("#stack-dialog").addEventListener("close", async () => {
  if ($("#stack-dialog").returnValue !== "start") return;
  // Stacking leaves the manual live view, as the app's imaging does.
  Object.assign(settings, {
    ManualImaging: stack.mode.value === "manual", Snapshot: false, SaveRaw: stack.saveraw.checked,
    StopOnPlateSolveFail: !stack.ignorefail.checked,
    ExposureSec: Number(exposureText(exposureAt($("#stack-exp").value))), ISO: isoAt($("#stack-iso").value, isos),
  });
  await save();
  $("#image").click();
});
