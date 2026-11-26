// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"strings"
	"testing"

	"github.com/dkorunic/nightglass/origin/fake"
)

// Spec §9.21.

// The open Origin settings redraw from what the Origin last reported,
// asking it nothing (updateAllSections).
func TestOriginSettingsCachedAsksNothing(t *testing.T) {
	a, _, s := connected(t)
	if _, err := a.OriginSettings(); err != nil {
		t.Fatal(err)
	}
	n := len(received(s, "LedRing", "GetBrightnessLevel")) // one of the settings' queries
	o, err := a.OriginSettingsCached()
	if err != nil || !o.Known {
		t.Fatalf("cached settings %+v %v", o, err)
	}
	if got := len(received(s, "LedRing", "GetBrightnessLevel")); got != n {
		t.Errorf("asked again for a cached read: %d → %d", n, got)
	}
}

// Disconnected, a manual exposure is held to the last limit the Origin
// allowed and the app's 1 s floor (Cached.Origin.MaxExposureTime).
func TestExposureLimitedOffline(t *testing.T) {
	a, _ := newApp(t)
	st := a.Settings()
	if st.MaxExposureSec != 30 {
		t.Errorf("default limit %v, want 30", st.MaxExposureSec)
	}
	st.ExposureSec, st.SnapshotExposureSec = 120, 90
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := a.Settings(); got.ExposureSec != 30 || got.SnapshotExposureSec != 30 {
		t.Errorf("offline exposures %v/%v, want 30/30", got.ExposureSec, got.SnapshotExposureSec)
	}
	st.ExposureSec = 0.2
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := a.Settings(); got.ExposureSec != 1 {
		t.Errorf("offline exposure %v, want the 1 s floor", got.ExposureSec)
	}
}

// A failed delete is the app's alert, with its capitalisation.
func TestDeleteFailureAlert(t *testing.T) {
	a, _ := newApp(t)
	if err := a.DeleteImages([]string{"gone.png"}); err == nil || !strings.HasPrefix(err.Error(), "alert:Unable to Delete Image: ") {
		t.Errorf("one image: %v", err)
	}
	if err := a.DeleteImages([]string{"gone.png", "lost.png"}); err == nil || !strings.HasPrefix(err.Error(), "alert:Unable to delete Images: ") {
		t.Errorf("several images: %v", err)
	}
}

// A saved image's record carries its run's ID (ImageUniqueID).
func TestImageRecordUuid(t *testing.T) {
	a, rec, s := connected(t)
	s.SetFile("final.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "M 42", "Uuid": "run-7"})
	saved := rec.wait(t, "saved", func(any) bool { return true }).(Saved)
	if r := a.Settings().Images[saved.Name]; r.Uuid != "run-7" {
		t.Errorf("record %+v", r)
	}
}
