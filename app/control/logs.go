// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/dkorunic/nightglass/origin"
)

// problemDownload is the app's alert title for a failed download.
const problemDownload = "Problem with Download"

// Logs is the "logs" event: the logs button's text while logs are being
// requested, prepared or downloaded, "" once that is over.
type Logs struct {
	Text string
}

// DownloadLogs asks the Origin for its logs (System/GetLogs), as the app's
// "Send Origin Logs to Celestron", only while idle. The Origin answers with
// System/LogsReady; the file it names is saved into SaveDir instead of
// being uploaded to Celestron.
func (a *App) DownloadLogs() error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		a.setLogs("Requesting Logs…")
		if _, err := c.Send(context.Background(), "System", "GetLogs", nil); err != nil {
			a.setLogs("")

			return err
		}

		return nil
	})
}

// setLogs records and sends the logs button's text.
func (a *App) setLogs(text string) {
	a.mu.Lock()
	a.logs = text
	a.mu.Unlock()
	a.emit("logs", Logs{text})
}

// logsMessage follows a logs request: the Origin's preparation progress,
// then its LogsReady with the file to download.
func (a *App) logsMessage(ctx context.Context, c *origin.Client, m origin.Message) {
	a.mu.Lock()
	pending := a.logs != ""
	a.mu.Unlock()
	if !pending {
		return
	}
	switch m.Key() {
	case keyFileTransfer:
		var ft struct{ PercentageComplete *int }
		if m.Decode(&ft) == nil && ft.PercentageComplete != nil && *ft.PercentageComplete > 0 && *ft.PercentageComplete < 99 {
			a.setLogs(fmt.Sprintf("Preparing Logs: %d%% Complete", *ft.PercentageComplete))
		}
	case "System/LogsReady":
		var lr struct{ DownloadLink string }
		if m.Decode(&lr) != nil || lr.DownloadLink == "" {
			return
		}
		a.setLogs("Downloading Logs…")
		go func() {
			p, err := a.saveLogs(ctx, c, lr.DownloadLink)
			a.setLogs("")
			if err != nil {
				a.emit("alert", Alert{problemDownload, "Unable to download logs. Please try again."})

				return
			}
			a.emit("toast", Toast{toastInfo, "Saved " + p})
			var sn struct{ SerialNumber string }
			if m, ok := c.Last("FactoryCalibrationController/GetSerialNumber"); ok {
				_ = m.Decode(&sn)
			}
			a.emit("logsaved", LogsSaved{Path: p, Serial: sn.SerialNumber})
		}()
	}
}

// saveLogs downloads link into SaveDir under its own file name.
func (a *App) saveLogs(ctx context.Context, c *origin.Client, link string) (string, error) {
	b, err := origin.FetchFile(ctx, a.httpc, c.Host(), link)
	if err != nil {
		return "", err
	}
	dir := a.Settings().SaveDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// The link comes from the telescope: keep only a plain file name, so a
	// "..\" or a drive letter cannot write outside SaveDir, and never replace
	// a file already there (openNew numbers the name instead).
	name := safeName(path.Base("/"+link), "origin-logs")
	stem, ext, _ := strings.Cut(name, ".")
	if ext != "" {
		ext = "." + ext
	}

	return writeNew(filepath.Join(dir, stem), ext, b)
}

// AppLog keeps the newest MiB of Nightglass's own log; app/main.go points
// slog at it and SaveAppLog saves it (the app's "email app log",
// Utility.emailLogFile).
var AppLog = &ringLog{max: 1 << 20}

type ringLog struct {
	b   []byte
	max int
	mu  sync.Mutex
}

func (l *ringLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	if n := len(l.b) - l.max; n > 0 {
		l.b = slices.Clone(l.b[n:])
	}

	return len(p), nil
}

func (l *ringLog) bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.b)
}

// SaveAppLog saves Nightglass's own log into SaveDir and returns the path.
func (a *App) SaveAppLog() (string, error) {
	dir := a.Settings().SaveDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "Nightglass-log-"+a.now().Format("20060102-150405")+".txt")
	if err := os.WriteFile(p, AppLog.bytes(), 0o644); err != nil { //nolint:gosec // a log the user asked for, readable like any other
		return "", err
	}
	a.emit("toast", Toast{toastInfo, "Saved " + p})

	return p, nil
}

// LogsSaved is the "logsaved" event: the Origin's logs were saved at Path;
// the frontend offers the app's support email (sendOriginLogsEmail).
type LogsSaved struct {
	Path, Serial string
}
