// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// A minimal Chrome DevTools Protocol driver for the GUI tests (e2e.mjs):
// headless Chrome, one page, evaluate JS in it. No dependencies beyond
// Node's own fetch and WebSocket.
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

// chromePath finds Chrome or Chromium: $CHROME, else the usual places.
function chromePath() {
  const candidates = [
    process.env.CHROME,
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser",
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  ];
  const p = candidates.find((c) => c && existsSync(c));
  if (!p) throw new Error("no Chrome found; set CHROME");
  return p;
}

// launch starts headless Chrome on url and returns a page handle.
export async function launch(url) {
  const profile = mkdtempSync(join(tmpdir(), "nightglass-e2e-"));
  const chrome = spawn(chromePath(), [
    "--headless=new", "--remote-debugging-port=0", `--user-data-dir=${profile}`,
    "--no-first-run", "--no-default-browser-check", "--window-size=1400,900", "about:blank",
  ], { stdio: "ignore" });
  // Chrome writes its port to DevToolsActivePort in the profile.
  let port;
  for (let i = 0; i < 100 && !port; i++) {
    await sleep(100);
    const f = join(profile, "DevToolsActivePort");
    if (existsSync(f)) port = readFileSync(f, "utf8").split("\n")[0];
  }
  if (!port) throw new Error("Chrome did not start");
  const target = await (await fetch(`http://127.0.0.1:${port}/json/new?${encodeURIComponent(url)}`, { method: "PUT" })).json();
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((ok, bad) => { ws.onopen = ok; ws.onerror = bad; });
  let id = 0;
  const pending = new Map();
  const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) {
      const { ok, bad } = pending.get(m.id);
      pending.delete(m.id);
      if (m.error) bad(new Error(m.error.message));
      else ok(m.result);
    } else if (m.method === "Runtime.exceptionThrown") {
      logs.push(m.params.exceptionDetails?.exception?.description ?? m.params.exceptionDetails?.text);
    } else if (m.method === "Runtime.consoleAPICalled" && m.params.type === "error") {
      logs.push(m.params.args.map((a) => a.value ?? a.description).join(" "));
    }
  };
  const send = (method, params = {}) => new Promise((ok, bad) => {
    pending.set(++id, { ok, bad });
    ws.send(JSON.stringify({ id, method, params }));
  });
  await send("Runtime.enable");
  const page = {
    errors: logs,
    // js evaluates the test's own expression in the test page
    // (Runtime.evaluate) and returns its value.
    async js(expr) {
      const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) throw new Error(`${expr}: ${r.exceptionDetails.exception?.description ?? r.exceptionDetails.text}`);
      return r.result.value;
    },
    // until polls expr until it is truthy, then returns its value.
    async until(expr, ms = 5000) {
      const end = Date.now() + ms;
      for (;;) {
        const v = await page.js(expr).catch(() => undefined);
        if (v) return v;
        if (Date.now() > end) throw new Error(`timed out waiting for: ${expr}`);
        await sleep(50);
      }
    },
    async close() {
      ws.close();
      chrome.kill();
      await sleep(200);
      rmSync(profile, { recursive: true, force: true });
    },
  };
  return page;
}
