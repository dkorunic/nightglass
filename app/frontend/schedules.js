// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Schedules tab of Celestron's web client (the app has none): tonight's
// targets, run by the Origin itself as one imaging list (it waits for each
// to rise), and the lists run before, to rerun or delete.
import { Events } from "/wails/runtime.js";
import { $, act, choice, confirmBox, openInTab, showAlert } from "./ui.js";
import { connected, lastTask, rows, selected, setSettings, settings } from "./app.js";
import { byVisibility, scheduleStarts, visibilityText, zoneClock, clock } from "./targets.js";

let isos = [100, 200, 500, 1000, 2000, 5000, 10000, 20000]; // the camera's, once connected
let vis = []; // Visibility of each draft entry
const draft = () => settings.Schedule ?? [];
const running = () => lastTask.State === "IMAGING_LIST";
const zone = () => settings.TimeZone || undefined;
const when = (ms) => new Intl.DateTimeFormat(undefined, {
  timeZone: zone(), day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hour12: (settings.TimeFormat || "").startsWith("12"),
}).format(ms);
const fmt = (s) => clock(zoneClock(s), (settings.TimeFormat || "24h").replace(/s$/, ""));

async function store(entries) {
  if ((await act("SetSchedule", entries)) === false) return false;
  setSettings((await act("Settings")) || settings);
  await renderDraft();
  return true;
}
async function visibility() {
  vis = await Promise.all(draft().map((e) => act("Visibility", e.RA, e.Dec)));
}

async function renderDraft() {
  await visibility();
  const entries = draft(), run = running(), starts = scheduleStarts(entries, vis, Date.now());
  const objects = lastTask.ImagingInfo?.ObjectInfoList ?? [];
  const cell = (text, cls = "") => Object.assign(document.createElement("span"), { textContent: text, className: cls });
  const input = (v, onchange) => {
    const el = Object.assign(document.createElement("input"), { value: v, size: 5, disabled: run });
    el.onchange = () => onchange(el);
    return el;
  };
  const update = (i, f) => store(entries.map((e, k) => (k === i ? { ...e, ...f } : e)));
  const out = entries.map((e, i) => {
    const row = document.createElement("div");
    row.className = `sched-row${run ? " running" : ""}`;
    const iso = document.createElement("select");
    iso.disabled = run;
    iso.append(...isos.map((v) => new Option(String(v), String(v))));
    iso.value = String(e.ISO);
    iso.onchange = () => update(i, { ISO: Number(iso.value) });
    row.append(cell(e.Name, "name"), cell(when(starts[i])),
      input(Math.round(e.DurationSec / 60), (el) => {
        const m = Number(el.value);
        Number.isInteger(m) && m > 0 ? update(i, { DurationSec: m * 60 }) : renderDraft();
      }),
      input(e.ExposureSec, (el) => {
        const s = Number(el.value);
        s > 0 ? update(i, { ExposureSec: s }) : renderDraft();
      }),
      iso, cell(visibilityText(vis[i], fmt)));
    if (run) {
      const st = objects[i]?.StackingStatus;
      row.append(cell({ COMPLETED: "Completed", FAILED: "Failed", RUNNING: "Running", DITHERING: "Running" }[st] ?? "Not Started", "status"));
    } else {
      const btn = (text, title, disabled, fn) => {
        const b = Object.assign(document.createElement("button"), { textContent: text, title, disabled });
        b.onclick = fn;
        return b;
      };
      const move = (d) => { const n = entries.slice(); [n[i], n[i + d]] = [n[i + d], n[i]]; store(n); };
      row.append(btn("▲", "Move Up", i === 0, () => move(-1)), btn("▼", "Move Down", i === entries.length - 1, () => move(1)),
        btn("✕", "Delete", false, () => store(entries.filter((_, k) => k !== i))));
    }
    return row;
  });
  if (out.length) {
    const head = document.createElement("div");
    head.className = "sched-row head";
    head.append(...["Name", "Start Time", "Duration (min)", "Exposure (sec)", "ISO", "Visibility", run ? "Status" : ""].map((t) => cell(t)));
    out.unshift(head);
  }
  $("#sched-table").replaceChildren(...out);
  $("#sched-empty").hidden = entries.length > 0;
  $("#sched-run").textContent = run ? "Imaging List Running…" : "Run Tonight's Schedule Now";
  $("#sched-run").disabled = run || !connected || lastTask.State !== "IDLE" || !entries.length;
  $("#sched-sort").disabled = run || entries.length < 2;
  $("#sched-autofocus").checked = !!settings.ScheduleAutofocus;
  $("#sched-saveraw").checked = settings.SaveRaw !== false;
  $("#sched-powerdown").checked = !!settings.SchedulePowerDown;
}

const STATUS = { COMPLETED: "Completed", FAILED: "Failed", RUNNING: "Running", DITHERING: "Running", STOPPED: "Stopped" };
async function renderPrevious() {
  const lists = connected ? (await act("Schedules")) || [] : [];
  const active = running() ? lastTask.ImagingInfo?.ListUuid : "";
  const shown = lists.filter((l) => !(l.Status === "RUNNING" && l.Uuid === active));
  $("#sched-previous").replaceChildren(...shown.map((l) => {
    const li = document.createElement("li");
    const title = `${l.DateTime} (${l.ObjectsCompleted}/${l.ObjectsTotal})`;
    const head = Object.assign(document.createElement("div"), { className: "prev-head" });
    const name = Object.assign(document.createElement("span"), { className: "grow", textContent: `▸ ${title}` });
    const detail = Object.assign(document.createElement("div"), { className: "prev-detail", hidden: true });
    name.onclick = async () => {
      detail.hidden = !detail.hidden;
      name.textContent = `${detail.hidden ? "▸" : "▾"} ${title}`;
      if (detail.hidden || detail.childElementCount) return;
      detail.textContent = "Loading targets…";
      const info = await act("ScheduleInfo", l.Uuid);
      const t = document.createElement("table");
      const hr = t.insertRow();
      for (const h of ["Target", "Frames", "Total Time", "Status"]) hr.append(Object.assign(document.createElement("th"), { textContent: h }));
      for (const o of info?.ObjectInfoList ?? []) {
        const tr = t.insertRow();
        for (const v of [o.ObjectName, o.StackDepth ?? "–", o.TotalTime != null ? `${Math.round(o.TotalTime)}s` : "–", STATUS[o.StackingStatus] ?? "Not Started"]) {
          tr.insertCell().textContent = v;
        }
      }
      detail.replaceChildren(t);
    };
    const rerun = Object.assign(document.createElement("button"), { textContent: "Rerun" });
    rerun.onclick = async () => {
      const c = await choice("Rerun Schedule", `Re-image targets in "${title}":`, ["Cancel", "All Targets", "Only Failed Targets"]);
      if (c > 0 && (await act("RerunSchedule", l.Uuid, c === 1)) !== false) setTimeout(renderPrevious, 1000);
    };
    const del = Object.assign(document.createElement("button"), { textContent: "Delete", className: "btn-danger" });
    del.onclick = async () => {
      const c = await choice("Delete Schedule", `Delete schedule "${title}"?`, ["Cancel", "Delete", "Delete With Image Files"]);
      if (c > 0 && (await act("DeleteSchedule", l.Uuid, c === 2)) !== false) setTimeout(renderPrevious, 1000);
    };
    head.append(name, rerun, del);
    li.append(head, detail);
    return li;
  }));
  $("#sched-previous-empty").hidden = shown.length > 0;
}

$("#open-schedules").onclick = async () => {
  openInTab($("#schedules-dialog"));
  $("#open-tonight").click(); // tonight's Sun and Moon beside it, in this tab
  const v = connected ? await act("SupportedISOs") : null;
  if (v?.length) isos = v;
  renderDraft();
  renderPrevious();
};
$("#sched-run").onclick = async () => {
  if ((await act("RunSchedule")) !== false) setTimeout(renderPrevious, 1000);
};
$("#sched-sort").onclick = () => store(byVisibility(vis, Date.now()).map((i) => draft()[i]));
for (const [id, key] of [["#sched-autofocus", "ScheduleAutofocus"], ["#sched-saveraw", "SaveRaw"], ["#sched-powerdown", "SchedulePowerDown"]]) {
  $(id).onchange = () => { settings[key] = $(id).checked; act("SaveSettings", settings); };
}

// Schedule, in the Target tab: the selected target with the web client's
// defaults (10 minutes of 10 s at ISO 1000).
$("#schedule-target").onclick = async () => {
  const r = selected >= 0 ? rows[selected] : null;
  if (!r) return showAlert("Schedule", "No target selected.");
  const v = await act("Visibility", r.RA, r.Dec);
  if (v?.NeverRises) return showAlert("Schedule", "This target does not rise from your location and cannot be scheduled.");
  const entries = [...draft(), { Name: r.Name, RA: r.RA, Dec: r.Dec, DurationSec: 600, ExposureSec: 10, ISO: 1000 }];
  await visibility();
  const all = [...vis, v], starts = scheduleStarts(entries, all, Date.now());
  const set = v?.Set && !v.Set.startsWith("0001-") ? Date.parse(v.Set) : NaN;
  let next = entries;
  // Up now but set before its turn: offer the visibility order.
  if (!v?.Circumpolar && set > Date.now() && set < starts.at(-1) &&
    await confirmBox("Reorder Tonight's Schedule", `${r.Name} sets before its turn in tonight's schedule. Reorder the schedule by visibility?`, "Reorder")) {
    next = byVisibility(all, Date.now()).map((i) => entries[i]);
  }
  if (await store(next)) $("#open-schedules").click();
};

// The draft follows the run, and the lists the run's end.
let wasRunning = false;
Events.On("status", (ev) => {
  if (ev.data.Key !== "TaskController/GetStatus") return;
  setTimeout(() => {
    if (!$("#schedules-dialog").open) return;
    if (running() || wasRunning !== running()) renderDraft();
    if (wasRunning !== running()) renderPrevious();
    wasRunning = running();
  });
});
Events.On("frame", (ev) => ev.data.Kind === "final" && $("#schedules-dialog").open && renderPrevious());
