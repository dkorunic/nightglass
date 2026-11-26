// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// remoteURL is where the app learns the Origin software versions per update
// channel (SkySafariAppOrigin.queryRemoteCelestronOriginVersion): Celestron's
// public, unauthenticated list. Tests replace it.
var remoteURL = "https://origin-update-manager.celestron.com/channels"

// remoteChannel is one channel of that list.
type remoteChannel struct {
	Version struct {
		Version     string `json:"version"`
		Description string `json:"description"` // the release notes
	} `json:"version"`
	Public bool `json:"public"`
}

// CheckRemoteVersions fetches the version list, as the app does when it
// starts, and keeps it; a failure leaves the copy from before.
func (a *App) CheckRemoteVersions() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK || !json.Valid(b) {
		return
	}
	a.update(func(s *Settings) { s.RemoteVersions = string(b) })
}

// remoteFor is a channel's version and release notes, "" when unknown.
func (a *App) remoteFor(channel string) (version, notes string) {
	var all map[string]remoteChannel
	if channel == "" || json.Unmarshal([]byte(a.Settings().RemoteVersions), &all) != nil {
		return "", ""
	}
	ch := all[channel]

	return ch.Version.Version, ch.Version.Description
}

// defaultChannel is the Origin's update channel unless set otherwise.
const defaultChannel = "stable"

// publicChannels is the app's channel list: "stable", then the list's other
// public channels in its order.
func publicChannels(raw string) []string {
	out := []string{defaultChannel}
	dec := json.NewDecoder(strings.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return out
		}
		name, _ := t.(string)
		var ch remoteChannel
		if dec.Decode(&ch) != nil {
			return out
		}
		if ch.Public && name != defaultChannel {
			out = append(out, name)
		}
	}

	return out
}

// updateDiffers is isNewerVersionOfOriginCoreAvailableForUpdate: any
// difference from the channel's version counts, not only a newer one.
func updateDiffers(local, remote string) bool {
	if local == "" || remote == "" {
		return false
	}

	return strings.ReplaceAll(local, "\n", "") != strings.ReplaceAll(remote, "\n", "")
}

// UpdateInfo is the Version & Updates section's update part.
type UpdateInfo struct {
	Remote   string   // the channel's version, "" when unknown
	Notes    string   // its release notes
	Channels []string // the channels offered
	Differs  bool     // the Origin's version is not the channel's
}

// versionAndChannel are the Origin's cached version Number and channel.
func versionAndChannel(c *origin.Client) (string, string) {
	var v struct{ Number string }
	var ch struct{ Value string }
	if m, ok := c.Last("System/GetVersion"); ok {
		_ = m.Decode(&v)
	}
	if m, ok := c.Last("System/GetUpdateChannel"); ok {
		_ = m.Decode(&ch)
	}

	return v.Number, ch.Value
}

// Updates returns the update part of Version & Updates.
func (a *App) Updates() UpdateInfo {
	info := UpdateInfo{Channels: publicChannels(a.Settings().RemoteVersions)}
	c, err := a.cl()
	if err != nil {
		return info
	}
	local, channel := versionAndChannel(c)
	info.Remote, info.Notes = a.remoteFor(channel)
	info.Differs = updateDiffers(local, info.Remote)

	return info
}

// offerUpdate is the "Origin Update Available" prompt ("update" event),
// when the Origin's version differs from its channel's.
func (a *App) offerUpdate(c *origin.Client) {
	local, channel := versionAndChannel(c)
	if remote, _ := a.remoteFor(channel); updateDiffers(local, remote) {
		a.emit("update", struct{ Version string }{remote})
	}
}

// offerUpdateOnConnect waits for the Origin's version and channel, then
// offers an update as the app does on every connect.
func (a *App) offerUpdateOnConnect(c *origin.Client) {
	for range 100 {
		if _, ok := c.Last("System/GetVersion"); ok {
			if _, ok := c.Last("System/GetUpdateChannel"); ok {
				a.offerUpdate(c)

				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// UpdateOrigin is "Download And Update Origin" (the frontend shows the
// app's Restart Warning first): System/Update {"Reboot": true}; the Origin
// downloads and installs the software itself, then restarts.
func (a *App) UpdateOrigin() error {
	return a.with(func(c *origin.Client) error {
		var us struct{ Status string }
		if m, ok := c.Last("System/UpdateStatus"); ok {
			_ = m.Decode(&us)
		}
		if us.Status == "UPDATE_DOWNLOADING" || us.Status == "UPDATE_INSTALLING" {
			return errors.New("an update is already in progress")
		}
		var net struct{ Value *bool }
		if m, ok := c.Last("Network/HasInternetConnection"); ok {
			_ = m.Decode(&net)
		}
		if net.Value == nil || !*net.Value {
			return errors.New("[Unknown]: No Internet Access")
		}
		if !a.Updates().Differs {
			return errors.New("No Updates Available")
		}
		_, err := c.Send(context.Background(), "System", "Update", map[string]any{"Reboot": true})

		return err
	})
}
