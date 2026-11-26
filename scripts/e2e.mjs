// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// GUI tests: the frontend in headless Chrome, served by the server-mode
// build (dist/app-server) against origin-fake. scripts/e2e.sh starts them
// and passes their addresses as APP and FAKE.
import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { launch } from "./cdp.mjs";

const APP = process.env.APP ?? "127.0.0.1:18091";
const FAKE = process.env.FAKE ?? "127.0.0.1:18090";
let p;

// q is document.querySelector in the page, as an expression.
const q = (sel) => `document.querySelector(${JSON.stringify(sel)})`;
const click = (sel) => p.js(`${q(sel)}.click()`);
const text = (sel) => p.js(`${q(sel)}.textContent`);
// choose clicks a button of the app's choice dialog by its label.
const choose = async (label) => {
  await p.until(`${q("#choice-dialog")}.open`);
  await p.js(`[...document.querySelectorAll("#choice-buttons button")].find((b) => b.textContent === ${JSON.stringify(label)}).click()`);
};
// confirm clicks the confirm dialog's OK.
const confirm = async () => {
  await p.until(`${q("#confirm-dialog")}.open`);
  await click("#confirm-ok");
};

before(async () => {
  p = await launch(`http://${APP}/`);
  await p.until(`${q("#activity")} && document.title === "Nightglass"`);
});
after(async () => {
  await p?.close();
});

test("Quick Start opens on the first launch and pages through", async () => {
  await p.until(`${q("#quickstart-dialog")}.open`, 10000);
  assert.equal(await text("#qs-title"), "Welcome to Nightglass");
  for (let i = 0; i < 5; i++) await click("#qs-next");
  assert.equal(await text("#qs-page"), "6 of 6");
  assert.equal(await p.js(`${q("#qs-next")}.disabled`), true);
  await p.js(`${q("#quickstart-dialog")}.close()`);
});

test("no location: connecting is refused with the app's offer", async () => {
  await p.js(`${q("#connect-dialog")}.open || ${q("#open-connect")}.click()`);
  await p.js(`${q("#host")}.value = ${JSON.stringify(FAKE)}`);
  await click("#connect");
  await p.until(`${q("#choice-title")}.textContent === "Precise Location Unavailable"`);
  await choose("Open Location Settings");
  await p.until(`${q("#settings-dialog")}.open`); // it asks Go for the ISO choices first
});

test("settings: a location is saved", async () => {
  await p.js(`(() => {
    const f = ${q("#settings-form")};
    f.LatDeg.value = "45.815"; f.LonDeg.value = "15.982"; f.TimeZone.value = "Europe/Zagreb";
    f.requestSubmit([...f.querySelectorAll("button")].find((b) => b.value === "save"));
  })()`);
  await p.until(`!${q("#settings-dialog")}.open`);
});

test("connect to the fake Origin: status, live view, labels", async () => {
  await p.js(`${q("#connect-dialog")}.open || ${q("#open-connect")}.click()`);
  await p.js(`${q("#host")}.value = ${JSON.stringify(FAKE)}`);
  await click("#connect");
  await p.until(`${q("#conn")}.textContent.startsWith("Connected to")`, 10000);
  await p.until(`${q("#st-battery")}.textContent.includes("V (~")`); // the battery estimate
  await p.until(`${q("#st-task")}.textContent === "Ready To Image"`);
  await p.until(`${q("#frame")}.getAttribute("src")?.startsWith("/frame/live")`, 10000);
  assert.equal(await p.js(`${q("#initialize")}.hidden`), true, "initialize hidden on an initialized Origin");
});

test("Origin settings: version and updates", async () => {
  await click("#open-origin");
  await p.until(`${q("#info-version")}.textContent === "1.1.4248"`);
  assert.equal(await text("#update-origin"), "[Unknown]: No Internet Access");
  // Restoring factory darks is confirmed once the Origin answers.
  await click("#restore-darks");
  await confirm();
  await p.until(`${q("#alert-dialog")}.open && ${q("#alert-title")}.textContent === "Factory dark frames successfully restored."`);
  await p.js(`${q("#alert-dialog")}.close()`);
  // Nightglass's own log is saved on request.
  await click("#save-app-log");
  await p.until(`[...document.querySelectorAll("#toasts .toast")].some((t) => /Saved .*Nightglass-log-/.test(t.textContent))`);
  await p.js(`${q("#origin-dialog")}.close()`);
});

test("imaging: a run, its stack, finish, the final image saved", async () => {
  await p.until(`!${q("#image")}.disabled`);
  await click("#image");
  await p.until(`${q("#cancel-imaging")}.disabled === false`, 5000);
  await p.until(`!${q("#finish-imaging")}.disabled`, 15000); // after the first stacked frame
  await click("#finish-imaging");
  await p.until(`[...document.querySelectorAll("#toasts .toast")].some((t) => t.textContent.startsWith("Saved "))`, 15000);
  // The saved image opens with what it is of, and live view returns.
  await p.until(`${q("#view-dialog")}.open && /Size\\d+x\\d+/.test(${q("#view-info")}.textContent)`, 10000);
  // Change Target… names it after another object.
  await p.js(`[...document.querySelectorAll("#view-btns button")].find((b) => b.textContent === "Change Target…").click()`);
  await p.until(`${q("#target-pick-dialog")}.open && document.querySelectorAll("#target-pick-list li").length > 0`, 10000);
  await p.js(`(() => { const s = document.querySelector("#target-pick-search"); s.value = "Andromeda"; s.dispatchEvent(new Event("input")); })()`);
  await p.until(`[...document.querySelectorAll("#target-pick-list li")].some((li) => li.textContent.startsWith("M 31 "))`);
  await p.js(`[...document.querySelectorAll("#target-pick-list li")].find((li) => li.textContent.startsWith("M 31 ")).click()`);
  await p.until(`!${q("#target-pick-dialog")}.open && /ObjectM 31Date/.test(${q("#view-info")}.textContent)`);
  await p.js(`${q("#view-dialog")}.close()`);
});

test("tonight: the Sun & Moon card", async () => {
  await click("#open-tonight");
  await p.until(`${q("#tonight-dialog")}.open && document.querySelectorAll("#tonight-sun dd").length === 4`);
  assert.match(await text("#tonight-moon-phase"), /Moon|Crescent|Quarter|Gibbous/);
  assert.match(await text("#tonight-moon-text"), /^\d+% illuminated, \d+\.\d\d days old$/);
  await p.js(`${q("#tonight-dialog")}.close()`);
});

test("gallery: the saved image, its editor", async () => {
  await click("#open-gallery");
  await p.until(`document.querySelectorAll("#gallery-grid .gcard").length > 0`);
  // The image just saved is among the Recent ones (the last 12 hours).
  await click("#gallery-recent");
  await p.until(`${q("#gallery-recent")}.classList.contains("on") && document.querySelectorAll("#gallery-grid .gcard").length > 0`);
  await click("#gallery-all");
  await p.js(`[...document.querySelectorAll("#gallery-grid .gcard button")].find((b) => b.textContent === "Edit").click()`);
  await p.until(`${q("#edit-dialog")}.open && ${q("#edit-canvas")}.width > 0`, 10000);
  await p.js(`(() => { const s = document.querySelector('#edit-sliders input[name=contrast]'); s.value = 70; s.dispatchEvent(new Event("input")); })()`);
  await p.until(`!${q("#edit-overwrite")}.disabled`);
  await click("#edit-overwrite");
  await choose("Overwrite");
  await p.until(`[...document.querySelectorAll("#toasts .toast")].some((t) => t.textContent === "Image successfully overwritten")`);
  await p.js(`${q("#gallery-dialog")}.close()`);
});

test("file manager lists the run's folder", async () => {
  await click("#open-files");
  await p.until(`document.querySelectorAll("#files-list li").length > 0`);
  await p.js(`${q("#files-dialog")}.close()`);
});

test("mosaic: plan, begin, listed, its tiles, cancelled", async () => {
  await p.js(`document.querySelector("#rows .trow").click()`);
  await p.until(`!${q("#mosaic")}.disabled`);
  await click("#mosaic");
  await p.until(`${q("#mosaic-dialog")}.open && ${q("#mosaic-grid")}.textContent.includes("tile")`);
  await p.js(`(() => { const f = ${q("#mosaic-form")}; f.width.value = 60; f.height.value = 30; f.width.dispatchEvent(new Event("input")); })()`);
  await p.until(`${q("#mosaic-grid")}.textContent === "2 × 1 = 2 tile(s)"`);
  await click("#mosaic-begin");
  await p.until(`!${q("#mosaic-dialog")}.open`);
  await p.until(`${q("#cancel-imaging")}.textContent === "Cancel Mosaic"`, 10000);
  await click("#open-mosaics");
  await p.until(`document.querySelectorAll("#mosaics-list li").length > 0`);
  await p.js(`document.querySelector("#mosaics-list li").click()`);
  await p.until(`${q("#mosaic-capture-dialog")}.open && document.querySelectorAll("#mc-tiles .mtile").length === 2`, 10000);
  await p.js(`${q("#mosaic-capture-dialog")}.close()`);
  await click("#cancel-imaging");
  await choose("End Mosaic Capture");
  await choose("End Capture Now");
  await p.until(`${q("#cancel-imaging")}.textContent === "Cancel imaging"`, 10000);
});

test("no page errors", () => {
  assert.deepEqual(p.errors, []);
});
