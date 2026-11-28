// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Final Image tab of Celestron's web client: the last final image, with
// zoom, the pixel under the pointer, and its own Strength, Background and
// De-noise, applied by Go on this computer (the Origin's stretch is left as
// it is). Saved images open from its "Saved Images" button.
import { Events } from "/wails/runtime.js";
import { $, act } from "./ui.js";
import { zoomAt } from "./targets.js";

let seq = 0; // the final image shown, by its frame number
const img = $("#final-frame");
const view = { z: 1, x: 0, y: 0, drag: null };

// Background runs 0.005–0.5 on a log scale, as the web client's slider.
const background = (pos) => 0.005 * 100 ** (pos / 100);
const backgroundPos = (b) => Math.round((100 * Math.log(b / 0.005)) / Math.log(100));
function labels() {
  $("#final-strength-val").textContent = (Number($("#final-strength").value) / 100).toFixed(2);
  $("#final-background-val").textContent = background(Number($("#final-background").value)).toFixed(3);
  const d = Number($("#final-denoise").value);
  $("#final-denoise-val").textContent = d ? String(d) : "Off";
}
function load() {
  const q = new URLSearchParams({ s: Number($("#final-strength").value) / 100, b: background(Number($("#final-background").value)), d: $("#final-denoise").value, n: seq });
  img.src = `/frame/final/view?${q}`;
}
// An 8-bit final has nothing to stretch: shown as it is, the sliders off.
img.onerror = () => {
  if (img.src.includes("/final/view")) img.src = `/frame/final?${seq}`;
  for (const id of ["#final-strength", "#final-background", "#final-denoise"]) $(id).disabled = true;
};
let pixels = null;
img.onload = () => {
  const c = Object.assign(document.createElement("canvas"), { width: img.naturalWidth, height: img.naturalHeight });
  const ctx = c.getContext("2d", { willReadFrequently: true });
  ctx.drawImage(img, 0, 0);
  pixels = ctx.getImageData(0, 0, c.width, c.height);
};

Events.On("frame", (ev) => {
  const { Kind, Seq, Info } = ev.data;
  if (Kind !== "final") return;
  seq = Seq;
  // The Origin's own stretch to start from, as the web client's.
  $("#final-strength").value = Math.round((Info.StretchStrength ?? 0.9) * 100);
  $("#final-background").value = backgroundPos(Info.StretchBackground || 0.035);
  $("#final-denoise").value = 0;
  for (const id of ["#final-strength", "#final-background", "#final-denoise"]) $(id).disabled = false;
  labels();
  img.hidden = false;
  $("#final-empty").hidden = true;
  $("#final-save").disabled = false;
  $("#final-info").textContent = [Info.ObjectName, Info.StackDepth ? `${Info.StackDepth} stacked` : "", Info.ExposureTime ? `${Info.ExposureTime} s` : ""]
    .filter(Boolean).join(" · ");
  zoom({ z: 1, x: 0, y: 0 });
  load();
});
for (const id of ["#final-strength", "#final-background", "#final-denoise"]) {
  $(id).oninput = labels;
  $(id).onchange = load; // when let go: each is a new render in Go
}
$("#final-save").onclick = () => act("SaveFinal");
$("#open-saved").onclick = () => $("#open-gallery").click();

// Zoom and pan, as the live view's.
function zoom(v) {
  Object.assign(view, v);
  img.style.transform = view.z === 1 ? "" : `translate(${view.x}px, ${view.y}px) scale(${view.z})`;
  img.classList.toggle("zoomed", view.z > 1);
  $("#fz-label").textContent = `${+view.z.toFixed(1)}×`;
}
const at = (e) => {
  const r = img.getBoundingClientRect();
  return [e.clientX - (r.left - view.x), e.clientY - (r.top - view.y)];
};
function centre(f) {
  if (img.hidden) return;
  const b = $("#final-view-box").getBoundingClientRect(), r = img.getBoundingClientRect();
  zoom(zoomAt(view, b.left + b.width / 2 - (r.left - view.x), b.top + b.height / 2 - (r.top - view.y), f));
}
$("#fz-in").onclick = () => centre(1.5);
$("#fz-out").onclick = () => centre(1 / 1.5);
$("#final-view-box").addEventListener("wheel", (e) => {
  if (img.hidden) return;
  e.preventDefault();
  zoom(zoomAt(view, ...at(e), Math.exp(-e.deltaY / 300)));
}, { passive: false });
img.ondblclick = (e) => zoom(view.z > 1 ? { z: 1, x: 0, y: 0 } : zoomAt(view, ...at(e), 2.5));
img.ondragstart = (e) => e.preventDefault();
img.onpointerdown = (e) => {
  if (view.z === 1) return;
  view.drag = { x: e.clientX - view.x, y: e.clientY - view.y };
  img.setPointerCapture(e.pointerId);
};
img.onpointerup = img.onpointercancel = () => (view.drag = null);
const ids = ["fpx-x", "fpx-y", "fpx-r", "fpx-g", "fpx-b"];
img.onpointermove = (e) => {
  if (view.drag) zoom({ x: e.clientX - view.drag.x, y: e.clientY - view.drag.y });
  if (!pixels) return;
  const r = img.getBoundingClientRect();
  const x = Math.floor(((e.clientX - r.left) / r.width) * pixels.width), y = Math.floor(((e.clientY - r.top) / r.height) * pixels.height);
  if (x < 0 || y < 0 || x >= pixels.width || y >= pixels.height) return;
  const i = (y * pixels.width + x) * 4;
  [x, y, pixels.data[i], pixels.data[i + 1], pixels.data[i + 2]].forEach((v, k) => ($(`#${ids[k]}`).textContent = v));
};
img.onpointerleave = () => ids.forEach((id) => ($(`#${id}`).textContent = "—"));
