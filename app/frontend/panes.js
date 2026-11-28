// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Mount and Focuser tabs' controls of Celestron's web client beyond
// the app's: Goto Target and Goto Coords beside the slew pad, Recenter,
// sidereal tracking, the slew limits and dithering (Nightglass's settings),
// and the focuser's Move To, rate, fine or rough autofocus and calibration.
import { Events } from "/wails/runtime.js";
import { $, act, confirmBox, showAlert, showPane } from "./ui.js";
import { connected, setSettings, settings } from "./app.js";

// save stores changed settings and takes Go's copy back (it may refuse).
async function save() {
  if ((await act("SaveSettings", settings)) === false) return render();
  setSettings((await act("Settings")) || settings);
  render();
}
function render() {
  const min = settings.SlewLimitMinDeg ?? 0, max = settings.SlewLimitMaxDeg || 90;
  $("#slew-min").value = min;
  $("#slew-max").value = max;
  $("#slew-min-val").textContent = `${min}°`;
  $("#slew-max-val").textContent = `${max}°`;
  $("#dither").checked = !!settings.Dither;
  $("#dither-interval").value = settings.DitherInterval || 5;
  $("#dither-distance").value = settings.DitherDistance || 10;
  $("#dither-distance-val").textContent = `${$("#dither-distance").value} px`;
  // The dither settings work only with dithering on (enableDitherSubOptions).
  $("#dither-interval").disabled = $("#dither-distance").disabled = !settings.Dither;
  for (const r of document.querySelectorAll("input[name='af-range']")) r.checked = r.value === (settings.RoughFocus ? "rough" : "fine");
}
setTimeout(render, 500); // once app.js has the settings
for (const id of ["#mount", "#focuser"]) $(`.tab[data-pane='cp-${id.slice(1)}']`).addEventListener("click", render);

// ---- Mount ----
// Goto Target is the Target tab's Go to; Goto Coords types coordinates there.
$("#mnt-goto").onclick = () => $("#goto").click();
new MutationObserver(() => ($("#mnt-goto").disabled = $("#goto").disabled)).observe($("#goto"), { attributes: true, attributeFilter: ["disabled"] });
$("#mnt-goto-coords").onclick = () => { showPane("cp-target"); $("#ra").focus(); };
$("#recenter").onclick = () => {
  showPane("mp-liveview");
  if (!$("#reframe").disabled) $("#reframe").click();
};
$("#tracking").onchange = () => act("SetTracking", $("#tracking").checked);
// The limits keep min below max, as the web client's sliders.
$("#slew-min").oninput = () => {
  if (+$("#slew-min").value >= +$("#slew-max").value) $("#slew-max").value = +$("#slew-min").value + 1;
  $("#slew-min-val").textContent = `${$("#slew-min").value}°`;
  $("#slew-max-val").textContent = `${$("#slew-max").value}°`;
};
$("#slew-max").oninput = () => {
  if (+$("#slew-max").value <= +$("#slew-min").value) $("#slew-min").value = +$("#slew-max").value - 1;
  $("#slew-min").oninput();
};
for (const id of ["#slew-min", "#slew-max"]) {
  $(id).onchange = () => {
    settings.SlewLimitMinDeg = Number($("#slew-min").value);
    settings.SlewLimitMaxDeg = Number($("#slew-max").value);
    save();
  };
}
$("#dither").onchange = () => { settings.Dither = $("#dither").checked; save(); };
$("#dither-interval").onchange = () => {
  settings.DitherInterval = Math.min(Math.max(parseInt($("#dither-interval").value, 10) || 5, 1), 10);
  save();
};
$("#dither-distance").oninput = () => ($("#dither-distance-val").textContent = `${$("#dither-distance").value} px`);
$("#dither-distance").onchange = () => { settings.DitherDistance = Number($("#dither-distance").value); save(); };

// ---- Focuser ----
let limits = { Min: 1000, Max: 35000 };
$("#focus-moveto").onclick = () => {
  const v = parseInt($("#focus-pos").value, 10);
  if (Number.isNaN(v) || v < limits.Min || v > limits.Max) {
    return showAlert("", `Please enter a position from ${limits.Min} to ${limits.Max}.`);
  }
  act("FocusTo", v);
};
// The rate sets how fast Move In and Move Out run.
$("#focus-rate").oninput = () => {
  const r = Number($("#focus-rate").value);
  $("#focus-rate-val").textContent = r;
  for (const b of document.querySelectorAll("[data-jog]")) b.dataset.jog = String(Math.sign(Number(b.dataset.jog)) * r);
};
for (const r of document.querySelectorAll("input[name='af-range']")) {
  r.onchange = () => { settings.RoughFocus = r.value === "rough"; save(); };
}
$("#calibrate-focuser").onclick = async () => {
  if (await confirmBox("Calibrate Focuser", "Are you sure you want to calibrate your focuser? This will take several minutes.", "Calibrate")) {
    act("CalibrateFocuser");
  }
};

Events.On("status", (ev) => {
  const { Key: k, Fields: f } = ev.data;
  if (k === "Mount/GetStatus" && f.IsTracking != null) {
    $("#tracking").checked = !!f.IsTracking;
    $("#tracking-state").textContent = f.IsTracking ? "(ON)" : "(OFF)";
  }
  if (k === "Focuser/GetStatus") {
    $("#st-fvel").textContent = f.Velocity == null ? "-" : `${f.Velocity >= 0 ? "+" : ""}${f.Velocity.toFixed(1)}`;
    if (f.Position != null && document.activeElement !== $("#focus-pos")) $("#focus-pos").value = Math.round(f.Position);
  }
  if (k === "Focuser/GetPositionLimits" && f.Min != null && f.Max != null) {
    limits = { Min: f.Min, Max: f.Max };
    $("#st-fmin").textContent = f.Min;
    $("#st-fmax").textContent = f.Max;
  }
});
Events.On("conn", () => setTimeout(() => connected || ($("#tracking-state").textContent = "(OFF)")));
