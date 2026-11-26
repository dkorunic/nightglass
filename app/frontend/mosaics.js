// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Mosaics (the app's mosaic screens).
import { Events } from "/wails/runtime.js";
import { $, act, choice, confirmBox } from "./ui.js";
import { connected, lastTask, rows, selected, setSettings, settings, target } from "./app.js";
import { viewImage } from "./gallery.js";
import { composeButton, isImagingState, mosaicAction, mosaicDetail, mosaicSize, tileKind, tileStatus } from "./targets.js";

// Create Mosaic Image: capture settings, area and confirmation in one
// dialog (the app's three screens), then Begin Mosaic Capture.
let mosaicPlan = null, mosaicTarget = null, startWin = null;
const hhmm = (sec) => `${String(Math.floor(sec / 3600)).padStart(2, "0")}:${String(Math.floor(sec / 60) % 60).padStart(2, "0")}`;
async function updateMosaicGrid() {
  const f = $("#mosaic-form");
  const w = Number(f.width.value) || 0, h = Number(f.height.value) || 0;
  $("#mosaic-width-label").textContent = `Width: ${mosaicSize(w)}`;
  $("#mosaic-height-label").textContent = `Height: ${mosaicSize(h)}`;
  $("#mosaic-rotation-label").textContent = `Rotation: ${Number(f.rotation.value).toFixed(0)}°`;
  const g = (await act("GridFor", w, h)) || { Cols: 0, Rows: 0, Tiles: 0 };
  $("#mosaic-grid").textContent = `${g.Cols} × ${g.Rows} = ${g.Tiles} tile(s)`;
  return g;
}
$("#mosaic").onclick = async () => {
  const r = selected >= 0 ? rows[selected] : null;
  const t = r ? { Catalog: r.Catalog, Name: r.Name, RA: r.RA, Dec: r.Dec } : (() => {
    const x = target();
    return x && { Catalog: "", Name: "", RA: x.RA, Dec: x.Dec };
  })();
  if (!t) return;
  mosaicTarget = t;
  mosaicPlan = await act("MosaicPlanFor", { Catalog: t.Catalog, Name: t.Name });
  if (!mosaicPlan) return;
  startWin = await act("MosaicStartTimes", t.RA, t.Dec);
  $("#mosaic-form").startat.value = "";
  const f = $("#mosaic-form");
  $("#mosaic-title").textContent = t.Name ? `${t.Name} Mosaic` : "Mosaic";
  $("#mosaic-auto-duration").textContent = hhmm(mosaicPlan.DurationSec);
  f.durh.value = Math.floor(mosaicPlan.DurationSec / 3600);
  f.durm.value = Math.floor(mosaicPlan.DurationSec / 60) % 60;
  f.exp.replaceChildren(...mosaicPlan.ExposureChoices.map((v) => new Option(`${v}`, `${v}`)));
  f.exp.value = String(Math.min(30, mosaicPlan.ExposureSec));
  f.iso.replaceChildren(...mosaicPlan.ISOChoices.map((v) => new Option(`${v}`, `${v}`)));
  f.iso.value = String(mosaicPlan.ISOChoices.includes(mosaicPlan.ISO) ? mosaicPlan.ISO : 200);
  f.width.value = mosaicPlan.WidthArcmin.toFixed(1);
  f.height.value = mosaicPlan.HeightArcmin.toFixed(1);
  f.rotation.value = Math.round(mosaicPlan.RotationDeg);
  f.powerdown.checked = f.autofocus.checked = false;
  await updateMosaicGrid();
  $("#mosaic-dialog").showModal();
};
for (const n of ["width", "height", "rotation"]) $("#mosaic-form")[n].oninput = updateMosaicGrid;

// The manual start time as the app's image settings: chosen, it starts at
// the earliest automatic start; checked a second after each change, and
// outside tonight's window it is reset (validateManualStartTimeAndSet).
const localTime = (ms) => {
  const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60000);
  return d.toISOString().slice(0, 16); // datetime-local wants local time
};
for (const r of $("#mosaic-form").start) {
  r.onchange = () => {
    const f = $("#mosaic-form");
    if (f.start.value === "manual" && !f.startat.value && startWin) f.startat.value = localTime(startWin.MinMs);
  };
}
async function validStart() {
  const f = $("#mosaic-form");
  if (f.start.value !== "manual" || !f.startat.value || !startWin?.DawnMs) return true;
  const inWindow = (ms) => ms >= startWin.MinMs - 60000 && ms <= startWin.DawnMs + 60000;
  if (inWindow(new Date(f.startat.value).getTime())) return true;
  const now = inWindow(Date.now());
  await choice("Invalid Start Time", "The time you have chosen is outside of the visibility window for this object. The start time will be reset.",
    [now ? "Set To Now" : "Set To Minimum"]);
  f.startat.value = localTime(now ? Date.now() : startWin.MinMs);
  return false;
}
let startCheck = 0;
$("#mosaic-form").startat.oninput = () => { clearTimeout(startCheck); startCheck = setTimeout(validStart, 1000); };
$("#mosaic-begin").onclick = async () => {
  clearTimeout(startCheck);
  if (!(await validStart())) return;
  const f = $("#mosaic-form"), g = await updateMosaicGrid();
  if (g.Tiles >= mosaicPlan.MaxTiles && await choice("Large Mosaic",
    "Mosaic may be too large to automatically compose on Origin. If you get a failure message during mosaic composition, you will need to download the individual tiles and manually compose them on another computer.",
    ["Capture Anyway", "Cancel"]) !== 0) return;
  const manualCam = f.camera.value === "manual";
  const duration = f.duration.value === "custom" ? (Number(f.durh.value) || 0) * 3600 + (Number(f.durm.value) || 0) * 60 : mosaicPlan.DurationSec;
  const start = f.start.value === "manual" && f.startat.value ? new Date(f.startat.value).toISOString() : "0001-01-01T00:00:00Z";
  const ok = await act("StartMosaic", {
    ...mosaicTarget, WidthArcmin: Number(f.width.value), HeightArcmin: Number(f.height.value), RotationDeg: Number(f.rotation.value),
    DurationSec: duration, Manual: manualCam, ExposureSec: manualCam ? Number(f.exp.value) : mosaicPlan.ExposureSec,
    ISO: manualCam ? Number(f.iso.value) : mosaicPlan.ISO, StartAt: start, PowerDown: f.powerdown.checked, AutoFocus: f.autofocus.checked,
  });
  if (ok === false) return;
  setSettings((await act("Settings")) || settings); // snapshot and manual live view are left
  $("#mosaic-dialog").close();
};

// The mosaic list and a mosaic's tiles (ImagingSchedulesFragment, MosaicCaptureFragment).
let listTimer = 0;
async function loadMosaics() {
  const list = (await act("Mosaics")) || [];
  // Polled every 5 s while any mosaic is unfinished, as ImagingSchedulesFragment.
  clearTimeout(listTimer);
  if (list.some((m) => ["NOT_STARTED", "IN_PROGRESS"].includes(m.Meta?.Status))) {
    listTimer = setTimeout(() => $("#mosaics-dialog").open && loadMosaics(), 5000);
  }
  $("#mosaics-empty").hidden = list.length > 0;
  $("#mosaics-list").replaceChildren(...list.map((m) => {
    const li = document.createElement("li");
    const title = document.createElement("b");
    title.textContent = m.Title;
    const detail = document.createElement("span");
    detail.textContent = ` — ${mosaicDetail(m.Meta, settings.DateOrder, settings.TimeFormat)} `;
    const del = document.createElement("button");
    del.textContent = "Delete";
    // Not for a mosaic being captured (canDeleteItem: isRunningSchedule,
    // isMosaicActivelyCapturing).
    const runningList = lastTask.ImagingInfo?.MosaicMetadata?.ListUuid ?? lastTask.ImagingInfo?.ListUuid;
    del.hidden = m.Meta?.Status === "IN_PROGRESS" || (isImagingState(lastTask.State) && runningList === m.Uuid);
    del.onclick = async (e) => {
      e.stopPropagation();
      if (!(await confirmBox("Delete Imaging Schedule", "Are you sure you want to delete this Imaging Schedule?", "Delete"))) return;
      if ((await act("DeleteMosaic", m.Uuid)) !== false) loadMosaics();
    };
    li.append(title, detail, del);
    li.onclick = () => openMosaic(m);
    return li;
  }));
}
$("#open-mosaics").onclick = () => { loadMosaics(); $("#mosaics-dialog").showModal(); };

let mosaic = null, mosaicView = null, tilesAt = 0, composePending = 0;
const thumbsAsked = new Set(), thumbs = new Set();
async function openMosaic(m) {
  mosaic = m;
  $("#mosaics-dialog").close();
  $("#mosaic-capture-dialog").showModal();
  await refreshMosaic();
}
// mosaicTaskStatus asks for the tiles again on each task status while the
// capture screen is open, at most every 2 s (TILE_STATUS_QUERY_THROTTLE_MS).
export function mosaicTaskStatus() {
  if ($("#mosaic-capture-dialog").open && Date.now() - tilesAt >= 2000) refreshMosaic();
}
async function refreshMosaic() {
  if (!mosaic || !connected) return renderMosaicCapture();
  tilesAt = Date.now();
  const v = await act("MosaicTiles", mosaic.Uuid);
  if (v && v.Meta.ListUuid === mosaic.Uuid) mosaicView = v;
  // Thumbnails of the captured tiles, as the app asks for them.
  for (const t of mosaicView?.Tiles ?? []) {
    if (t.StackingStatus === "COMPLETED" && !thumbsAsked.has(t.Uuid)) { thumbsAsked.add(t.Uuid); act("MosaicThumbnail", t.Uuid, 0.25); }
  }
  renderMosaicCapture();
}
Events.On("thumb", (ev) => {
  // One that failed is asked for again on the next refresh (MosaicCaptureFragment$3$1).
  if (ev.data.Failed) thumbsAsked.delete(ev.data.Uuid);
  else thumbs.add(ev.data.Uuid);
  if ($("#mosaic-capture-dialog").open) renderMosaicCapture();
});
const showSaved = (name) => viewImage(name); // the image's own detail screen, as the app's
export function renderMosaicCapture() {
  if (!mosaic) return;
  const meta = mosaicView?.Meta;
  const tiles = mosaicView?.Tiles ?? [];
  // Recapturing: the active tile's number when one is busy (activeTileNumber).
  const active = tiles.findIndex((t) => tileKind(t.StackingStatus) === "busy");
  const activeN = active < 0 ? 0 : tiles[active].TileId > 0 ? tiles[active].TileId : active + 1;
  $("#mc-title").textContent = lastTask.State === "RECAPTURE_MOSAIC_TILE" ? (activeN ? `Recapturing Tile ${activeN}` : "Recapturing Mosaic Tile")
    : meta?.TilesTotal ? `Captured ${meta.TilesCompleted} of ${meta.TilesTotal} Tiles` : "Mosaic Capture";
  const idle = connected && lastTask.State === "IDLE";
  $("#mc-tiles").replaceChildren(...tiles.map((t, i) => {
    const n = t.TileId > 0 ? t.TileId : i + 1;
    const div = document.createElement("div");
    div.className = `mtile ${tileKind(t.StackingStatus)}`;
    // The thumbnail, or a placeholder until there is one.
    const img = document.createElement(thumbs.has(t.Uuid) ? "img" : "div");
    if (thumbs.has(t.Uuid)) img.src = `/frame/thumb/${encodeURIComponent(t.Uuid)}`;
    else { img.className = "mph"; img.textContent = "No Preview"; }
    const h = document.createElement("div");
    h.textContent = `Tile ${n} of ${tiles.length}`;
    const st = document.createElement("div");
    st.className = "mstat";
    st.textContent = tileStatus(t.StackingStatus) + (t.StackDepth > 0 && t.StackingStatus !== "FAILED" ? ` · Stack Depth: ${t.StackDepth}` : "")
      + (t.StackingStatus === "FAILED" && t.FailureMessage ? ` · ${t.FailureMessage}` : "");
    div.append(img, h, st);
    const btn = (label, fn, on = true) => { const b = document.createElement("button"); b.textContent = label; b.onclick = fn; b.disabled = !on; div.append(b); };
    if (t.StackingStatus === "COMPLETED" || t.StackingStatus === "FAILED") {
      btn("Re-Capture", async () => {
        if (await confirmBox("Re-Capture Tile?", `This will re-capture tile ${n} of ${tiles.length}. The telescope will slew to the tile position and re-image it.`, "Re-Capture")) {
          if ((await act("RecaptureTile", mosaic.Uuid, t.Uuid, t.TileRow ?? -1, t.TileCol ?? -1)) !== false) {
            setSettings((await act("Settings")) || settings); // Go left snapshot and manual live view
            refreshMosaic();
          }
        }
      }, idle);
    }
    if (t.Image) btn("Show Image", () => showSaved(t.Image)); // downloaded this session
    else if (t.StackingStatus === "COMPLETED" || ((t.StackingStatus === "FAILED" || !t.StackingStatus) && t.StackDepth > 0)) {
      btn("Download", () => act("DownloadTile", t.Uuid));
    }
    return div;
  }));
  const a = mosaicAction(meta, lastTask, idle);
  $("#mc-action").hidden = !a;
  if (a) { $("#mc-action").textContent = a.text; $("#mc-action").disabled = !a.enabled; $("#mc-action").onclick = () => (a.act === "cancel" ? act("CancelImaging") : act("ResumeMosaic", mosaic.Uuid).then(async (ok) => {
    if (ok !== false) setSettings((await act("Settings")) || settings); // Go left snapshot and manual live view
    refreshMosaic();
  })); }
  const completed = tiles.filter((t) => t.StackingStatus === "COMPLETED").length;
  // The composition's preview (showComposedPreviewImage) and Show Image.
  const comp = mosaicView?.Composed;
  $("#mc-preview").hidden = !(comp?.Uuid && thumbs.has(comp.Uuid));
  if (!$("#mc-preview").hidden) $("#mc-preview").src = `/frame/thumb/${encodeURIComponent(comp.Uuid)}`;
  const c = composeButton(lastTask, mosaic.Uuid, Date.now() - composePending < 15000, completed, idle, !!comp?.Image);
  $("#mc-compose").textContent = c.text;
  $("#mc-compose").disabled = !c.enabled;
  $("#mc-spinner").hidden = !c.spin;
  $("#mc-compose").onclick = async () => {
    if (c.act === "halt") return act("HaltTasks");
    if (c.act === "show") return showSaved(comp.Image);
    if (completed < tiles.length && await choice("Compose Partial Mosaic?",
      `Only ${completed} of ${tiles.length} tiles captured successfully. The mosaic will be composed from the captured tiles, leaving the rest of the image blank.`,
      ["Compose", "Cancel"]) !== 0) return;
    composePending = Date.now();
    if ((await act("ComposeMosaic", mosaic.Uuid)) === false) composePending = 0;
    renderMosaicCapture();
  };
}

// A mosaic that finished while away is offered on connect
// (warnAboutUndownloadedImageList); Go asks one question at a time.
Events.On("listready", async (ev) => {
  const { Uuid, Title } = ev.data;
  const i = await choice("Image List Available for Download",
    "There is an imaging list on Origin that you have not downloaded. Would you like to download it now?",
    ["Download Images…", "Don't Warn About This List", "Cancel"]);
  await act("AnswerList", Uuid, i === 0 || i === 1);
  if (i === 0) openMosaic({ Uuid, Title });
});
