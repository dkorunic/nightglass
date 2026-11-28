// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dkorunic/nightglass/origin"
)

// The Origin Telescope File Manager (RemoteFilesManagerFragment): the image
// folders kept on the Origin, re-downloaded, deleted or copied to a USB
// drive. Copy progress arrives as TaskController/FileTransferStatus.

// transferring is the app's currentlyTransferringFiles.
func (a *App) transferring() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.transferPct != nil && *a.transferPct > 0 && *a.transferPct < 100
}

// folderAction runs a per-folder action: idle and not copying, as the app's
// folder menu.
func (a *App) folderAction(f func(c *origin.Client) error) error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil || a.transferring() {
			return errBusyAlert
		}

		return f(c)
	})
}

// ImageDirectories lists the Origin's image folders.
func (a *App) ImageDirectories() ([]string, error) {
	c, err := a.cl()
	if err != nil {
		return nil, err
	}
	m, err := c.Send(context.Background(), "ImageServer", "GetListOfAvailableDirectories", nil)
	if err != nil {
		return nil, err
	}
	var v struct{ DirectoryList []string }
	if err := m.Decode(&v); err != nil {
		return nil, err
	}

	return slices.DeleteFunc(v.DirectoryList, func(d string) bool { return d == "" }), nil
}

// DownloadDirectory asks for a folder's stacked master ("Download Stacked
// Master"); it arrives, and is saved, as a final image.
func (a *App) DownloadDirectory(name string) error {
	return a.folderAction(func(c *origin.Client) error {
		m, err := c.Send(context.Background(), "ImageServer", "GetStackedMasterFromDirectory", map[string]any{"DirectoryName": name})
		if err != nil {
			return err
		}
		var v struct{ FileExists bool }
		if m.Decode(&v) != nil || !v.FileExists {
			return errors.New("alert:Final stacked master does not exist. Cannot download.")
		}

		return nil
	})
}

// DownloadFolderZip is Celestron's web app's folder download (not in the
// app): the Origin zips the folder, raw frames included
// (TaskController/ZipDirectory, its progress in the status as ZipInfo), and
// zipMessage saves the ZIP into SaveDir.
func (a *App) DownloadFolderZip(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("bad folder name %q", name)
	}

	return a.folderAction(func(c *origin.Client) error {
		a.mu.Lock()
		a.zipFolder = name // before asking: the answer may come at once
		a.mu.Unlock()
		if _, err := c.Send(context.Background(), "TaskController", "ZipDirectory", map[string]any{"DirectoryName": name}); err != nil {
			a.mu.Lock()
			a.zipFolder = ""
			a.mu.Unlock()

			return err
		}

		return nil
	})
}

// zipMessage follows a DownloadFolderZip: on ImageServer/ZipReady it
// downloads the ZIP into SaveDir, then deletes it on the Origin (DeleteFile
// takes its path below Images/), as the web app does. Another client's ZIP
// is left alone.
func (a *App) zipMessage(ctx context.Context, c *origin.Client, m origin.Message) {
	if m.Command != "ZipReady" || m.Module() != "ImageServer" {
		return
	}
	a.mu.Lock()
	name := a.zipFolder
	a.zipFolder = ""
	a.mu.Unlock()
	if name == "" {
		return
	}
	if m.ErrorCode != 0 {
		if !origin.Localized(m.ErrorCode) { // a known code the relay shows itself
			a.emit("alert", Alert{problemDownload, cmp.Or(m.ErrorMessage, "Could not create the ZIP file.")})
		}

		return
	}
	var zr struct{ DownloadLink string }
	if m.Decode(&zr) != nil || zr.DownloadLink == "" {
		return
	}
	a.emit("toast", Toast{toastInfo, "Downloading " + name + "…"})
	go func() {
		dir := a.Settings().SaveDir
		// The link comes from the telescope: only a plain file name is kept.
		p := filepath.Join(dir, safeName(path.Base("/"+zr.DownloadLink), safeName(name, "origin")+".zip"))
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			err = origin.FetchToFile(ctx, a.httpc, c.Host(), zr.DownloadLink, p)
		}
		_ = c.Post(ctx, "ImageServer", "DeleteFile", map[string]any{"FilePath": strings.TrimPrefix(zr.DownloadLink, "Images/")})
		if err != nil {
			a.emit("alert", Alert{problemDownload, "Unable to download " + name + ". Please try again."})

			return
		}
		a.emit("toast", Toast{toastInfo, "Saved " + p})
	}()
}

// DeleteDirectory deletes one folder ("Delete Image Directory").
func (a *App) DeleteDirectory(name string) error {
	return a.folderAction(func(c *origin.Client) error {
		_, err := c.Send(context.Background(), "ImageServer", "DeleteImageDirectory", map[string]any{"DirectoryName": name})

		return err
	})
}

// CopyDirectoryToUSB copies one folder to the USB drive ("Copy Folder to USB").
func (a *App) CopyDirectoryToUSB(name string) error {
	return a.folderAction(func(c *origin.Client) error {
		_, err := c.Send(context.Background(), "TaskController", "CopyDirectoryToFlashDrive", map[string]any{"DirectoryName": name})

		return err
	})
}

// DeleteAllDirectories deletes every folder, only while idle.
func (a *App) DeleteAllDirectories() error {
	return a.with(func(c *origin.Client) error {
		if idle(c) != nil {
			return errBusyAlert
		}
		_, err := c.Send(context.Background(), "ImageServer", "DeleteAllImageDirectories", nil)

		return err
	})
}

// CopyAllToUSB copies every folder to the USB drive; while a copy runs it
// cancels it instead ("Cancel File Copy").
func (a *App) CopyAllToUSB() error {
	return a.with(func(c *origin.Client) error {
		if a.transferring() {
			return c.HaltTasks(context.Background())
		}
		if idle(c) != nil {
			return errBusyAlert
		}
		_, err := c.Send(context.Background(), "TaskController", "CopyAllDirectoriesToFlashDrive", nil)

		return err
	})
}

// errBusyAlert is the file manager's "Origin Is Busy" alert.
var errBusyAlert = errors.New("alert:" + errBusy.Error())
