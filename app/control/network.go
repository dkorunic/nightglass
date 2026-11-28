// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/dkorunic/nightglass/origin"
)

// The Wi-Fi and Optics panes of Celestron's web client (firmware 1.5.6286),
// which the Android app has not: the Origin's own Network module (station
// and Direct Connect access point) and its Optics module (an Origin answers
// every Optics command with an error, and the pane is then not shown).

// WiFiNetwork is a network the Origin sees or knows (Network/GetVisibleWiFi),
// once per SSID.
type WiFiNetwork struct {
	SSID                  string
	AuthType              string // OPEN, COMPATIBLE, INCOMPATIBLE (WPA3 only)
	SignalStrength        int    // dBm; 0: known but not in range
	IsConnected           bool
	HasConfiguredPassword bool // known
	HasWrongPassword      bool
	IsVisible             bool
}

// WiFiState is the Wi-Fi pane: the link (Network/GetStatus), Direct Connect
// and the networks.
type WiFiState struct {
	IPAddress   string
	Networks    []WiFiNetwork
	Mode        int // WifiStatus: 0 disconnected, 1–3 starting, 4 client, 5 Direct Connect; -1 unknown
	Internet    bool
	ForceDirect bool
	Allowed5GHz bool
	Use5GHz     bool
}

// mergeWiFi folds a dual-band router's rows into one per SSID, its strongest
// signal and any row's flags, strongest first and out of range last, as the
// web client lists them.
func mergeWiFi(rows []WiFiNetwork) []WiFiNetwork {
	by := map[string]int{}
	var out []WiFiNetwork
	for _, n := range rows {
		i, ok := by[n.SSID]
		if !ok {
			by[n.SSID] = len(out)
			out = append(out, n)

			continue
		}
		m := &out[i]
		if n.SignalStrength != 0 && (m.SignalStrength == 0 || n.SignalStrength > m.SignalStrength) {
			m.SignalStrength, m.AuthType = n.SignalStrength, n.AuthType
		}
		m.IsConnected = m.IsConnected || n.IsConnected
		m.HasConfiguredPassword = m.HasConfiguredPassword || n.HasConfiguredPassword
		m.HasWrongPassword = m.HasWrongPassword || n.HasWrongPassword
		m.IsVisible = m.IsVisible || n.IsVisible
	}
	key := func(n WiFiNetwork) int {
		if n.SignalStrength == 0 {
			return -1000 // out of range
		}

		return n.SignalStrength
	}
	slices.SortStableFunc(out, func(x, y WiFiNetwork) int { return cmp.Compare(key(y), key(x)) })

	return out
}

// WiFi asks the Origin for the Wi-Fi pane, as the web client does on
// connecting; a query the Origin refuses leaves its part unknown.
func (a *App) WiFi() (WiFiState, error) {
	st := WiFiState{Mode: -1}
	err := a.with(func(c *origin.Client) error {
		ctx := context.Background()
		if m, err := c.Send(ctx, "Network", "GetStatus", nil); err == nil {
			f := m.Fields()
			st.Mode = intField(f["Value"], -1)
			st.Internet = truthy(f["InternetStatus"])
			st.IPAddress, _ = f["IPAddress"].(string)
		}
		if m, err := c.Send(ctx, "Network", "GetForceDirectConnect", nil); err == nil {
			st.ForceDirect = truthy(m.Fields()["Value"])
		}
		if m, err := c.Send(ctx, "Network", "Get5GHzAccessPoint", nil); err == nil {
			f := m.Fields()
			st.Allowed5GHz, st.Use5GHz = truthy(f["5GHzAllowed"]), truthy(f["5GHzRequested"])
		}
		m, err := c.Send(ctx, "Network", "GetVisibleWiFi", nil)
		if err != nil {
			return err
		}
		var v struct{ Networks []WiFiNetwork }
		if err := m.Decode(&v); err != nil {
			return err
		}
		st.Networks = mergeWiFi(v.Networks)

		return nil
	})

	return st, err
}

// JoinWiFi joins a network, storing its passphrase first when given (an open
// network stores an empty one): AddWiFiNetwork, then ConnectToSpecificNetwork.
func (a *App) JoinWiFi(ssid, password string, store bool) error {
	if ssid == "" {
		return errWiFiSSID
	}
	if store && password != "" {
		if err := wpaPassphrase(password); err != nil {
			return err
		}
	}

	return a.with(func(c *origin.Client) error {
		ctx := context.Background()
		if store {
			if _, err := c.Send(ctx, "Network", "AddWiFiNetwork", map[string]any{"Ssid": ssid, "Password": password}); err != nil {
				return err
			}
		}
		_, err := c.Send(ctx, "Network", "ConnectToSpecificNetwork", map[string]any{"Ssid": ssid})

		return err
	})
}

// ForgetWiFi forgets one known network, or all of them for "".
func (a *App) ForgetWiFi(ssid string) error {
	if ssid == "" {
		return a.set("Network", "RemoveAllKnownNetworks", nil)
	}

	return a.set("Network", "DeleteWiFiNetwork", map[string]any{"Ssid": ssid})
}

// SetForceDirectConnect keeps the Origin on its own access point, or lets
// it join a network again: the first known one in range with a good
// passphrase, else whichever it finds (ReconnectWiFi), as the web client.
func (a *App) SetForceDirectConnect(on bool) error {
	if err := a.set("Network", "ForceDirectConnect", map[string]any{"Value": on}); err != nil || on {
		return err
	}
	st, err := a.WiFi()
	if err != nil {
		return err
	}
	for _, n := range st.Networks {
		if n.HasConfiguredPassword && n.IsVisible && !n.HasWrongPassword {
			return a.JoinWiFi(n.SSID, "", false)
		}
	}

	return a.set("Network", "ReconnectWiFi", nil)
}

// SetWiFi5GHz chooses the Direct Connect access point's band.
func (a *App) SetWiFi5GHz(on bool) error {
	return a.set("Network", "5GHzAccessPoint", map[string]any{"Value": on})
}

// SetDirectConnectPassword sets the Origin's access point passphrase.
func (a *App) SetDirectConnectPassword(password string) error {
	if err := wpaPassphrase(password); err != nil {
		return err
	}

	return a.set("Network", "SetDirectConnectPassword", map[string]any{"Password": password})
}

var errWiFiSSID = errors.New("no network name")

// wpaPassphrase checks a WPA2 passphrase: 8 to 63 printable ASCII
// characters (IEEE 802.11i), which the Origin refuses otherwise
// (WIFI_INVALID_PASSWORD_LENGTH).
func wpaPassphrase(p string) error {
	if len(p) < 8 || len(p) > 63 {
		return errors.New("Wi-Fi Password: a passphrase has 8 to 63 characters")
	}
	for _, r := range p {
		if r < 0x20 || r > 0x7e {
			return errors.New("Wi-Fi Password: a passphrase has only printable ASCII characters")
		}
	}

	return nil
}

// OpticsConfig is the telescope's optics (Optics/GetConfiguration): a known
// model's, or "Custom" with its design, focal length and aperture in mm.
type OpticsConfig struct {
	MirrorFlip   *bool // not shown, sent back as reported
	ModelName    string
	Design       string // REFR, NEWT, SCT, MAK, RASA
	FocalLength  float64
	Aperture     float64
	ReducerRatio float64
}

// OpticsState is the Optics pane: Available false when the Origin refuses
// Optics (an Origin's optics are fixed), and the pane is then not shown.
type OpticsState struct {
	Models    []OpticsModel
	Config    OpticsConfig
	Available bool
}

// OpticsModel is a model the Origin knows, with Nightglass's name for it.
type OpticsModel struct {
	Key  string
	Name string
}

var (
	opticsKey     = regexp.MustCompile(`^([A-Z]+)(\d+)F(\d+)$`)
	opticsDesigns = map[string]string{
		"REFR": "Refractor", "NEWT": "Newtonian", "SCT": "Schmidt-Cassegrain", "MAK": "Maksutov-Cassegrain",
		"RASA": "Rowe-Ackermann Schmidt Astrograph",
	}
	// opticsShort names a design in the Model list, which is narrow.
	opticsShort = map[string]string{"REFR": "Refractor", "NEWT": "Newtonian", "SCT": "SCT", "MAK": "Maksutov", "RASA": "RASA"}
)

// opticsName names a model key such as SCT8F10 by its aperture in inches,
// focal ratio and design; an unknown form stays as it is.
func opticsName(key string) string {
	m := opticsKey.FindStringSubmatch(key)
	if m == nil || opticsDesigns[m[1]] == "" {
		return key
	}
	in := m[2]
	if in == "9" {
		in = "9.25" // the 9¼″ SCT
	}

	return fmt.Sprintf("%s″ f/%s %s", in, m[3], opticsShort[m[1]])
}

// Optics asks for the models and the configuration, as the web client does
// on opening its Optics tab.
func (a *App) Optics() (OpticsState, error) {
	var st OpticsState
	err := a.with(func(c *origin.Client) error {
		m, err := c.Send(context.Background(), "Optics", "GetSupportedModels", nil)
		if err != nil {
			return nil //nolint:nilerr // refused: an Origin, whose optics are fixed
		}
		var v struct{ SupportedModels []string }
		if err := m.Decode(&v); err != nil {
			return err
		}
		for _, k := range v.SupportedModels {
			st.Models = append(st.Models, OpticsModel{Key: k, Name: opticsName(k)})
		}
		st.Available = true
		st.Config, err = a.opticsConfig(c, "")

		return err
	})

	return st, err
}

// OpticsFor is a known model's figures, shown before it is chosen.
func (a *App) OpticsFor(model string) (OpticsConfig, error) {
	var cfg OpticsConfig
	err := a.with(func(c *origin.Client) error {
		var err error
		cfg, err = a.opticsConfig(c, model)

		return err
	})

	return cfg, err
}

func (*App) opticsConfig(c *origin.Client, model string) (OpticsConfig, error) {
	var p map[string]any
	if model != "" {
		p = map[string]any{"ModelName": model}
	}
	var cfg OpticsConfig
	m, err := c.Send(context.Background(), "Optics", "GetConfiguration", p)
	if err != nil {
		return cfg, err
	}

	return cfg, m.Decode(&cfg)
}

// SetOptics sends a new configuration, checked as the web client checks it.
// A known model goes alone first, then its reducer and mirror flip: the
// firmware ignores the other fields beside a known ModelName.
func (a *App) SetOptics(cfg OpticsConfig) error {
	custom := cfg.ModelName == "" || cfg.ModelName == "Custom"
	if custom {
		if opticsDesigns[cfg.Design] == "" {
			return fmt.Errorf("Change Optics: unknown design %q", cfg.Design)
		}
		if cfg.Aperture < 50 || cfg.Aperture > 500 {
			return errors.New("Change Optics: Please enter an aperture from 50 to 500 mm.")
		}
		if cfg.FocalLength < 100 || cfg.FocalLength > 5000 {
			return errors.New("Change Optics: Please enter a focal length from 100 to 5000 mm.")
		}
	}
	if cfg.ReducerRatio < 0.2 || cfg.ReducerRatio > 3 {
		return errors.New("Change Optics: Please enter a reducer ratio from 0.2 to 3.0.")
	}
	rest := map[string]any{"ReducerRatio": cfg.ReducerRatio}
	if cfg.MirrorFlip != nil {
		rest["MirrorFlip"] = *cfg.MirrorFlip
	}

	return a.with(func(c *origin.Client) error {
		ctx := context.Background()
		if custom {
			rest["ModelName"], rest["Design"], rest["FocalLength"], rest["Aperture"] = "Custom", cfg.Design, cfg.FocalLength, cfg.Aperture
		} else if _, err := c.Send(ctx, "Optics", "SetConfiguration", map[string]any{"ModelName": cfg.ModelName}); err != nil {
			return err
		}
		_, err := c.Send(ctx, "Optics", "SetConfiguration", rest)

		return err
	})
}

// intField reads a JSON number (or numeric string) as an int.
func intField(v any, def int) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}

	return def
}

// truthy reads a JSON boolean that may come as 0/1.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	}

	return false
}
