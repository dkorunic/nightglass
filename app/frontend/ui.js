// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Helpers every module uses: Go calls, the DOM, toasts and the app's
// alert boxes. It imports nothing of the app, so it is ready before any
// feature module runs.
import { Call } from "/wails/runtime.js";

const SVC = "github.com/dkorunic/nightglass/app/control.App.";
export const call = (method, ...args) => Call.ByName(SVC + method, ...args);
export const $ = (sel) => document.querySelector(sel);
export const $$ = (sel) => [...document.querySelectorAll(sel)];


export function toast(level, message) {
  const el = document.createElement("div");
  el.className = `toast ${level}`;
  el.textContent = message;
  $("#toasts").append(el);
  setTimeout(() => el.remove(), 6000);
}

// showAlert and confirmBox are the app's alert and OK/Cancel boxes;
// confirmBox resolves true for OK. Alerts queue, as the app's dialogs
// stack: the next one shows when the current one closes.
const alerts = [];
let shownAlert = null; // the one on screen, so a flash's timer closes only itself
function nextAlert() {
  const d = $("#alert-dialog");
  if (d.open || !alerts.length) return;
  const a = (shownAlert = alerts.shift());
  const { title, text, ms } = a;
  $("#alert-title").textContent = title;
  $("#alert-text").textContent = text;
  d.classList.toggle("flash", !!ms);
  d.showModal();
  if (ms) setTimeout(() => shownAlert === a && d.close(), ms);
}
$("#alert-dialog").addEventListener("close", () => {
  $("#alert-dialog").classList.remove("flash");
  shownAlert?.done?.();
  nextAlert();
});
// showAlert queues an alert; its promise resolves once it is dismissed.
export function showAlert(title, text) {
  return new Promise((done) => {
    alerts.push({ title, text, done });
    nextAlert();
  });
}
// flashAlert is the app's showAlertWithoutButtons: text only, closing by
// itself after 3 s.
export function flashAlert(text, ms = 3000) {
  alerts.push({ title: "", text, ms });
  nextAlert();
}
export function confirmBox(title, text, ok, cancel = "Cancel") {
  return new Promise((resolve) => {
    const d = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    $("#confirm-ok").textContent = ok;
    $("#confirm-cancel").textContent = cancel;
    const done = (v) => { d.close(); resolve(v); };
    $("#confirm-ok").onclick = () => done(true);
    $("#confirm-cancel").onclick = () => done(false);
    d.oncancel = () => resolve(false);
    d.showModal();
  });
}

// choice is the app's alert with several buttons; it resolves the index of
// the one clicked, or -1 when dismissed.
export function choice(title, text, buttons) {
  return new Promise((resolve) => {
    const d = $("#choice-dialog");
    $("#choice-title").textContent = title;
    $("#choice-text").textContent = text;
    $("#choice-buttons").replaceChildren(...buttons.map((label, i) => {
      const b = document.createElement("button");
      b.textContent = label;
      b.onclick = () => { d.close(); resolve(i); };
      return b;
    }));
    d.oncancel = () => resolve(-1);
    d.showModal();
  });
}

// act runs a Go call and shows its error instead of throwing; it returns
// false on error, else the result (null for methods without one).
export async function act(method, ...args) {
  try {
    return await call(method, ...args);
  } catch (e) {
    const m = String(e?.message ?? e);
    if (m.startsWith("shown:")) return false; // already shown by Go, or one the app shows nothing for
    if (m.startsWith("flash:")) {
      // The app's alert without buttons; "flash:2500:text" stays 2.5 s.
      const [, ms, text] = /^flash:(?:(\d+):)?(.*)$/s.exec(m);
      flashAlert(text, Number(ms) || 3000);
    } else if (m.startsWith("alert:")) {
      // "alert:Title: message" is a refusal the app shows in an alert box.
      const [title, ...rest] = m.slice(6).split(": ");
      showAlert(title, rest.join(": "));
    } else toast("error", m.replace(/^origin: \S+ failed: /, "Origin Error: "));
    return false;
  }
}

// sliderLabels shows each range's value next to it (the app's
// onProgressChanged labels): <span data-val="name">, "%" with data-pct.
export function sliderLabels(form) {
  for (const sp of form.querySelectorAll("[data-val]")) {
    const el = form.elements[sp.dataset.val];
    if (el) sp.textContent = `${el.value}${sp.hasAttribute("data-pct") ? "%" : ""}`;
  }
}
