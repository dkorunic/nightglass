// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// Download All Files: the Origin zips a folder (raw frames included), the
// ZIP is saved into SaveDir and then deleted on the Origin, as Celestron's
// web app does (TaskController/ZipDirectory, ImageServer/ZipReady and
// ImageServer/DeleteFile with the link's path below Images/).
func TestDownloadFolderZip(t *testing.T) {
	a, rec, s := connected(t)
	if err := a.DownloadFolderZip("M 42 2026"); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "TaskController", "ZipDirectory"); m["DirectoryName"] != "M 42 2026" {
		t.Errorf("ZipDirectory %v", m)
	}
	zip := []byte("PK\x03\x04 the folder")
	s.SetFile("Images/Astrophotography/M 42 2026.zip", zip)
	s.Notify("ImageServer", "ZipReady", fake.Msg{"DownloadLink": "Images/Astrophotography/M 42 2026.zip"})
	want := filepath.Join(a.Settings().SaveDir, "M_42_2026.zip")
	rec.wait(t, "toast", func(d any) bool { return d.(Toast).Message == "Saved "+want })
	if b, err := os.ReadFile(want); err != nil || string(b) != string(zip) {
		t.Errorf("saved %q, %v", b, err)
	}
	if m := lastSent(t, s, "ImageServer", "DeleteFile"); m["FilePath"] != "Astrophotography/M 42 2026.zip" {
		t.Errorf("DeleteFile %v", m)
	}
	// A ZipReady nobody here asked for (another client's) is left alone.
	s.Notify("ImageServer", "ZipReady", fake.Msg{"DownloadLink": "Images/Astrophotography/other.zip"})
	time.Sleep(200 * time.Millisecond)
	if n := len(received(s, "ImageServer", "DeleteFile")); n != 1 {
		t.Errorf("%d deletes: the unasked ZIP was touched", n)
	}
	// The Origin's refusal is shown in its own words.
	if err := a.DownloadFolderZip("empty"); err != nil {
		t.Fatal(err)
	}
	s.Notify("ImageServer", "ZipReady", fake.Msg{"ErrorCode": -1, "ErrorMessage": "This folder has no images to download."})
	rec.wait(t, "alert", func(d any) bool { return d.(Alert).Message == "This folder has no images to download." })
	for _, bad := range []string{"", "../x", "a/b"} {
		if a.DownloadFolderZip(bad) == nil {
			t.Errorf("folder %q accepted", bad)
		}
	}
}
