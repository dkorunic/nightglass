// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// The simulated Wi-Fi: the networks the Origin sees (one row per band, as
// a dual-band access point shows twice), those it knows, and the one it is
// on. The networks, addresses and wrong password are the simulator's.

// WifiStatus values of Network/GetStatus.
const (
	wifiDisconnected = 0
	wifiInitializing = 2 // 1–3 are the Origin's initialization steps
	wifiClient       = 4
	wifiDirect       = 5
)

// wrongPassword is the password the simulated networks refuse.
const wrongPassword = "wrongpass"

type wifiNet struct {
	ssid, auth, password string // auth: OPEN, COMPATIBLE or INCOMPATIBLE (WPA3 only)
	signal               []int  // dBm per band, 0 when out of range
	known, wrong         bool
}

type wifi struct {
	connected  string // the SSID joined, "" when none
	dcPassword string // the direct connect (access point) password
	nets       []*wifiNet
	force      bool // ForceDirectConnect
	want5GHz   bool // 5GHzAccessPoint
	joining    bool // a join under way: WifiStatus initializing
}

// newWifi is the Origin's Wi-Fi as the simulator starts: on HomeNet.
func newWifi() *wifi {
	return &wifi{
		connected: "HomeNet", dcPassword: "origin1234",
		nets: []*wifiNet{
			{ssid: "HomeNet", auth: "COMPATIBLE", signal: []int{-48, -63}, known: true, password: "homenet123"},
			{ssid: "CafeGuest", auth: "OPEN", signal: []int{-71}},
			{ssid: "Neighbour-WPA3", auth: "INCOMPATIBLE", signal: []int{-77}},
			{ssid: "Garage", auth: "COMPATIBLE", signal: []int{-84}},
			{ssid: "Office", auth: "COMPATIBLE", signal: []int{0}, known: true, password: "office-pass"},
		},
	}
}

func (w *wifi) find(ssid string) *wifiNet {
	i := slices.IndexFunc(w.nets, func(n *wifiNet) bool { return n.ssid == ssid })
	if i < 0 {
		return nil
	}

	return w.nets[i]
}

func (n *wifiNet) inRange() bool {
	return slices.ContainsFunc(n.signal, func(s int) bool { return s != 0 })
}

// status is Network/GetStatus.
func (w *wifi) status() fake.Msg {
	st := fake.Msg{"Value": wifiDisconnected, "InternetStatus": 0, "IPAddress": ""}
	switch {
	case w.force:
		st["Value"], st["IPAddress"] = wifiDirect, "10.0.0.1"
	case w.joining:
		st["Value"] = wifiInitializing
	case w.connected != "":
		st["Value"], st["IPAddress"] = wifiClient, "192.168.1.50"
	}

	return st
}

// visible is Network/GetVisibleWiFi's Networks: the band in use is the
// first row of the network joined.
func (w *wifi) visible() []any {
	var out []any
	for _, n := range w.nets {
		if !n.known && !n.inRange() {
			continue // a forgotten network that is not there
		}
		for i, s := range n.signal {
			out = append(out, fake.Msg{
				"SSID": n.ssid, "SignalStrength": s, "AuthType": n.auth, "IsConnected": i == 0 && w.connected == n.ssid && !w.joining,
				"HasConfiguredPassword": n.known, "HasWrongPassword": n.wrong, "IsVisible": s != 0,
			})
		}
	}

	return out
}

// validPassword is the Origin's rule for a Wi-Fi or access point password.
func validPassword(p string) bool { return len(p) >= 8 && len(p) <= 63 }

// networkMsg answers the Network module; ok is false for anything else.
func (m *sim) networkMsg(key string, req, r fake.Msg) bool {
	ssid, _ := req["Ssid"].(string)
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.wifi
	switch key {
	case "Network/GetVisibleWiFi":
		r["Networks"] = w.visible()

		return true
	case "Network/GetForceDirectConnect":
		r["Value"] = w.force

		return true
	case "Network/Get5GHzAccessPoint":
		r["5GHzAllowed"], r["5GHzRequested"] = true, w.want5GHz

		return true
	case "Network/5GHzAccessPoint":
		w.want5GHz, _ = req["Value"].(bool)

		return true
	case "Network/SetDirectConnectPassword":
		if p, _ := req["Password"].(string); !validPassword(p) {
			r["ErrorCode"], r["ErrorMessage"] = -101, "The password must have 8 to 63 characters." // WIFI_INVALID_PASSWORD_LENGTH
		} else {
			w.dcPassword = p
		}

		return true
	case "Network/ForceDirectConnect":
		w.force, _ = req["Value"].(bool)
	case "Network/AddWiFiNetwork":
		pass, _ := req["Password"].(string)
		n := w.find(ssid)
		switch {
		case n != nil && n.auth == "INCOMPATIBLE":
			r["ErrorCode"], r["ErrorMessage"] = -98, "The network only allows WPA3." // WIFI_SPECIFIED_NETWORK_INCOMPATIBLE
		case !validPassword(pass) && (pass != "" || n == nil || n.auth != "OPEN"):
			r["ErrorCode"], r["ErrorMessage"] = -101, "The password must have 8 to 63 characters." // WIFI_INVALID_PASSWORD_LENGTH
		case n == nil: // a hidden network: known, not seen
			w.nets = append(w.nets, &wifiNet{ssid: ssid, auth: "COMPATIBLE", signal: []int{0}, known: true, password: pass})
		default:
			n.known, n.wrong, n.password = true, false, pass
		}

		return true
	case "Network/ConnectToSpecificNetwork":
		switch n := w.find(ssid); {
		case n == nil || !n.known:
			r["ErrorCode"], r["ErrorMessage"] = -102, "The network is not configured." // WIFI_SPECIFIED_NETWORK_NOT_CONFIGURED
		case !n.inRange():
			r["ErrorCode"], r["ErrorMessage"] = -103, "The network is not in range." // WIFI_SPECIFIED_NETWORK_NOT_VISIBLE
		default:
			m.join(n)
		}

		return true
	case "Network/ReconnectWiFi":
		if i := slices.IndexFunc(w.nets, func(n *wifiNet) bool { return n.known && n.inRange() }); i >= 0 {
			m.join(w.nets[i])
		}

		return true
	case "Network/DeleteWiFiNetwork":
		if n := w.find(ssid); n != nil {
			m.forget(n)
		}
	case "Network/RemoveAllKnownNetworks":
		for _, n := range w.nets {
			m.forget(n)
		}
	default:
		return false
	}
	m.netChanged()

	return true
}

// forget drops a known network (m.mu held), leaving it if joined.
func (m *sim) forget(n *wifiNet) {
	n.known, n.wrong, n.password = false, false, ""
	if m.wifi.connected == n.ssid {
		m.wifi.connected = ""
	}
}

// join joins n (m.mu held): initializing for a moment, then on it, or
// disconnected with HasWrongPassword when its password is wrongPassword.
func (m *sim) join(n *wifiNet) {
	w := m.wifi
	w.connected, w.joining, w.force = "", true, false
	m.netChanged()
	time.AfterFunc(2*m.step, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.joining, n.wrong = false, n.password == wrongPassword
		if !n.wrong {
			w.connected = n.ssid
		}
		m.netChanged()
	})
}

// netChanged publishes the Network status and pushes it (m.mu held).
func (m *sim) netChanged() {
	st := m.wifi.status()
	m.s.SetStatus("Network", st)
	go m.s.NotifyStatus("Network") // not under m.mu: it writes to every client
}
