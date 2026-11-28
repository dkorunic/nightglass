// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The gallery and the image editor (PhotoGalleryFragment, ImageAdjustmentsFragment).
import { $, $$, act, choice, confirmBox, openInTab, toast } from "./ui.js";
import { CROP_RATIOS, adjustPixels, calendar, clock, cropRect, dms, latLon, offsetIn, prepare, shareExposure, sliderEdit, view } from "./targets.js";
import { settings } from "./app.js";

// The app's PhotoGalleryFragment: All, Favorites and Recent (the last 12
// hours), and per image
// Favorite, Share, View, Edit and Delete; Select picks several to share or
// delete. Share opens the file manager at the image.
const gallery = { list: [], tab: "all", select: false, chosen: new Set() };
const TABS = ["all", "favorites", "recent"];
const savedURL = (name) => `/frame/saved/${encodeURIComponent(name)}?${Date.now()}`;
async function loadGallery() {
  gallery.list = (await act("Gallery")) || [];
  renderGallery();
}
function renderGallery() {
  const since = Date.now() / 1000 - 12 * 3600;
  const shown = gallery.list.filter((g) => gallery.tab === "all" || (gallery.tab === "favorites" ? g.Favorite : (g.Record?.TakenUnix ?? 0) > since));
  for (const t of TABS) $(`#gallery-${t}`).classList.toggle("on", gallery.tab === t);
  $("#gallery-empty").hidden = shown.length > 0;
  const n = gallery.chosen.size;
  // The app's selection label: "Select" until something is chosen.
  $("#gallery-count").textContent = gallery.select ? (n === 0 ? "Select" : n === 1 ? "1 Image" : `${n} Images`) : "";
  $("#gallery-select").textContent = gallery.select ? "Cancel Selection" : "Select";
  $("#gallery-share-sel").hidden = $("#gallery-delete-sel").hidden = !gallery.select;
  $("#gallery-share-sel").disabled = $("#gallery-delete-sel").disabled = n === 0;
  $("#gallery-grid").replaceChildren(...shown.map((g) => {
    const card = document.createElement("div");
    card.className = `gcard${gallery.chosen.has(g.Name) ? " sel" : ""}`;
    const img = document.createElement("img");
    img.src = savedURL(g.Name);
    img.loading = "lazy";
    img.onclick = () => (gallery.select ? toggleChosen(g.Name) : viewImage(g.Name));
    const fav = document.createElement("span");
    fav.className = "fav";
    fav.textContent = g.Favorite ? "★" : "";
    // The target, date and time, as the app labels its cells.
    const name = document.createElement("div");
    name.className = "gname";
    name.textContent = `${g.Record?.Title || g.Name} · ${takenText(g)}`;
    name.title = g.Name;
    const btns = document.createElement("div");
    btns.className = "gbtns";
    const btn = (label, fn) => {
      const b = document.createElement("button");
      b.textContent = label;
      b.onclick = fn;
      btns.append(b);
    };
    btn(g.Favorite ? "Un-Favorite" : "Favorite", async () => { await act("SetFavorite", g.Name, !g.Favorite); loadGallery(); });
    btn("Share", () => shareImage(g));
    btn("View", () => viewImage(g.Name));
    btn("Edit", () => editImage(g));
    btn("Delete", () => deleteImages([g.Name]));
    card.append(img, fav, name, btns);
    return card;
  }));
}
function toggleChosen(name) {
  if (!gallery.chosen.delete(name)) gallery.chosen.add(name);
  renderGallery();
}
// deleteImages asks, deletes and reports whether it did.
async function deleteImages(names) {
  const title = names.length === 1 ? "Delete this photo?" : `Delete these ${names.length} photos?`;
  if (!(await confirmBox(title, "", "Delete"))) return false;
  const ok = (await act("DeleteImages", names)) !== false;
  gallery.chosen.clear();
  gallery.select = false; // the app ends select mode after deleting
  loadGallery();
  return ok;
}
// takenText is when an image was taken in the chosen date and time formats.
function takenText(g) {
  const m = /^(\d{4})-(\d{2})-(\d{2}) (\d{2}:\d{2}:\d{2})$/.exec(g.Record?.Taken ?? "");
  if (m) return `${calendar(m[1], m[2], m[3], settings.DateOrder)} ${clock(m[4], settings.TimeFormat)}`;
  const d = new Date(g.Modified * 1000);
  const pad = (n) => String(n).padStart(2, "0");
  return `${calendar(d.getFullYear(), d.getMonth() + 1, d.getDate(), settings.DateOrder)} ${clock(`${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`, settings.TimeFormat)}`;
}

// viewImage shows a saved image as the app's ImageDetailFragment, with its
// actions, and what its ImageMetaDataFragment lists (field in degrees with
// one decimal, "-" for what is not known).
export async function viewImage(name) {
  const g = gallery.list.find((x) => x.Name === name) ?? ((await act("Gallery")) || []).find((x) => x.Name === name) ?? { Name: name };
  const r = g.Record;
  const deg = (v) => (v ? v.toFixed(1) : "-");
  const rows = (size) => [
    ["Object", r?.Title || name],
    ["Date and Time", takenText(g)],
    ["RA", r && (r.RaDeg || r.DecDeg) ? dms(r.RaDeg / 15, true) : "-"],
    ["Dec", r && (r.RaDeg || r.DecDeg) ? dms(r.DecDeg, false) : "-"],
    ["Width (Degs.)", deg(r?.FovXDeg)],
    ["Height (Degs.)", deg(r?.FovYDeg)],
    ["Orientation", r?.FovXDeg ? `${r.RotationDeg.toFixed(1)}°` : "-"],
    ["ISO", r?.ISO || "-"],
    ["SourceImageNumberOfCompositeImage", r?.StackDepth || "-"],
    ["ImageUniqueID", r?.Uuid || "-"],
    ["Size", size],
  ];
  const fill = (size) => $("#view-info").replaceChildren(...rows(size).flatMap(([k, v]) => {
    const dt = document.createElement("dt"), dd = document.createElement("dd");
    dt.textContent = k;
    dd.textContent = String(v);
    return [dt, dd];
  }));
  $("#view-img").onload = () => fill(`${$("#view-img").naturalWidth}x${$("#view-img").naturalHeight}`);
  fill("-");
  const btn = (label, fn) => {
    const b = document.createElement("button");
    b.textContent = label;
    b.onclick = fn;
    return b;
  };
  const close = () => $("#view-dialog").close();
  $("#view-btns").replaceChildren(
    btn(g.Favorite ? "Un-Favorite" : "Favorite", async () => { await act("SetFavorite", name, !g.Favorite); await loadGallery(); viewImage(name); }),
    btn("Share", () => { close(); shareImage(g); }),
    btn("Edit", () => { close(); editImage(g); }),
    btn("Delete", async () => { if (await deleteImages([name])) close(); }),
    btn("Change Target…", () => pickTarget(name)),
  );
  $("#view-img").src = savedURL(name);
  if (!$("#view-dialog").open) $("#view-dialog").showModal();
}

// pickTarget is the image info's Change Target… (ImageMetaDataFragment
// handleChangeTarget → SearchFragment): every list is searched and the
// chosen object names the image from now on.
let pickRows = null;
async function pickTarget(name) {
  if (!pickRows) {
    const r = await act("AllTargets");
    if (!r) return;
    pickRows = r.map(prepare);
  }
  const list = () => {
    const hits = view(pickRows, [], $("#target-pick-search").value, false, { col: "Name", dir: 1 }).slice(0, 100);
    $("#target-pick-list").replaceChildren(...hits.map((i) => {
      const r = pickRows[i], li = document.createElement("li");
      li.textContent = `${r.Name} — ${r.Description || r.Type} (${r.Catalog})`;
      li.onclick = async () => {
        if ((await act("SetImageTarget", name, r.Name)) === false) return;
        $("#target-pick-dialog").close();
        await loadGallery();
        viewImage(name);
      };
      return li;
    }));
  };
  $("#target-pick-search").value = "";
  $("#target-pick-search").oninput = list;
  list();
  $("#target-pick-dialog").showModal();
}

// shareImage is the app's ImageDetailShareFragment: the chosen details are
// drawn onto a copy, which is saved next to the image and shown in the file
// manager to share from there.
let sharing = null;
function shareImage(g) {
  sharing = g;
  $("#share-dialog").showModal();
}
$("#share-go").onclick = async () => {
  const g = sharing, r = g.Record, f = $("#share-form").elements;
  const img = new Image();
  img.src = savedURL(g.Name);
  await img.decode();
  const cv = document.createElement("canvas");
  [cv.width, cv.height] = [img.naturalWidth, img.naturalHeight];
  const c = cv.getContext("2d");
  c.drawImage(img, 0, 0);
  const lines = [];
  const exposure = shareExposure(r);
  if (f.exposure.checked && exposure) lines.push(exposure);
  if (f.date.checked) lines.push([takenText(g), r && (r.LatDeg || r.LonDeg) ? latLon(r.LatDeg, r.LonDeg) : ""].filter(Boolean).join(", "));
  if (f.details.checked && r?.Title) lines.push(r.Title); // the app's Object: the target name
  const px = Math.max(14, Math.round(cv.height / 40));
  c.font = `${px}px sans-serif`;
  c.fillStyle = "#fff";
  c.shadowColor = "#000";
  c.shadowBlur = px / 4;
  lines.forEach((l, i) => c.fillText(l, px, cv.height - px * (lines.length - i)));
  if (f.logo.checked) {
    c.textAlign = "right";
    c.fillText("Nightglass", cv.width - px, cv.height - px);
  }
  $("#share-dialog").close();
  const copy = await act("SaveShared", g.Name, cv.toDataURL("image/png"));
  if (copy) { act("RevealImage", copy); loadGallery(); }
};
$("#open-gallery").onclick = () => {
  Object.assign(gallery, { tab: "all", select: false });
  gallery.chosen.clear();
  loadGallery();
  openInTab($("#gallery-dialog"));
};
for (const t of TABS) $(`#gallery-${t}`).onclick = () => { gallery.tab = t; renderGallery(); };
// Select and Cancel Selection toggle at once (toggleSelectMode).
$("#gallery-select").onclick = () => {
  gallery.select = !gallery.select;
  gallery.chosen.clear();
  renderGallery();
};
// Backing out (Escape) of select mode ends it, a large selection (more
// than 5) only once confirmed (handleBackPressed, LARGE_SELECTION_THRESHOLD).
$("#gallery-dialog").addEventListener("cancel", async (e) => {
  if (!gallery.select) return;
  e.preventDefault();
  if (gallery.chosen.size > 5 &&
    !(await confirmBox("Cancel Selection?", `Cancel selection of ${gallery.chosen.size} images?`, "Cancel Selection", "Keep Selection"))) return;
  gallery.select = false;
  gallery.chosen.clear();
  renderGallery();
});
$("#gallery-share-sel").onclick = () => {
  for (const n of gallery.chosen) act("RevealImage", n);
  gallery.select = false; // sharing ends select mode, as the app's shareItems
  gallery.chosen.clear();
  renderGallery();
};
$("#gallery-delete-sel").onclick = () => deleteImages([...gallery.chosen]);

// The editor (ImageAdjustmentsFragment): a preview at most 800 pixels
// wide, as the app's, redrawn 300 ms after a slider stops moving; Go
// applies the same edit to the full image on Overwrite or Export Copy.
const SLIDER_DEFAULTS = { brightness: 50, contrast: 50, saturation: 50, red: 0, green: 0, blue: 0 };
const editor = { image: null, src: null, rotate: 0, crop: null, timer: 0 };
const sliders = () => Object.fromEntries([...$$("#edit-sliders input")].map((el) => [el.name, Number(el.value)]));
function currentEdit() {
  const c = editor.crop ?? { X: 0, Y: 0, W: 0, H: 0 };
  return { ...sliderEdit(sliders()), Rotate: editor.rotate, Crop: c };
}
// rotated draws the preview source turned by editor.rotate quarter turns.
function rotated() {
  const s = editor.src, q = ((editor.rotate % 4) + 4) % 4, cv = document.createElement("canvas");
  [cv.width, cv.height] = q % 2 ? [s.height, s.width] : [s.width, s.height];
  const g = cv.getContext("2d");
  g.translate(cv.width / 2, cv.height / 2);
  g.rotate((q * Math.PI) / 2);
  g.drawImage(s, -s.width / 2, -s.height / 2);
  return cv;
}
function drawEdit() {
  const r = rotated(), c = editor.crop ?? { X: 0, Y: 0, W: 1, H: 1 };
  const sx = Math.round(c.X * r.width), sy = Math.round(c.Y * r.height);
  const w = Math.max(1, Math.round(c.W * r.width)), h = Math.max(1, Math.round(c.H * r.height));
  const cv = $("#edit-canvas"), g = cv.getContext("2d");
  [cv.width, cv.height] = [w, h];
  g.drawImage(r, sx, sy, w, h, 0, 0, w, h);
  const d = g.getImageData(0, 0, w, h);
  adjustPixels(d.data, currentEdit());
  g.putImageData(d, 0, 0);
}
function setEdited(changed) {
  $("#edit-revert").disabled = $("#edit-overwrite").disabled = !changed;
}
async function editImage(g) {
  const img = new Image();
  img.src = savedURL(g.Name);
  await img.decode();
  // createPreviewBitmap: at most 800 pixels on the long side.
  const k = Math.min(1, 800 / Math.max(img.width, img.height)), src = document.createElement("canvas");
  [src.width, src.height] = [Math.round(img.width * k), Math.round(img.height * k)];
  src.getContext("2d").drawImage(img, 0, 0, src.width, src.height);
  Object.assign(editor, { image: g, src, rotate: 0, crop: null });
  for (const el of $$("#edit-sliders input")) el.value = SLIDER_DEFAULTS[el.name];
  setEdited(false);
  drawEdit();
  $("#edit-dialog").showModal();
}
for (const el of $$("#edit-sliders input")) {
  el.oninput = () => {
    setEdited(true);
    clearTimeout(editor.timer);
    editor.timer = setTimeout(drawEdit, 300);
  };
}
// Double-clicking a slider's name resets it, as the app's double tap.
for (const sp of $$("#edit-sliders [data-reset]")) {
  sp.ondblclick = () => {
    $(`#edit-sliders input[name=${sp.dataset.reset}]`).value = SLIDER_DEFAULTS[sp.dataset.reset];
    drawEdit();
  };
}
$("#edit-revert").onclick = () => {
  for (const el of $$("#edit-sliders input")) el.value = SLIDER_DEFAULTS[el.name];
  Object.assign(editor, { rotate: 0, crop: null });
  setEdited(false);
  drawEdit();
};
$("#edit-overwrite").onclick = async () => {
  if (await choice("Overwrite", "Overwrite the image?", ["Overwrite", "Cancel"]) !== 0) return;
  toast("info", "Overwriting Image...");
  if ((await act("OverwriteImage", editor.image.Name, currentEdit())) === false) return;
  toast("info", "Image successfully overwritten");
  $("#edit-dialog").close();
  loadGallery();
};
$("#edit-export").onclick = async () => {
  const how = await choice("Export Copy", "How do you want to export this image?", ["Save Copy", "Share Image", "Cancel"]);
  if (how < 0 || how > 1) return;
  const name = await act("ExportImage", editor.image.Name, currentEdit());
  if (name === false) return;
  if (how === 0) toast("info", `Image saved to your save folder as ${name}`);
  else act("RevealImage", name);
  loadGallery();
};

// Crop/Rotate: drag across the turned preview; cropping a final image
// warns first, as the app does for an image with position data.
let cropDraft = null, cropStart = null, cropRotate = 0;
$("#crop-ratio").replaceChildren(...Object.keys(CROP_RATIOS).map((k) => new Option(k, k)));
// The preset's width/height in pixels of the turned preview.
function cropRatio() {
  const r = CROP_RATIOS[$("#crop-ratio").value], cv = $("#crop-canvas");
  return r === "image" ? cv.width / cv.height : r;
}
function drawCrop() {
  const saved = editor.rotate;
  editor.rotate = cropRotate;
  const r = rotated();
  editor.rotate = saved;
  const cv = $("#crop-canvas");
  [cv.width, cv.height] = [r.width, r.height];
  cv.getContext("2d").drawImage(r, 0, 0);
  const el = $("#crop-rect");
  el.hidden = !cropDraft;
  if (!cropDraft) return;
  const b = cv.getBoundingClientRect();
  Object.assign(el.style, {
    left: `${cropDraft.X * b.width}px`, top: `${cropDraft.Y * b.height}px`,
    width: `${cropDraft.W * b.width}px`, height: `${cropDraft.H * b.height}px`,
  });
}
$("#edit-crop").onclick = async () => {
  if (editor.image.Final && await choice("Crop Warning!",
    "Cropping your image even a small amount off center will nullify its position and size information, which will be reset to zeros and require a new plate-solve. There is no risk to simple rotations.",
    ["Crop/Rotate Anyway", "Cancel"]) !== 0) return;
  cropDraft = editor.crop && { ...editor.crop };
  cropRotate = editor.rotate;
  $("#crop-ratio").value = "Original"; // the app's default
  $("#crop-dialog").showModal();
  drawCrop();
};
$("#crop-box").onpointerdown = (e) => {
  cropStart = offsetIn($("#crop-canvas").getBoundingClientRect(), e.clientX, e.clientY);
  $("#crop-box").setPointerCapture(e.pointerId);
};
$("#crop-box").onpointermove = (e) => {
  if (!cropStart) return;
  const p = offsetIn($("#crop-canvas").getBoundingClientRect(), e.clientX, e.clientY), cv = $("#crop-canvas");
  cropDraft = cropRect(cropStart, p, cropRatio(), cv.width, cv.height);
  drawCrop();
};
$("#crop-box").onpointerup = () => {
  cropStart = null;
  if (cropDraft && (cropDraft.W < 0.01 || cropDraft.H < 0.01)) cropDraft = null; // a click, not a drag
  drawCrop();
};
$("#crop-rotate").onclick = () => { cropRotate = (cropRotate + 1) % 4; cropDraft = null; drawCrop(); };
$("#crop-rotate-left").onclick = () => { cropRotate = (cropRotate + 3) % 4; cropDraft = null; drawCrop(); };
$("#crop-ratio").onchange = () => { cropDraft = null; drawCrop(); };
$("#crop-reset").onclick = () => { cropRotate = 0; cropDraft = null; drawCrop(); };
$("#crop-done").onclick = () => {
  const restored = !cropDraft && cropRotate === 0;
  if (restored && (editor.crop || editor.rotate)) toast("info", "Restored to original");
  else if (!restored) toast("info", "Crop applied");
  Object.assign(editor, { crop: cropDraft, rotate: cropRotate });
  $("#crop-dialog").close();
  setEdited(true);
  drawEdit();
};
