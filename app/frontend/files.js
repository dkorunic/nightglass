// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Origin file manager (RemoteFilesManagerFragment).
import { $, act, choice, confirmBox, openInTab, showAlert } from "./ui.js";
import { connected, lastDisk, lastTask, lastTransfer } from "./app.js";
import { fileStatus } from "./targets.js";

export function updateFilesStatus() {
  const st = fileStatus(lastDisk, lastTransfer);
  $("#files-status").textContent = st.text;
  $("#files-free").textContent = st.free;
  $("#files-copy-all").textContent = st.copyLabel;
  $("#files-delete-all").disabled = st.copying;
}
// busy refuses as RemoteFilesManagerFragment does, before any question:
// the Origin must be idle and not copying.
// copying is a copy under way, as the app counts it: 0 < pct < 100
// (RemoteFilesManagerFragment).
const copying = () => { const pct = lastTransfer?.PercentageComplete; return pct > 0 && pct < 100; };
function busy() {
  if (connected && lastTask.State === "IDLE" && !copying()) return false;
  showAlert("Origin Is Busy", "This feature can only be used while Origin is idle");
  return true;
}
export async function loadFiles() {
  const dirs = (await act("ImageDirectories")) || [];
  $("#files-list").replaceChildren(...dirs.map((d) => {
    const li = document.createElement("li");
    const name = document.createElement("span");
    name.textContent = d;
    const btn = (label, fn) => {
      const b = document.createElement("button");
      b.textContent = label;
      b.onclick = fn;
      return b;
    };
    li.append(name,
      btn("Download Stacked Master", () => act("DownloadDirectory", d)),
      // The whole folder, raw frames included, as a ZIP (Celestron's web app's download).
      btn("Download All Files (ZIP)", () => act("DownloadFolderZip", d)),
      btn("Copy Folder to USB", () => act("CopyDirectoryToUSB", d)),
      btn("Delete Image Directory", async () => {
        if (busy()) return;
        if (!(await confirmBox(`Are you sure you want to delete "${d}"?`, "This action cannot be undone.", "Delete"))) return;
        if ((await act("DeleteDirectory", d)) !== false) loadFiles();
      }));
    return li;
  }));
}
$("#open-files").onclick = () => {
  updateFilesStatus();
  loadFiles();
  act("FilesOpened"); // disk status and undownloaded runs, as the app's file manager
  openInTab($("#files-dialog"));
};
$("#files-refresh").onclick = loadFiles;
// Copy All cancels a copy under way; else it needs an idle Origin.
$("#files-copy-all").onclick = () => {
  if (!copying() && busy()) return;
  act("CopyAllToUSB");
};
// Delete All asks with Cancel as its first button, as the app's.
$("#files-delete-all").onclick = async () => {
  if (busy()) return;
  if (await choice("Are you sure you want to delete ALL images data from Origin?",
    "This action is highly destructive and cannot be undone.", ["Cancel", "Delete"]) !== 1) return;
  if ((await act("DeleteAllDirectories")) !== false) setTimeout(loadFiles, 500);
};
