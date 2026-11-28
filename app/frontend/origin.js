// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Origin settings (SettingsOriginAdvancedFragment): lighting, fans, autofocus,
// dew heater, calibration frames, logs, version and updates, polar alignment.
import { Browser, Events } from "/wails/runtime.js";
import { $, act, sliderLabels, choice, confirmBox, showAlert } from "./ui.js";
import { activityText, connected, lastOrigin, lastTask, lastTransfer, lastUpdate, locationOK, setLastOrigin, settings } from "./app.js";
import { darkMessage, polarOffset, polarText, updateButton, updateText } from "./targets.js";

// renderOrigin shows the settings the Origin reported, enabling each
// control as SettingsOriginAdvancedFragment does; cached, it asks nothing
// and redraws from the Origin's last statuses.
async function renderOrigin(cached = false) {
  const o = await act(cached ? "OriginSettingsCached" : "OriginSettings");
  if (!o) return;
  const f = $("#origin-form").elements;
  const show = (el, value, enabled) => {
    if (el.type === "checkbox") el.checked = !!value;
    else if (value != null) el.value = value;
    el.disabled = !enabled;
  };
  show(f.wedge, o.OnWedge, o.OnWedge != null && o.Known);
  // Power Off and Reboot need an idle Origin, as the app's updateMountSection
  // (its alert covers a state that changed meanwhile).
  $("#power-off").disabled = $("#reboot").disabled = !o.Idle;
  $("#reinit").disabled = !o.Idle;
  show(f.ledring, o.LedRing != null ? Math.round(o.LedRing * 100) : null, o.LedRing != null && o.Known);
  show(f.mountled, o.MountLed != null ? Math.round(o.MountLed * 100) : null, o.MountLed != null && o.Known);
  show(f.otafan, o.OtaFan, o.OtaFan != null && o.Known);
  show(f.cpufan, o.CpuFan, o.CpuFan != null && o.Known);
  show(f.tempaf, o.TempAutofocus, o.TempAutofocus != null && o.Known);
  show(f.threshold, o.TempThresholdC, o.Known && !!o.TempAutofocus && o.TempThresholdC != null);
  $("#threshold-label").textContent = o.TempThresholdC != null ? `${Math.trunc(o.TempThresholdC)} °C` : "";
  show(f.gotoaf, o.GotoAutofocus, o.GotoAutofocus != null && o.Known);
  for (const r of f.dewmode) { r.checked = r.value === o.DewMode; r.disabled = !(o.DewMode && o.Known); }
  show(f.aggression, o.DewAggression, o.Known && o.DewMode === "Auto");
  show(f.dewpower, Math.round(o.DewPower * 100), o.Known && o.DewMode === "Manual");
  show(f.giotto, o.ElPanelOn, o.ElPanelConnected && o.Known && o.Idle);
  show(f.giottolevel, o.ElPanelLevel ?? null, o.ElPanelConnected && o.ElPanelOn && o.Known && o.Idle);
  show(f.finalformat, o.FinalFormat || null, !!o.FinalFormat && o.Idle);
  $("#cooling-section").hidden = !o.HasCooling;
  f.cooling.checked = !!settings.Cooling;
  f.coolingtarget.value = settings.CoolingTargetC;
  $("#recalibrate").disabled = !(o.Recalibrating === false && o.Idle);
  $("#recalibrate").textContent = o.Recalibrating ? "Recalibration in Progress…" : "Recalibrate Environment Sensor";
  $("#polar-align").disabled = !(o.Idle && o.OnWedge);
  setLastOrigin(o);
  showCalibrationButtons();
  // Version & Updates.
  $("#info-model").textContent = o.Model || "[Unknown]";
  $("#info-version").textContent = o.Version || "[Unknown]";
  $("#info-serial").textContent = o.Serial || "[Unknown]";
  $("#info-channel").textContent = o.Channel || "[Unknown]";
  updates = (await act("Updates")) || { Channels: ["stable"] };
  showUpdate();
  show(f.autoupdates, o.AutoUpdates, o.AutoUpdates != null && o.Known);
  // "stable" and the public channels of Celestron's version list, and the
  // Origin's own channel.
  f.channel.replaceChildren(...[...new Set([...updates.Channels, o.Channel])].map((c) => new Option(c, c)));
  show(f.channel, o.Channel, o.Idle);
  $("#demo-mode").disabled = !o.Known;
  f.mosaicflatten.checked = !!settings.MosaicFlatten;
  f.autocrop.checked = !!settings.AutoCrop;
  f.postprocess.checked = !!settings.PostProcess;
  f.mosaicnormalize.checked = !!settings.MosaicNormalize;
  f.mosaicequalize.checked = !!settings.MosaicEqualize;
  showLogsButton();
  $("#info-app").textContent = `Nightglass, ver ${(await act("AppVersion")) || "?"}`;
  sliderLabels($("#origin-form"));
}
// "Check Again" is the app's pull to refresh: asks the Origin again (the
// settings' HasUpdateAvailable, without ForceCheck), or connects first.
$("#check-updates").onclick = async () => {
  if (connected) return renderOrigin();
  if (await locationOK()) act("ConnectOrigin");
};
// originStatus redraws the open settings as the Origin's statuses arrive
// (updateAllSections on TaskController and Environment statuses and on the
// settings' answers), at most every half second.
let originRedraw = 0;
const originKeys = new Set(["TaskController/GetStatus", "Environment/GetStatus", "DewHeater/GetStatus", "ElPanel/GetStatus",
  "Mount/GetMountConfig", "LedRing/GetBrightnessLevel", "LampController/GetBrightnessLevel", "Focuser/GetFocuserAdvancedSettings"]);
export function originStatus(key) {
  if (!originKeys.has(key) || !$("#origin-dialog").open || originRedraw) return;
  originRedraw = setTimeout(() => { originRedraw = 0; if ($("#origin-dialog").open) renderOrigin(true); }, 500);
}
// The calibration buttons say what is running, as the app's do.
export function showCalibrationButtons() {
  const idle = connected && lastTask.State === "IDLE";
  const darks = lastTask.State === "GENERATING_DARKS", flats = lastTask.State === "GENERATING_FLATS";
  $("#capture-darks").textContent = darks ? activityText : "Capture Dark Frames";
  $("#recapture-darks").textContent = darks ? "Generating Darks…" : "Recapture Complete Set of Dark Frames";
  $("#capture-flats").textContent = flats ? "Generating Flats…" : "Capture Flat Frames";
  for (const id of ["#capture-darks", "#recapture-darks", "#capture-flats", "#restore-darks", "#restore-flats"]) $(id).disabled = !idle;
  const f = $("#origin-form").elements;
  for (const el of [f.darkiso, f.darkexp, f.darkcount]) el.disabled = !idle;
}
// change runs a setter, then shows what the Origin now reports.
const change = async (method, ...args) => { await act(method, ...args); renderOrigin(); };
$("#origin-form").addEventListener("input", () => sliderLabels($("#origin-form")));
{
  const f = $("#origin-form").elements;
  f.wedge.onchange = () => change("SetWedge", f.wedge.checked);
  f.ledring.onchange = () => change("SetLedRing", f.ledring.value / 100);
  f.mountled.onchange = () => change("SetMountLed", f.mountled.value / 100);
  f.otafan.onchange = () => change("SetFan", "Telescope", f.otafan.checked);
  f.cpufan.onchange = () => change("SetFan", "CPU", f.cpufan.checked);
  f.tempaf.onchange = () => change("SetTempAutofocus", f.tempaf.checked, -1);
  f.threshold.oninput = () => ($("#threshold-label").textContent = `${f.threshold.value} °C`);
  f.threshold.onchange = () => change("SetTempAutofocus", true, Number(f.threshold.value));
  f.gotoaf.onchange = () => change("SetGotoAutofocus", f.gotoaf.checked);
  for (const r of f.dewmode) {
    r.onchange = () => (r.value === "Manual" ? change("SetDewManual", f.dewpower.value / 100) : change("SetDewAuto", Number(f.aggression.value)));
  }
  f.aggression.onchange = () => change("SetDewAuto", Number(f.aggression.value));
  f.dewpower.onchange = () => change("SetDewManual", f.dewpower.value / 100);
  f.giotto.onchange = () => change("SetFlatPanel", f.giotto.checked);
  f.giottolevel.onchange = () => change("SetFlatPanelLevel", Number(f.giottolevel.value));
  f.finalformat.onchange = () => change("SetFinalFormat", f.finalformat.value);
  // The cooler's choice is a preference (sent again on each connect), then sent now.
  const cool = async () => {
    const target = Number(f.coolingtarget.value);
    if (!(target >= -30 && target <= 30)) return renderOrigin();
    settings.Cooling = f.cooling.checked;
    settings.CoolingTargetC = target;
    await act("SaveSettings", settings);
    await act("SetCooling", settings.Cooling, target);
  };
  f.cooling.onchange = cool;
  f.coolingtarget.onchange = cool;
  // The app's mosaic composition options are its preferences, not the Origin's.
  for (const [el, key] of [[f.mosaicflatten, "MosaicFlatten"], [f.mosaicnormalize, "MosaicNormalize"], [f.mosaicequalize, "MosaicEqualize"], [f.autocrop, "AutoCrop"], [f.postprocess, "PostProcess"]]) {
    el.onchange = async () => { settings[key] = el.checked; await act("SaveSettings", settings); };
  }
}
// Re-initialize, from the settings as in the app: asked first.
$("#reinit").onclick = async () => {
  if (!(await confirmBox("Re-Initialize Telescope?", "Are you sure you want to re-initialize the telescope now? This can take several minutes.",
    "Re-Initialize Now"))) return;
  $("#origin-dialog").close();
  act("Reinitialize");
};
$("#reboot").onclick = async () => {
  if (!(await confirmBox("Reboot Origin?",
    "Origin will restart; Nightglass reconnects once it is back up (a few minutes).", "Reboot"))) return;
  $("#origin-dialog").close();
  act("Reboot");
};
$("#power-off").onclick = async () => {
  // Refused before asking, as the app's powerOffScopeNowButtonTapped.
  if (!connected || lastTask.State !== "IDLE") return showAlert("Origin Is Busy", "Origin cannot be powered down unless it is idle.");
  if (await confirmBox("Are you sure you want to Power Off Origin?",
    "To turn Origin back on, you will need to manually cycle the power switch.", "Power Off")) change("PowerOff");
};
$("#recalibrate").onclick = async () => {
  if (await confirmBox("Recalibrate Environment Sensor Now?",
    "Are you sure you want to recalibrate the environment sensor now?", "Recalibrate Now")) change("RecalibrateEnvironmentSensor");
};
$("#open-origin").onclick = async () => {
  // The dark frame fields start from the app's defaults.
  const f = $("#origin-form").elements, d = (await act("DarkDefaults")) || { ISO: 200, ExposureSec: 10, Count: 10 };
  const isos = (await act("SupportedISOs")) || [];
  f.darkiso.replaceChildren(...isos.map((v) => new Option(String(v), String(v))));
  f.darkiso.value = String(d.ISO);
  f.darkexp.value = d.ExposureSec.toFixed(1);
  f.darkcount.value = d.Count;
  await renderOrigin();
  $("#origin-dialog").showModal();
};
$("#origin-dialog").addEventListener("close", () => setLastOrigin(null));

// Z: dark and flat frames.
{
  const f = $("#origin-form").elements;
  f.darkcount.onchange = () => (f.darkcount.value = Math.min(100, Math.max(1, parseInt(f.darkcount.value, 10) || 0)));
  $("#capture-darks").onclick = async () => {
    const n = parseInt(f.darkcount.value, 10) || 10, exp = parseFloat(f.darkexp.value) || 10;
    if (await confirmBox("Capture New Dark Frames Now?", darkMessage(n, exp), "Capture Now")) change("CaptureDarks", Number(f.darkiso.value), exp, n);
  };
}
$("#recapture-darks").onclick = async () => {
  if (await confirmBox("Capture Dark Frames Now?",
    "This will delete all of your darks including any custom darks you have made, and generate a new set. This will take about 20 minutes.",
    "Capture Now")) change("RecaptureAllDarks");
};
// restored runs a restore and confirms it, as SettingsOriginAdvancedFragment
// does on the Origin's answer.
const restored = async (method, done) => {
  if ((await act(method)) !== false) showAlert(done, "");
  renderOrigin();
};
$("#restore-darks").onclick = async () => {
  if (await confirmBox("Restore Factory Darks?", "This replaces all of your dark frames with the set Origin left the factory with.",
    "Restore Darks")) restored("RestoreFactoryDarks", "Factory dark frames successfully restored.");
};
$("#restore-flats").onclick = async () => {
  if (await confirmBox("Restore Factory Flats?", "This replaces your flat frames with the set Origin left the factory with.",
    "Restore Flats")) restored("RestoreFactoryFlats", "Factory flat frames successfully restored.");
};
$("#capture-flats").onclick = async () => {
  if (await confirmBox("Capture New Flat Frames Now?", "Are you sure you want to capture new flat frames? This can take several minutes.",
    "Capture Now")) change("CaptureFlats");
};

// AA: automatic updates and the update channel.
{
  const f = $("#origin-form").elements;
  f.autoupdates.onchange = () => change("SetAutoUpdates", f.autoupdates.checked);
  f.channel.onchange = async () => {
    const c = f.channel.value;
    if (c !== "stable" && !(await confirmBox("Use Test Software?",
      'Using a software update channel other than "stable" may result in unexpected behaviour. Caution is advised.', "Use Test Software"))) {
      return renderOrigin();
    }
    change("SetUpdateChannel", c);
  };
}

// Origin logs, as the app's "Send Origin Logs to Celestron", saved here
// instead of uploaded; the button says how far it got.
let logsText = "";
Events.On("logs", (ev) => {
  logsText = ev.data.Text;
  showLogsButton();
});
$("#download-logs").onclick = () => act("DownloadLogs");
$("#save-app-log").onclick = () => act("SaveAppLog");

// Firmware update, as the app's Version & Updates: the status, the button,
// release notes, the Restart Warning, and the prompt when the Origin's
// version differs from its channel's.
let updates = { Channels: ["stable"] };
export function showUpdate() {
  const internet = lastOrigin?.Internet;
  $("#info-update").textContent = updateText(connected, internet, lastUpdate, updates.Differs, updates.Remote);
  const b = updateButton(connected, internet, lastUpdate, updates.Differs);
  $("#update-origin").textContent = b.text;
  $("#update-origin").disabled = !b.enabled;
}
$("#release-notes").onclick = () => showAlert("", updates.Notes || "");
$("#update-origin").onclick = async () => {
  if (!(await confirmBox("Restart Warning",
    "Updating Origin causes it to restart and require a reconnection. Once the update is complete, Nightglass will attempt to reconnect after 180 seconds.",
    "Start Update"))) return;
  if ((await act("UpdateOrigin")) !== false) renderOrigin();
};
Events.On("update", async () => {
  $("#origin-dialog").close();
  if (await choice("Origin Update Available",
    "A newer version of the software that runs your Origin telescope is available. Would you like to update now?",
    ["Show Updates", "Cancel"]) === 0) $("#open-origin").click();
});

// AD: demo mode.
$("#demo-mode").onclick = async () => {
  if (lastOrigin?.FakeInitialized) return showAlert("Demo Mode Active", "Demo Mode is already active.");
  if (lastTask.State !== "IDLE") return;
  if (await confirmBox("Activate Demo Mode?",
    "Activating Demo Mode will prevent Origin from correctly operating under the night sky and is intended for indoor testing and demonstration.",
    "Activate Demo Mode")) change("DemoMode");
};

// AC: polar alignment, from the settings or recommended after an
// equatorial Origin initializes.
async function startPolarAlign(now = false) {
  // From the recommendation the app sends it at once (PolarAlignNow).
  if ((await act(now ? "PolarAlignNow" : "PolarAlign")) === false) return;
  showPolar(lastTask);
  $("#polar-dialog").showModal();
}
$("#polar-align").onclick = async () => {
  if (await confirmBox("Polar Align Telescope?",
    "Are you sure you want to polar align the telescope now? This can take several minutes.", "Polar Align Now")) {
    $("#origin-dialog").close();
    startPolarAlign();
  }
};
Events.On("polar", () => $("#polar-rec-dialog").showModal());
$("#polar-rec-run").onclick = () => { $("#polar-rec-dialog").close(); startPolarAlign(true); };
$("#warning").onclick = () => {
  if (lastTask.State !== "POLAR_ALIGNING") return;
  showPolar(lastTask);
  $("#polar-dialog").showModal();
};
$("#polar-rec-noeq").onclick = () => { $("#polar-rec-dialog").close(); act("NotOnEquatorialMount"); };
// The app's alert first; its OK halts and closes the polar screen
// (PolarAlignFragment$1).
$("#polar-end").onclick = async () => {
  if (lastTask.State === "POLAR_ALIGNING") {
    await showAlert("Origin will now re-initialize", "An initialization is required after polar alignment.");
    act("EndPolarAlignment");
  }
  $("#polar-dialog").close();
};

// showPolar shows the polar screen's errors as text and as the error
// crosshair's offset (PolarAlignFragment.updateUI, PolarAlignCrosshairView).
export function showPolar(task) {
  $("#polar-errors").textContent = polarText(task);
  const o = polarOffset(task);
  $("#polar-mark").toggleAttribute("hidden", !o);
  if (o) $("#polar-mark").setAttribute("transform", `translate(${o.x * 30} ${-o.y * 30})`);
}

// showLogsButton is updateLoggingSection: while a USB copy runs (1–98 %) the
// button shows its progress and waits; else its own logs progress, idle only.
export function showLogsButton() {
  const pct = lastTransfer?.PercentageComplete;
  const copying = pct > 0 && pct < 99;
  $("#download-logs").textContent = copying ? `Preparing Logs: ${pct}% Complete` : logsText || "Download Origin Logs";
  $("#download-logs").disabled = copying || !!logsText || !(connected && lastTask.State === "IDLE");
}

// Saved logs can go to Celestron support by email, as the app's
// sendOriginLogsEmail (its subject; the file is attached by hand).
Events.On("logsaved", async (ev) => {
  const { Path, Serial } = ev.data;
  if ((await choice("Logs Saved", `The Origin's logs are saved in ${Path}. Email them to Celestron support?`, ["Email Support", "Close"])) !== 0) return;
  const q = new URLSearchParams({
    subject: `[Origin]: Log Files for SN# ${Serial || "N/A"}`,
    body: `My log files for Origin with Serial # ${Serial || "N/A"} are attached.`,
  });
  act("RevealSaved", Path.split(/[\\/]/).pop());
  Browser.OpenURL(`mailto:origin@celestron.com?${q.toString().replaceAll("+", "%20")}`);
});
