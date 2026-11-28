// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Optics tab of Celestron's web client (the app has none): the
// telescope's model or custom figures and its reducer. An Origin's optics
// are fixed and it refuses Optics, so there the tab stays hidden, as the
// web client hides it.
import { Events } from "/wails/runtime.js";
import { $, act, confirmBox, showPane } from "./ui.js";
import { connected } from "./app.js";

let custom = null; // the custom figures last reported, restored on picking Custom
let flip = null; // reported, sent back unchanged
const REDUCERS = ["1", "0.2", "0.63", "0.7", "2", "2.5"];

function show(cfg) {
  const model = $("#optics-model");
  model.value = [...model.options].some((o) => o.value === cfg.ModelName) ? cfg.ModelName : "Custom";
  $("#optics-design").value = cfg.Design || "RASA";
  $("#optics-focal").value = cfg.FocalLength?.toFixed(1) ?? "";
  $("#optics-aperture").value = cfg.Aperture?.toFixed(1) ?? "";
  const ratio = String(Number((cfg.ReducerRatio ?? 1).toFixed(2)));
  $("#optics-reducer").value = REDUCERS.includes(ratio) ? ratio : "custom";
  $("#optics-ratio").value = ratio;
  enable();
}
function enable() {
  const isCustom = $("#optics-model").value === "Custom";
  for (const id of ["#optics-design", "#optics-focal", "#optics-aperture"]) $(id).disabled = !connected || !isCustom;
  $("#optics-ratio").disabled = !connected || $("#optics-reducer").value !== "custom";
}
export async function loadOptics() {
  const st = connected ? await act("Optics") : null;
  const tab = $(".tab[data-pane='cp-optics']");
  tab.hidden = !st?.Available;
  if (tab.hidden) {
    if (tab.classList.contains("active")) showPane("cp-target");
    return;
  }
  $("#optics-model").replaceChildren(...st.Models.map((m) => new Option(m.Name, m.Key)), new Option("Custom", "Custom"));
  flip = st.Config.MirrorFlip ?? null;
  if (!st.Models.some((m) => m.Key === st.Config.ModelName)) custom = st.Config;
  show(st.Config);
}
// After app.js has taken the new link (its listener runs after this one).
Events.On("conn", () => setTimeout(loadOptics));
$(".tab[data-pane='cp-optics']").addEventListener("click", () => connected && loadOptics());

$("#optics-model").onchange = async () => {
  const m = $("#optics-model").value;
  if (m === "Custom") {
    if (custom) show({ ...custom, ModelName: "Custom" });
    return enable();
  }
  const cfg = await act("OpticsFor", m); // the model's figures, before choosing it
  if (cfg) show({ ...cfg, ModelName: m });
};
$("#optics-reducer").onchange = () => {
  const v = $("#optics-reducer").value;
  if (v !== "custom") $("#optics-ratio").value = v;
  enable();
};
$("#optics-apply").onclick = async () => {
  if (!(await confirmBox("Change Optics",
    "About to send a new optical configuration to Origin. You may need to re-initialize or generate a new flat. Change optics now?",
    "Change Optics"))) return loadOptics();
  const cfg = {
    ModelName: $("#optics-model").value, Design: $("#optics-design").value,
    FocalLength: Number($("#optics-focal").value), Aperture: Number($("#optics-aperture").value),
    ReducerRatio: Number($("#optics-ratio").value), MirrorFlip: flip,
  };
  if ((await act("SetOptics", cfg)) !== false) loadOptics();
};
