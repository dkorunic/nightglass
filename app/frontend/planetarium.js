// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Planetarium of Celestron's web client, in Nightglass's own drawing:
// the sky above the observer, its stars (the Yale Bright Star Catalogue)
// and constellations, the chosen target list, the Moon and planets, the
// target and the telescope. Go places every point (as the target table's);
// this projects and draws them. A click chooses a target, Center Target
// and Center Telescope turn the view to them.
import { Events } from "/wails/runtime.js";
import { $, $$, act, showPane, stored } from "./ui.js";
import { lastMount, pos, rows, select, selected, settings } from "./app.js";
import { dms, skyProject, skyScale, starColor, starRadius, targetFor } from "./targets.js";

const canvas = $("#sky"), ctx = canvas.getContext("2d");
const view = { alt: 90, az: 180, fov: 185 }; // the whole sky, north up
let chart = null; // SkyChart: stars, names, constellations
let places = []; // SkyPositions: stars, line vertices, constellation names
let solar = { rows: [], pos: [] };
let star = null; // a star chosen as the target: { name, ra, dec }
const show = {}; // what to draw, per viewer
for (const el of $$("[data-sky]")) {
  const v = stored(`sky:${el.dataset.sky}`);
  if (v != null) el.checked = v === "1";
  show[el.dataset.sky] = el.checked;
  el.onchange = () => { show[el.dataset.sky] = el.checked; stored(`sky:${el.dataset.sky}`, el.checked ? "1" : "0"); draw(); };
}
{
  const v = Number(stored("sky:mag"));
  if (v >= 2 && v <= 6.5) $("#sky-mag").value = v;
  $("#sky-mag").oninput = () => { $("#sky-mag-val").textContent = Number($("#sky-mag").value).toFixed(1); stored("sky:mag", $("#sky-mag").value); draw(); };
  $("#sky-mag-val").textContent = Number($("#sky-mag").value).toFixed(1); // drawn once the data comes
}

const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
const deg = (rad) => (rad * 180) / Math.PI;

// ---- data: the chart once, the places every 30 s ----
async function refresh() {
  chart ??= await act("SkyChart");
  places = (await act("SkyPositions")) || [];
  const b = await act("SkyBodies"); // without changing the chosen list
  solar = { rows: b?.Rows || [], pos: b?.Places || [] };
  const tz = settings.TimeZone || undefined;
  $("#sky-place").textContent = settings.LatDeg || settings.LonDeg
    ? `${Math.abs(settings.LatDeg).toFixed(2)}° ${settings.LatDeg < 0 ? "S" : "N"}, ${Math.abs(settings.LonDeg).toFixed(2)}° ${settings.LonDeg < 0 ? "W" : "E"}` : "Not set";
  $("#sky-time").textContent = new Intl.DateTimeFormat(undefined, { timeZone: tz, dateStyle: "medium", timeStyle: "short" }).format(new Date());
  draw();
}
setInterval(refresh, 30_000);
setTimeout(refresh, 500); // once app.js has the settings

// ---- drawing ----
let queued = false;
function draw() {
  if (queued) return;
  queued = true;
  requestAnimationFrame(() => { queued = false; paint(); });
}
function size() {
  const r = $("#sky-box").getBoundingClientRect(), d = devicePixelRatio || 1;
  if (canvas.width !== Math.round(r.width * d) || canvas.height !== Math.round(r.height * d)) {
    canvas.width = Math.round(r.width * d);
    canvas.height = Math.round(r.height * d);
  }
  ctx.setTransform(d, 0, 0, d, 0, 0);
  return [r.width, r.height];
}
let v = {}; // the projection in use (for clicks)
function paint() {
  if (!$("#mp-planetarium").classList.contains("active")) return;
  const [w, h] = size();
  // The field is across the shorter side: at 180° the whole sky fits, as the web client's.
  const side = Math.min(w, h);
  v = { alt: view.alt, az: view.az, scale: skyScale(view.fov, side), cx: w / 2, cy: h / 2 };
  const P = (alt, az) => skyProject(alt, az, v);
  const dark = document.documentElement.dataset.theme !== "light";
  ctx.fillStyle = dark ? "#05070d" : "#0b1530";
  ctx.fillRect(0, 0, w, h);
  $("#sky-fov").textContent = `${Math.round((view.fov * w) / side)}° × ${Math.round((view.fov * h) / side)}°`;
  $("#sky-empty").hidden = places.length > 0;
  if (!places.length || !chart) return;
  const accent = css("--accent") || "#3b82f6";
  ctx.font = "11px system-ui, sans-serif";

  const line = (pts, closed = false) => {
    ctx.beginPath();
    let pen = false;
    for (const p of pts) {
      if (!p) { pen = false; continue; }
      if (pen) ctx.lineTo(p[0], p[1]);
      else ctx.moveTo(p[0], p[1]);
      pen = true;
    }
    if (closed) ctx.closePath();
    ctx.stroke();
  };
  if (show.grid) {
    ctx.strokeStyle = "rgba(120,140,190,0.18)";
    ctx.lineWidth = 1;
    for (let alt = -60; alt <= 60; alt += 30) {
      line(Array.from({ length: 121 }, (_, i) => P(alt, i * 3)));
    }
    for (let az = 0; az < 360; az += 30) line(Array.from({ length: 61 }, (_, i) => P(-90 + i * 3, az)));
  }
  // Constellation figures, behind the stars.
  const nStars = chart.Stars.length / 4;
  let k = 2 * nStars;
  if (show.lines) {
    ctx.strokeStyle = "rgba(90,140,220,0.45)";
    ctx.lineWidth = 1;
  }
  for (const f of chart.Constellations) {
    for (const n of f.Lines) {
      if (show.lines) line(Array.from({ length: n }, (_, i) => P(places[k + 2 * i], places[k + 2 * i + 1])));
      k += 2 * n;
    }
  }
  // Stars, faintest first so the bright ones lie on top.
  const limit = Number($("#sky-mag").value);
  for (let i = 0; i < nStars; i++) {
    const mag = chart.Stars[4 * i + 2];
    if (mag > limit) continue;
    const p = P(places[2 * i], places[2 * i + 1]);
    if (!p || p[0] < -5 || p[1] < -5 || p[0] > w + 5 || p[1] > h + 5) continue;
    ctx.fillStyle = starColor(chart.Stars[4 * i + 3]);
    ctx.beginPath();
    ctx.arc(p[0], p[1], starRadius(mag, view.fov), 0, 2 * Math.PI);
    ctx.fill();
    const name = show.snames && chart.Names[i];
    if (name && (mag < 1.6 || view.fov < 100)) {
      ctx.fillStyle = "rgba(220,225,240,0.75)";
      ctx.fillText(name, p[0] + 5, p[1] - 4);
    }
  }
  if (show.cnames) {
    ctx.fillStyle = "rgba(110,160,235,0.7)";
    ctx.textAlign = "center";
    chart.Constellations.forEach((f, i) => {
      const p = P(places[k + 2 * i], places[k + 2 * i + 1]);
      if (p) ctx.fillText(f.Name, p[0], p[1]);
    });
    ctx.textAlign = "start";
  }
  // The chosen target list.
  if (show.dso && pos.length) {
    ctx.strokeStyle = "rgba(240,200,110,0.8)";
    ctx.fillStyle = "rgba(240,200,110,0.85)";
    ctx.lineWidth = 1;
    for (let i = 0; i < rows.length; i++) {
      if (rows[i].Catalog === "Solar System") continue; // drawn below
      const p = P(pos[2 * i], pos[2 * i + 1]);
      if (!p || p[0] < 0 || p[1] < 0 || p[0] > w || p[1] > h) continue;
      ctx.beginPath();
      ctx.ellipse(p[0], p[1], 4, 2.5, 0, 0, 2 * Math.PI);
      ctx.stroke();
      if (view.fov <= 120) ctx.fillText(rows[i].Name, p[0] + 6, p[1] + 10);
    }
  }
  if (show.solar) {
    solar.rows.forEach((r, i) => {
      const p = P(solar.pos[2 * i], solar.pos[2 * i + 1]);
      if (!p) return;
      const moon = r.Type === "Moon";
      ctx.fillStyle = moon ? "#e8e4d8" : "#f2d48b";
      ctx.beginPath();
      // The Moon at its size (0.26° radius; a unit is a radian at the centre).
      ctx.arc(p[0], p[1], moon ? Math.max(5, (0.26 * Math.PI / 180) * v.scale) : 3.5, 0, 2 * Math.PI);
      ctx.fill();
      ctx.fillText(r.Name, p[0] + 7, p[1] - 6);
    });
  }
  // The ground: the projection keeps the horizon a circle, the sky inside it
  // when the view's centre is above the horizon, the ground inside it when
  // below. Exactly on the horizon the circle is a line, so the centre is
  // nudged off it for this only.
  if (show.ground) {
    const g = { ...v, alt: Math.abs(v.alt) < 0.05 ? (v.alt < 0 ? -0.05 : 0.05) : v.alt };
    ctx.fillStyle = dark ? "rgba(26,32,22,0.92)" : "rgba(40,52,34,0.92)";
    ctx.beginPath();
    for (let az = 0; az <= 360; az++) {
      const p = skyProject(0, az, g, true);
      if (az) ctx.lineTo(p[0], p[1]);
      else ctx.moveTo(p[0], p[1]);
    }
    ctx.closePath();
    if (g.alt > 0) ctx.rect(0, 0, w, h);
    ctx.fill(g.alt > 0 ? "evenodd" : "nonzero");
    ctx.strokeStyle = "rgba(150,170,120,0.8)";
    ctx.lineWidth = 1;
    line(Array.from({ length: 361 }, (_, i) => P(0, i)));
  }
  ctx.fillStyle = "#e8c35a";
  ctx.font = "bold 13px system-ui, sans-serif";
  for (const [l, az] of [["N", 0], ["E", 90], ["S", 180], ["W", 270]]) {
    const p = P(0, az);
    if (p) ctx.fillText(l, p[0] - 4, p[1] - 6);
  }
  // The target, then the telescope's reticle.
  const t = targetPlace();
  if (t) {
    const p = P(t[0], t[1]);
    if (p) {
      ctx.strokeStyle = "#ff6b6b";
      ctx.lineWidth = 1.5;
      ctx.strokeRect(p[0] - 9, p[1] - 9, 18, 18);
    }
  }
  if (lastMount.Alt != null && lastMount.Azm != null) {
    const p = P(deg(lastMount.Alt), deg(lastMount.Azm));
    if (p) {
      ctx.strokeStyle = accent;
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(p[0], p[1], 12, 0, 2 * Math.PI);
      for (const [a, b, c, d] of [[-20, 0, -8, 0], [8, 0, 20, 0], [0, -20, 0, -8], [0, 8, 0, 20]]) {
        ctx.moveTo(p[0] + a, p[1] + b);
        ctx.lineTo(p[0] + c, p[1] + d);
      }
      ctx.stroke();
    }
  }
}

// ---- the target ----
let typedPlace = null; // AltAzOf for typed coordinates or a chosen star
function targetPlace() {
  if (selected >= 0 && pos.length) return [pos[2 * selected], pos[2 * selected + 1]];
  return typedPlace;
}
function updateButtons() {
  const has = selected >= 0 || !!($("#ra").value && $("#dec").value);
  $("#sky-target").textContent = has ? $("#selected").textContent : "None";
  $("#sky-center-target").disabled = $("#center-target").disabled = !has;
  $("#sky-center-telescope").disabled = lastMount.Alt == null;
  $("#sky-goto").disabled = $("#goto").disabled;
  $("#sky-mosaic").disabled = $("#mosaic").disabled;
}
// placeTyped finds where a chosen star or typed coordinates are now.
async function placeTyped() {
  typedPlace = null;
  if (selected >= 0) return;
  const t = star ? { RA: star.ra, Dec: star.dec } : targetFor(null, $("#ra").value, $("#dec").value);
  if (t) typedPlace = await act("AltAzOf", t.RA, t.Dec);
}
// Coordinates typed by hand replace a chosen star.
for (const id of ["#ra", "#dec"]) $(id).addEventListener("input", (e) => e.isTrusted && (star = null));
// The table, the coordinates or a click changes the target.
new MutationObserver(() => { if (selected >= 0) star = null; placeTyped().then(draw); updateButtons(); })
  .observe($("#selected"), { childList: true, characterData: true, subtree: true });
for (const id of ["#goto", "#mosaic"]) new MutationObserver(updateButtons).observe($(id), { attributes: true, attributeFilter: ["disabled"] });

async function center() {
  if (selected < 0) await placeTyped();
  const t = targetPlace();
  if (!t) return;
  view.alt = t[0];
  view.az = t[1];
  view.fov = Math.min(view.fov, 60);
  draw();
}
$("#sky-center-target").onclick = center;
// The Target tab's Center: the Planetarium, turned to the target.
$("#center-target").onclick = () => { showPane("mp-planetarium"); center(); };
$("#sky-center-telescope").onclick = () => {
  if (lastMount.Alt == null) return;
  view.alt = deg(lastMount.Alt);
  view.az = deg(lastMount.Azm);
  view.fov = Math.min(view.fov, 60);
  draw();
};
$("#sky-goto").onclick = () => $("#goto").click();
$("#sky-mosaic").onclick = () => $("#mosaic").click();

// ---- looking around: drag, wheel, buttons ----
const zoom = (f) => { view.fov = Math.min(Math.max(view.fov * f, 1), 220); draw(); };
$("#sky-in").onclick = () => zoom(1 / 1.5);
$("#sky-out").onclick = () => zoom(1.5);
canvas.addEventListener("wheel", (e) => { e.preventDefault(); zoom(Math.exp(e.deltaY / 400)); }, { passive: false });
let drag = null;
canvas.onpointerdown = (e) => { drag = { x: e.clientX, y: e.clientY, moved: false }; canvas.setPointerCapture(e.pointerId); };
canvas.onpointermove = (e) => {
  if (!drag) return;
  const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
  if (!drag.moved && Math.hypot(dx, dy) < 4) return;
  drag.moved = true;
  const per = view.fov / Math.min(canvas.clientWidth, canvas.clientHeight); // degrees per pixel at the centre
  // Turning toward the zenith reverses the azimuth's sense near it.
  view.az = (((view.az + dx * per / Math.max(Math.cos((view.alt * Math.PI) / 180), 0.25)) % 360) + 360) % 360;
  view.alt = Math.min(Math.max(view.alt + dy * per, -90), 90);
  drag.x = e.clientX;
  drag.y = e.clientY;
  draw();
};
canvas.onpointerup = (e) => {
  const click = drag && !drag.moved;
  drag = null;
  if (click) pick(e);
};
// pick chooses the nearest object under the pointer: a target list's
// object, the Moon or a planet, else a star (as typed coordinates).
async function pick(e) {
  const r = canvas.getBoundingClientRect(), x = e.clientX - r.left, y = e.clientY - r.top;
  let best = null;
  const near = (alt, az, max, what) => {
    const p = skyProject(alt, az, v);
    if (!p) return;
    const d = Math.hypot(p[0] - x, p[1] - y);
    if (d < max && (!best || d < best.d)) best = { d, what };
  };
  if (show.dso) rows.forEach((_, i) => pos.length && near(pos[2 * i], pos[2 * i + 1], 10, { row: i }));
  if (show.solar) solar.rows.forEach((s, i) => near(solar.pos[2 * i], solar.pos[2 * i + 1], 12, { body: s.Name }));
  if (!best && chart) {
    const limit = Number($("#sky-mag").value);
    for (let i = 0; i < chart.Stars.length / 4; i++) {
      if (chart.Stars[4 * i + 2] <= limit) near(places[2 * i], places[2 * i + 1], 8, { star: i });
    }
  }
  if (!best) return;
  const w = best.what;
  if (w.row != null) return select(w.row);
  if (w.body) {
    // A body of the Solar System list, chosen from that list.
    if ($("#catalog").value !== "Solar System") {
      $("#catalog").value = "Solar System";
      await $("#catalog").onchange();
    }
    const i = rows.findIndex((r) => r.Name === w.body);
    if (i >= 0) select(i);
    return;
  }
  // A star: its J2000 place as typed coordinates, named.
  const i = w.star, ra = chart.Stars[4 * i], dec = chart.Stars[4 * i + 1];
  star = { name: chart.Names[i] || `Star (mag ${chart.Stars[4 * i + 2].toFixed(1)})`, ra, dec };
  $("#ra").value = dms(ra / 15, true).replace(/[h′″]/g, "");
  $("#dec").value = dms(dec, false).replace(/[°′″+]/g, "").replace("−", "-");
  $("#ra").dispatchEvent(new Event("input"));
  $("#selected").textContent = star.name;
  showPane("cp-target");
}

Events.On("status", (ev) => ev.data.Key === "Mount/GetStatus" && setTimeout(() => { updateButtons(); draw(); }));
Events.On("conn", () => setTimeout(() => { updateButtons(); refresh(); }));
$(".tab[data-pane='mp-planetarium']").addEventListener("click", () => setTimeout(draw));
new ResizeObserver(draw).observe($("#sky-box"));
new MutationObserver(draw).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
