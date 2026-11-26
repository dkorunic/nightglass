// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// The Quick Start.
import { $ } from "./ui.js";

// The app's Quick Start, shown during the first three launches and from
// About; the pages are Nightglass's own.
const QUICK_START = [
  ["Welcome to Nightglass", "Nightglass runs your Celestron Origin from this computer: it connects, initializes, points, focuses and images, and keeps your final images in your save folder. These pages take you to your first image."],
  ["Switch On", "Set Origin up outdoors on firm, level ground with an open view of the sky, then switch it on. It needs about a minute to start."],
  ["Connect", "Join this computer to Origin's own Wi-Fi network (its name starts with “Origin-”), or to the network Origin is on. Click Connect…, then Search network, or type Origin's address."],
  ["Your Location", "Open Settings and enter your latitude, longitude and time zone. Origin needs them to know where it is under the sky."],
  ["Initialization", "Click Initialize & align. Origin photographs the sky to learn where it points and focuses itself; this takes a few minutes."],
  ["Ready to Image", "Pick a target, click Go to, then Start Imaging. Finish imaging saves the final image to your save folder; the Gallery shows, edits and shares it."],
];
let qsPage = 0;
export function showQuickStart(page = 0) {
  qsPage = page;
  const [title, text] = QUICK_START[qsPage];
  $("#qs-title").textContent = title;
  $("#qs-text").textContent = text;
  $("#qs-page").textContent = `${qsPage + 1} of ${QUICK_START.length}`;
  $("#qs-back").disabled = qsPage === 0;
  $("#qs-next").disabled = qsPage === QUICK_START.length - 1;
  if (!$("#quickstart-dialog").open) $("#quickstart-dialog").showModal();
}
$("#qs-back").onclick = () => showQuickStart(qsPage - 1);
$("#qs-next").onclick = () => showQuickStart(qsPage + 1);
$("#about-quickstart").onclick = () => { $("#about-dialog").close(); showQuickStart(); };
