// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

// awakeWhy is what the OS shows for Nightglass's sleep inhibitor.
const awakeWhy = "Imaging with the Origin telescope"

// needAwake is when the app holds its wake lock (SkySafariActivityImaging
// $ImageDownloadAndProcess): while the Origin images and while a master
// image is requested, downloaded, processed or saved.
func (a *App) needAwake() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	busy := a.waitingFinal || a.dlFinal || a.dlStacked != nil || a.saving || a.processing != nil

	return busy || a.client != nil && a.client.Connected() && a.client.Imaging()
}

// keepAwake holds the sleep inhibitor the moment needs: the display's for
// the whole session with Keep Screen On (the app's PREVENT_SLEEP_KEY), else
// the system's while need is set. The caller holds actMu, which guards
// release and heldDisplay.
func (a *App) keepAwake(need bool) {
	display := a.Settings().KeepScreenOn
	want := need || display
	if a.release != nil && (!want || a.heldDisplay != display) {
		a.release()
		a.release = nil
	}
	if want && a.release == nil {
		why := awakeWhy
		if display {
			why = "Nightglass is open (Keep Screen On)"
		}
		a.release, a.heldDisplay = a.inhibit(why, display), display
	}
}

// Ready is the frontend's start: what holds for the whole session takes
// effect (Keep Screen On).
func (a *App) Ready() { a.refresh() }
