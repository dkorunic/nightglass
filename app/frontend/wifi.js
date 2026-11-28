// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Wi-Fi tab of Celestron's web client (the app has none): the Origin's
// link, Direct Connect (its own access point) and the networks it knows or
// sees. Joining another network moves the Origin there, so the computer
// must follow it.
import { Events } from "/wails/runtime.js";
import { $, act, confirmBox, promptBox, showAlert } from "./ui.js";
import { connected } from "./app.js";
import { wifiBars, wifiMode, wifiSecurity } from "./targets.js";

let state = null;
const follow = (name) => showAlert("Join Network",
  `Join the "${name}" network from your computer to continue using Origin. Click OK after you've joined.`);

function render() {
  const nets = state?.Networks ?? [];
  $("#wifi-mode").textContent = state ? wifiMode(state.Mode, state.Internet) : "Disconnected";
  $("#wifi-ip").textContent = state?.IPAddress || "—";
  $("#wifi-force").checked = !!state?.ForceDirect;
  $("#wifi-5ghz").checked = !!state?.Use5GHz;
  $("#wifi-5ghz-row").hidden = !!state && !state.Allowed5GHz;
  const list = (el, rows, empty, item) => {
    el.replaceChildren(...(rows.length ? rows.map(item) : [Object.assign(document.createElement("li"), { className: "empty", textContent: state ? empty : "Disconnected" })]));
  };
  list($("#wifi-known"), nets.filter((n) => n.HasConfiguredPassword), "No known networks", (n) => {
    const li = row(n, () => join(n, false));
    if (n.HasWrongPassword) li.append(Object.assign(document.createElement("span"), { className: "bad", textContent: "[Bad Password]" }));
    const x = Object.assign(document.createElement("button"), { className: "forget", textContent: "✕", title: "Forget this network" });
    x.onclick = async (e) => {
      e.stopPropagation();
      if (await confirmBox("Forget Network", `Forget Wi-Fi network "${n.SSID}"?`, "Forget")) done(await act("ForgetWiFi", n.SSID));
    };
    li.append(x);
    return li;
  });
  list($("#wifi-visible"), nets.filter((n) => n.IsVisible && n.SSID), "No visible networks", (n) => {
    const li = row(n, () => join(n, true));
    li.classList.toggle("incompatible", n.AuthType === "INCOMPATIBLE");
    li.append(Object.assign(document.createElement("span"), { className: `bars b${wifiBars(n.SignalStrength)}`, title: `${n.SignalStrength} dBm` }),
      Object.assign(document.createElement("span"), { className: "sec", textContent: wifiSecurity(n.AuthType) }));
    return li;
  });
}
function row(n, onclick) {
  const li = document.createElement("li");
  li.append(Object.assign(document.createElement("span"), { className: "tick", textContent: n.IsConnected ? "✓" : "" }),
    Object.assign(document.createElement("span"), { className: "ssid", textContent: n.SSID }));
  li.onclick = connected ? onclick : null;
  return li;
}
// join asks as the web client does: a passphrase only for a secured network
// not known (or known with a wrong one).
async function join(n, visible) {
  if (n.IsConnected) return showAlert("", `Already connected to "${n.SSID}".`);
  if (visible && n.AuthType === "INCOMPATIBLE") {
    return showAlert("", "Origin is incompatible with WiFi 6 and WiFi 7 networks that only allow WPA3 authentication. Please choose another network.");
  }
  let password = null; // null: use what the Origin knows
  if (n.HasWrongPassword || (visible && !n.HasConfiguredPassword && n.AuthType !== "OPEN")) {
    password = await promptBox("Wi-Fi Password",
      n.HasWrongPassword ? "Incorrect password, please re-enter:" : `Please enter passphrase for Wi-Fi network "${n.SSID}":`, "Join");
    if (password == null) return;
  } else {
    if (!(await confirmBox("Join Network",
      `Join Wi-Fi network "${n.SSID}"? You will have to switch your computer's Wi-Fi network to continue using Origin.`, "Join"))) return;
    if (visible && !n.HasConfiguredPassword) password = ""; // an open network
  }
  if ((await act("JoinWiFi", n.SSID, password ?? "", password != null)) !== false) setTimeout(() => follow(n.SSID), 2000);
  refreshSoon();
}
const done = (ok) => ok !== false && refreshSoon();

export async function loadWiFi() {
  state = connected ? (await act("WiFi")) || null : null;
  render();
}
// The Origin's own reports refresh the list a little later, as the web
// client's (a join takes a while to show).
let soon = 0;
function refreshSoon() {
  clearTimeout(soon);
  soon = setTimeout(loadWiFi, 2000);
}
Events.On("status", (ev) => ev.data.Key === "Network/GetStatus" && connected && refreshSoon());
// After app.js has taken the new link (its listener runs after this one).
Events.On("conn", () => setTimeout(loadWiFi));
$(".tab[data-pane='cp-wifi']").addEventListener("click", () => connected && loadWiFi());

// Direct Connect: asked first, since the computer must then join it.
$("#wifi-force").onchange = async () => {
  const on = $("#wifi-force").checked;
  $("#wifi-force").checked = !on; // until confirmed and done
  if (!(await confirmBox("Force Direct Connect",
    `Are you sure you want to switch Direct Connect ${on ? "ON" : "OFF"}? You will have to switch your computer's Wi-Fi network to continue using Origin.`,
    "Proceed"))) return;
  if ((await act("SetForceDirectConnect", on)) === false) return;
  $("#wifi-force").checked = on;
  setTimeout(() => follow(on ? "Origin" : "your local Wi-Fi"), 2000);
  refreshSoon();
};
$("#wifi-5ghz").onchange = async () => done(await act("SetWiFi5GHz", $("#wifi-5ghz").checked));
$("#wifi-dc-password").onclick = async () => {
  const p = await promptBox("Direct Connect Password", "Enter new Direct Connect password:", "Set Password");
  if (p == null || (await act("SetDirectConnectPassword", p)) === false) return;
  if (state?.Mode === 5) setTimeout(() => follow("Origin"), 2000);
};
$("#wifi-forget-all").onclick = async () => {
  if (await confirmBox("Forget Networks", "Really forget all known networks?", "Forget All")) done(await act("ForgetWiFi", ""));
};
