// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Planetarium of Celestron's web client, in Nightglass's own drawing:
// a circular field of the sky seen from the observer's place, now or at a
// chosen time, with its stars (the Yale Bright Star Catalogue), the
// constellations, the chosen target list, the Sun, Moon and planets, the
// guide lines, the target and the telescope. Go places every point (as the
// target table's); this projects and draws them. A click chooses a
// target; Center Target and Center Telescope turn the view to them.
import { Events } from "/wails/runtime.js";
import { $, $$, act, showPane, stored } from "./ui.js";
import { lastMount, pos, rows, select, selected, setSettings, settings } from "./app.js";
import { commonName, dms, sexa, skyProject, skyScale, starColor, starRadius, targetFor, zoneOffset, zonedTime } from "./targets.js";

const canvas = $("#sky"), ctx = canvas.getContext("2d");
// The web client's opening view: south, 45° up, a 180° field.
const view = { alt: 45, az: 180, fov: 180 };
let chart = null; // SkyChart: stars, names, constellations, guide paths
let places = []; // SkyPositions, in SkyChart's order
let solar = { rows: [], pos: [] }; // SkyBodies: the Moon, planets and the Sun
let listPos = null; // the target list's places at a chosen time (else app.js's pos)
let star = null; // a star chosen as the target: { name, ra, dec }

// What to draw, per viewer (the web client's display options).
const show = {};
for (const el of $$("[data-sky]")) {
  const v = stored(`sky:${el.dataset.sky}`);
  if (v != null) el.checked = v === "1";
  show[el.dataset.sky] = el.checked;
  el.onchange = () => { show[el.dataset.sky] = el.checked; stored(`sky:${el.dataset.sky}`, el.checked ? "1" : "0"); draw(); };
}
for (const r of $$("input[name='sky-grid']")) {
  r.checked = r.value === (stored("sky:gridkind") || "eq");
  r.onchange = () => { stored("sky:gridkind", r.value); draw(); };
}
const gridKind = () => $("input[name='sky-grid']:checked")?.value ?? "eq";

const deg = (rad) => (rad * 180) / Math.PI;
const zone = () => settings.TimeZone || Intl.DateTimeFormat().resolvedOptions().timeZone;
// at is the chosen time as Go takes it: "" for now.
function at() {
  if ($("#sky-now").checked || !$("#sky-date").value || !$("#sky-time").value) return "";
  return new Date(zonedTime($("#sky-date").value, $("#sky-time").value, zone())).toISOString();
}

// ---- location and time, in the pane ----
function showPlace() {
  const lat = settings.LatDeg ?? 0, lon = settings.LonDeg ?? 0;
  if (document.activeElement !== $("#sky-lon")) $("#sky-lon").value = dms(Math.abs(lon), false).replace(/^[+−-]/, "");
  if (document.activeElement !== $("#sky-lat")) $("#sky-lat").value = dms(Math.abs(lat), false).replace(/^[+−-]/, "");
  for (const r of $$("input[name='sky-ew']")) r.checked = r.value === (lon < 0 ? "W" : "E");
  for (const r of $$("input[name='sky-ns']")) r.checked = r.value === (lat < 0 ? "S" : "N");
  if (document.activeElement !== $("#sky-zone")) $("#sky-zone").value = settings.TimeZone || "";
  const off = zoneOffset(Date.now(), zone());
  $("#sky-utc").textContent = `${off < 0 ? "−" : "+"}${Math.floor(Math.abs(off) / 60)}:${String(Math.abs(off) % 60).padStart(2, "0")}`;
}
// The time fields follow the clock while "Use Local System Time" is on.
function showClock() {
  if (!$("#sky-now").checked) return;
  const p = Object.fromEntries(new Intl.DateTimeFormat("en-CA", { timeZone: zone(), hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" })
    .formatToParts(new Date()).map((x) => [x.type, x.value]));
  $("#sky-date").value = `${p.year}-${p.month}-${p.day}`;
  $("#sky-time").value = `${p.hour}:${p.minute}:${p.second}`;
}
setInterval(showClock, 1000);
async function savePlace() {
  const num = (id) => sexa($(id).value.replace(/[°′″'"]/g, " ")); // as shown, or plain
  const lon = num("#sky-lon"), lat = num("#sky-lat");
  if (Number.isNaN(lon) || Number.isNaN(lat) || Math.abs(lat) > 90 || Math.abs(lon) > 180) return showPlace();
  const next = {
    ...settings, TimeZone: $("#sky-zone").value.trim() || settings.TimeZone,
    LonDeg: $("input[name='sky-ew']:checked")?.value === "W" ? -Math.abs(lon) : Math.abs(lon),
    LatDeg: $("input[name='sky-ns']:checked")?.value === "S" ? -Math.abs(lat) : Math.abs(lat),
  };
  if ((await act("SaveSettings", next)) === false) return showPlace();
  setSettings((await act("Settings")) || next);
  refresh();
}
for (const id of ["#sky-lon", "#sky-lat", "#sky-zone"]) $(id).onchange = savePlace;
for (const r of $$("input[name='sky-ew'], input[name='sky-ns']")) r.onchange = savePlace;
$("#sky-now").onchange = () => { showClock(); refresh(); };
for (const id of ["#sky-date", "#sky-time"]) $(id).onchange = () => { $("#sky-now").checked = false; refresh(); };

// ---- the field of view, both ways ----
function showView() {
  const set = (id, val) => document.activeElement !== $(id) && ($(id).value = val);
  set("#sky-az", view.az.toFixed(1));
  set("#sky-alt", view.alt.toFixed(1));
  set("#sky-width", view.fov.toFixed(1));
  $("#sky-fov").textContent = `${Math.round(view.fov)}° × ${Math.round(view.fov)}°`;
}
$("#sky-az").onchange = () => { view.az = ((Number($("#sky-az").value) % 360) + 360) % 360; draw(); };
$("#sky-alt").onchange = () => { view.alt = Math.min(Math.max(Number($("#sky-alt").value), -90), 90); draw(); };
$("#sky-width").onchange = () => { view.fov = Math.min(Math.max(Number($("#sky-width").value) || 180, 1), 180); draw(); };

// ---- data: the chart once, the places every 30 s or on a new time ----
async function refresh() {
  chart ??= await act("SkyChart");
  const t = at();
  places = (await act("SkyPositions", t)) || [];
  const b = await act("SkyBodies", t); // without changing the chosen list
  solar = { rows: b?.Rows || [], pos: b?.Places || [] };
  listPos = t ? (await act("PositionsAt", $("#catalog").value, t)) || [] : null;
  await placeTyped();
  showPlace();
  draw();
}
setInterval(() => $("#sky-now").checked && refresh(), 30_000);
setTimeout(() => { showClock(); refresh(); }, 500); // once app.js has the settings
$("#catalog").addEventListener("change", () => setTimeout(refresh, 300));

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
// symbol draws a deep-sky object's mark by its type, as star charts do.
function symbol(x, y, type) {
  const t = type ?? "";
  ctx.beginPath();
  if (/Galaxy/.test(t)) ctx.ellipse(x, y, 5, 2.5, -0.5, 0, 2 * Math.PI);
  else if (/Globular/.test(t)) {
    ctx.arc(x, y, 4, 0, 2 * Math.PI);
    ctx.moveTo(x - 4, y); ctx.lineTo(x + 4, y); ctx.moveTo(x, y - 4); ctx.lineTo(x, y + 4);
  } else if (/Planetary/.test(t)) {
    ctx.arc(x, y, 3, 0, 2 * Math.PI);
    for (const [a, b] of [[0, 1], [0, -1], [1, 0], [-1, 0]]) { ctx.moveTo(x + 3 * a, y + 3 * b); ctx.lineTo(x + 6 * a, y + 6 * b); }
  } else if (/Nebula|Supernova|Remnant/.test(t)) ctx.rect(x - 4, y - 4, 8, 8);
  else if (/Cluster|Asterism|Star Cloud/.test(t)) { ctx.setLineDash([2, 2]); ctx.arc(x, y, 4.5, 0, 2 * Math.PI); }
  else ctx.arc(x, y, 3, 0, 2 * Math.PI);
  ctx.stroke();
  ctx.setLineDash([]);
}
let v = {}; // the projection in use (for clicks)
function paint() {
  showView();
  if (!$("#mp-planetarium").classList.contains("active")) return;
  const [w, h] = size();
  const side = Math.min(w, h), R = side / 2 - 4;
  v = { alt: view.alt, az: view.az, scale: skyScale(view.fov, side - 8), cx: w / 2, cy: h / 2 };
  const light = document.documentElement.dataset.theme === "light";
  ctx.fillStyle = light ? "#dfe3ea" : "#000";
  ctx.fillRect(0, 0, w, h);
  $("#sky-empty").hidden = places.length > 0;
  // The field: a circle, the sky inside it.
  ctx.save();
  ctx.beginPath();
  ctx.arc(v.cx, v.cy, R, 0, 2 * Math.PI);
  ctx.fillStyle = "#03050b";
  ctx.fill();
  ctx.clip();
  if (places.length && chart) paintSky((alt, az) => skyProject(alt, az, v), w, h);
  ctx.restore();
  ctx.strokeStyle = "rgba(140,150,170,0.6)";
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.arc(v.cx, v.cy, R, 0, 2 * Math.PI);
  ctx.stroke();
}
function paintSky(P, w, h) {
  const line = (pts) => {
    ctx.beginPath();
    let pen = false;
    for (const p of pts) {
      if (!p) { pen = false; continue; }
      if (pen) ctx.lineTo(p[0], p[1]);
      else ctx.moveTo(p[0], p[1]);
      pen = true;
    }
    ctx.stroke();
  };
  const run = (k, n) => Array.from({ length: n }, (_, i) => P(places[k + 2 * i], places[k + 2 * i + 1]));
  ctx.font = "11px system-ui, sans-serif";
  const nStars = chart.Stars.length / 4;
  // Offsets into places: stars, each constellation's lines and boundary,
  // the constellation names, then the guide paths.
  let k = 2 * nStars;
  const span = (n) => { const a = k; k += 2 * n; return [a, n]; };
  const figures = chart.Constellations.map((f) => ({ lines: f.Lines.map(span), bounds: f.Bounds.map(span) }));
  const labels = k;
  k += 2 * chart.Constellations.length;
  const paths = chart.Paths.map((p) => ({ kind: p.Kind, span: span(p.Count) }));

  // Grids and guides, behind everything.
  if (show.grid) {
    ctx.strokeStyle = "rgba(90,120,180,0.28)";
    ctx.lineWidth = 1;
    if (gridKind() === "eq") for (const p of paths) if (p.kind === "grid") line(run(...p.span));
    if (gridKind() === "hz") {
      for (let alt = 0; alt <= 60; alt += 30) line(Array.from({ length: 121 }, (_, i) => P(alt, i * 3)));
      for (let az = 0; az < 360; az += 30) line(Array.from({ length: 31 }, (_, i) => P(i * 3, az)));
    }
  }
  for (const [kind, on, color] of [["equator", show.equator, "rgba(80,170,210,0.6)"], ["ecliptic", show.ecliptic, "rgba(230,150,40,0.75)"]]) {
    if (!on) continue;
    ctx.strokeStyle = color;
    ctx.lineWidth = 1.2;
    for (const p of paths) if (p.kind === kind) line(run(...p.span));
  }
  if (show.meridian) {
    ctx.strokeStyle = "rgba(200,200,120,0.45)";
    line([...Array.from({ length: 31 }, (_, i) => P(i * 3, 0)), ...Array.from({ length: 31 }, (_, i) => P(90 - i * 3, 180))]);
  }
  if (show.bounds) {
    ctx.strokeStyle = "rgba(200,180,60,0.45)";
    ctx.setLineDash([2, 4]);
    for (const f of figures) for (const s of f.bounds) line(run(...s));
    ctx.setLineDash([]);
  }
  if (show.lines) {
    ctx.strokeStyle = "rgba(90,140,220,0.5)";
    ctx.lineWidth = 1;
    for (const f of figures) for (const s of f.lines) line(run(...s));
  }
  // Stars, to the naked eye's limit as the field narrows.
  if (show.stars) {
    const limit = view.fov >= 120 ? 5.5 : view.fov >= 60 ? 6 : 6.5;
    for (let i = 0; i < nStars; i++) {
      const mag = chart.Stars[4 * i + 2];
      if (mag > limit) continue;
      const p = P(places[2 * i], places[2 * i + 1]);
      if (!p || p[0] < -5 || p[1] < -5 || p[0] > w + 5 || p[1] > h + 5) continue;
      ctx.fillStyle = starColor(chart.Stars[4 * i + 3]);
      ctx.beginPath();
      ctx.arc(p[0], p[1], starRadius(mag, view.fov), 0, 2 * Math.PI);
      ctx.fill();
      const name = show.snames && chart.Names[i] && (mag < 1.6 || view.fov < 120) ? chart.Names[i] : "";
      const no = show.snos && mag < (view.fov < 60 ? 5 : 2.5) ? `${mag.toFixed(1)}` : ""; // its magnitude
      if (name || no) {
        ctx.fillStyle = "rgba(235,238,245,0.85)";
        ctx.fillText([name, no].filter(Boolean).join(" "), p[0] + 5, p[1] - 4);
      }
    }
  }
  if (show.cnames) {
    ctx.fillStyle = "rgba(110,150,220,0.8)";
    ctx.font = "italic 11px system-ui, sans-serif";
    ctx.textAlign = "center";
    chart.Constellations.forEach((f, i) => {
      const p = P(places[labels + 2 * i], places[labels + 2 * i + 1]);
      if (p) ctx.fillText(f.Name, p[0], p[1]);
    });
    ctx.textAlign = "start";
    ctx.font = "11px system-ui, sans-serif";
  }
  // The chosen target list: names, catalogue numbers or both.
  const lp = listPos ?? pos;
  if (show.dso && lp.length) {
    ctx.strokeStyle = "rgba(205,210,225,0.75)";
    ctx.fillStyle = "rgba(200,205,220,0.8)";
    ctx.lineWidth = 1;
    for (let i = 0; i < rows.length; i++) {
      const r = rows[i];
      if (r.Catalog === "Solar System") continue; // drawn below
      const p = P(lp[2 * i], lp[2 * i + 1]);
      if (!p || p[0] < 0 || p[1] < 0 || p[0] > w || p[1] > h) continue;
      symbol(p[0], p[1], r.Type);
      const cn = commonName(r.Description);
      const label = [show.dnames && (cn || (!show.dnos && r.Name)), show.dnos && r.Name].filter(Boolean).join(", ");
      if (label) ctx.fillText(label, p[0] + 7, p[1] + 4);
    }
  }
  if (show.solar) {
    solar.rows.forEach((r, i) => {
      const p = P(solar.pos[2 * i], solar.pos[2 * i + 1]);
      if (!p) return;
      const big = r.Type === "Moon" || r.Type === "Sun";
      ctx.fillStyle = r.Type === "Sun" ? "#ffd54a" : r.Type === "Moon" ? "#e8e4d8" : "#f2d48b";
      ctx.beginPath();
      // The Sun and Moon at their size (0.26°; a unit is a radian at the centre).
      ctx.arc(p[0], p[1], big ? Math.max(6, (0.26 * Math.PI / 180) * v.scale) : 3.5, 0, 2 * Math.PI);
      ctx.fill();
      if (show.pnames) {
        ctx.fillStyle = "#f2d48b";
        ctx.fillText(r.Name, p[0] + 8, p[1] - 6);
      }
    });
  }
  // The horizon mask: the projection keeps the horizon a circle, the sky
  // inside it when the view's centre is above the horizon. Exactly on the
  // horizon the circle is a line, so the centre is nudged off it here.
  if (show.ground) {
    const g = { ...v, alt: Math.abs(v.alt) < 0.05 ? (v.alt < 0 ? -0.05 : 0.05) : v.alt };
    ctx.fillStyle = "rgba(30,80,30,0.45)";
    ctx.beginPath();
    for (let az = 0; az <= 360; az++) {
      const p = skyProject(0, az, g, true);
      if (az) ctx.lineTo(p[0], p[1]);
      else ctx.moveTo(p[0], p[1]);
    }
    ctx.closePath();
    if (g.alt > 0) ctx.rect(0, 0, w, h);
    ctx.fill(g.alt > 0 ? "evenodd" : "nonzero");
    ctx.strokeStyle = "rgba(120,190,110,0.8)";
    ctx.lineWidth = 1;
    line(Array.from({ length: 361 }, (_, i) => P(0, i)));
  }
  if (show.cardinal) {
    ctx.fillStyle = "#7bc96f";
    ctx.font = "bold 12px system-ui, sans-serif";
    ctx.textAlign = "center";
    ["N", "NE", "E", "SE", "S", "SW", "W", "NW"].forEach((l, i) => {
      const p = P(0, i * 45);
      if (p) ctx.fillText(l, p[0], p[1] - 6);
    });
    ctx.textAlign = "start";
  }
  // The target, then the telescope's reticle (now only).
  const t = targetPlace();
  if (t) {
    const p = P(t[0], t[1]);
    if (p) {
      ctx.strokeStyle = "#ff6b6b";
      ctx.lineWidth = 1.5;
      ctx.strokeRect(p[0] - 9, p[1] - 9, 18, 18);
    }
  }
  if (lastMount.Alt != null && lastMount.Azm != null && $("#sky-now").checked) {
    const p = P(deg(lastMount.Alt), deg(lastMount.Azm));
    if (p) {
      ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue("--accent").trim() || "#3b82f6";
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
let typedPlace = null; // where a chosen star or typed coordinates are
function targetPlace() {
  const lp = listPos ?? pos;
  if (selected >= 0 && lp.length) return [lp[2 * selected], lp[2 * selected + 1]];
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
// placeTyped finds where a chosen star or typed coordinates are.
async function placeTyped() {
  typedPlace = null;
  if (selected >= 0) return;
  const t = star ? { RA: star.ra, Dec: star.dec } : targetFor(null, $("#ra").value, $("#dec").value);
  if (t) typedPlace = await act("AltAzOf", t.RA, t.Dec, at());
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
  [view.alt, view.az] = t;
  view.fov = Math.min(view.fov, 60);
  draw();
}
$("#sky-center-target").onclick = center;
// The Target tab's Center: the Planetarium, turned to the target.
$("#center-target").onclick = () => { showPane("mp-planetarium"); center(); };
$("#sky-center-telescope").onclick = () => {
  if (lastMount.Alt == null) return;
  view.alt = deg(lastMount.Alt);
  view.az = ((deg(lastMount.Azm) % 360) + 360) % 360;
  view.fov = Math.min(view.fov, 60);
  draw();
};
$("#sky-goto").onclick = () => $("#goto").click();
$("#sky-mosaic").onclick = () => $("#mosaic").click();

// ---- looking around: drag, wheel, buttons ----
const zoom = (f) => { view.fov = Math.min(Math.max(view.fov * f, 1), 180); draw(); };
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
  view.az = (((view.az + (dx * per) / Math.max(Math.cos((view.alt * Math.PI) / 180), 0.25)) % 360) + 360) % 360;
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
  const lp = listPos ?? pos;
  if (show.dso && lp.length) rows.forEach((_, i) => near(lp[2 * i], lp[2 * i + 1], 10, { row: i }));
  if (show.solar) solar.rows.forEach((s, i) => s.Type !== "Sun" && near(solar.pos[2 * i], solar.pos[2 * i + 1], 12, { body: s.Name }));
  if (!best && chart && show.stars) {
    for (let i = 0; i < chart.Stars.length / 4; i++) if (chart.Stars[4 * i + 2] <= 6.5) near(places[2 * i], places[2 * i + 1], 8, { star: i });
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
    const i = rows.findIndex((row) => row.Name === w.body);
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
