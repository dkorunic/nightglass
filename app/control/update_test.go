// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dkorunic/nightglass/origin/fake"
)

const channelsJSON = `{"stable":{"public":true,"version":{"version":"1.5.6240","url":"x","description":"* fixes"}},
"beta":{"public":true,"version":{"version":"1.5.6286","url":"x","description":"* beta"}},
"alpha":{"public":false,"version":{"version":"1.5.6292","url":"x","description":""}}}`

// serveChannels points remoteURL at a stand-in for Celestron's list.
func serveChannels(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := remoteURL
	remoteURL = srv.URL
	t.Cleanup(func() { remoteURL = old })
}

// The app's version list: kept when fetched, kept from before on failure;
// the channels are "stable" and the public ones.
func TestRemoteVersions(t *testing.T) {
	a, _ := newApp(t)
	serveChannels(t, channelsJSON, 200)
	a.CheckRemoteVersions()
	if got := publicChannels(a.Settings().RemoteVersions); !slices.Equal(got, []string{"stable", "beta"}) {
		t.Errorf("channels %v", got)
	}
	if v, notes := a.remoteFor("beta"); v != "1.5.6286" || notes != "* beta" {
		t.Errorf("beta %q %q", v, notes)
	}
	serveChannels(t, "oops", 500)
	a.CheckRemoteVersions()
	if v, _ := a.remoteFor("stable"); v != "1.5.6240" {
		t.Error("a failed check lost the list")
	}
	if !updateDiffers("1.4.1", "1.5.6240") || !updateDiffers("1.6", "1.5") || updateDiffers("1.5\n", "1.5") || updateDiffers("", "1.5") {
		t.Error("updateDiffers is the app's string inequality")
	}
}

// On connect an Origin whose version differs from its channel's is offered
// the update; Download And Update sends System/Update {Reboot: true}.
func TestUpdateOrigin(t *testing.T) {
	serveChannels(t, channelsJSON, 200)
	a, rec, s := connectedTo(t, func(s *fake.Server) {
		s.SetInfo("System/GetVersion", fake.Msg{"Number": "1.4.6146"})
		s.SetInfo("System/GetUpdateChannel", fake.Msg{"Value": "stable"})
		s.SetInfo("Network/HasInternetConnection", fake.Msg{"Value": false})
	})
	a.CheckRemoteVersions()
	// Reconnect so the connect-time offer sees the list.
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "update", func(d any) bool { return d.(struct{ Version string }).Version == "1.5.6240" })
	if u := a.Updates(); !u.Differs || u.Remote != "1.5.6240" || u.Notes != "* fixes" {
		t.Errorf("updates %+v", u)
	}
	if a.UpdateOrigin() == nil {
		t.Error("update without internet on the Origin")
	}
	s.SetInfo("Network/HasInternetConnection", fake.Msg{"Value": true})
	c, _ := a.cl()
	if _, err := c.Send(t.Context(), "Network", "HasInternetConnection", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateOrigin(); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "System", "Update"); m["Reboot"] != true {
		t.Errorf("System/Update %v", m)
	}
	// Switching to beta reads the channel back and offers beta's version.
	s.SetInfo("System/GetUpdateChannel", fake.Msg{"Value": "beta"})
	if err := a.SetUpdateChannel("beta"); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "update", func(d any) bool { return d.(struct{ Version string }).Version == "1.5.6286" })
}
