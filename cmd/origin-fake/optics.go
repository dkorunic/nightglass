// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"maps"
	"regexp"
	"slices"
	"strconv"

	"github.com/dkorunic/nightglass/origin/fake"
)

// Configurable optics (-optics): a real Origin refuses every Optics command
// (its RASA 6 is fixed), so these answers are the simulator's, for GUI work
// on an Optics panel. The model figures follow from the name: aperture in
// inches (9 meaning 9.25) and focal ratio.

var opticsModels = []string{"RASA6F2", "RASA8F2", "SCT8F10", "SCT9F10", "NEWT6F5", "REFR4F7"}

var modelName = regexp.MustCompile(`^([A-Z]+)(\d+)F(\d+)$`)

// opticsModel is a model's configuration, false for an unknown model.
func opticsModel(name string) (fake.Msg, bool) {
	p := modelName.FindStringSubmatch(name)
	if p == nil || !slices.Contains(opticsModels, name) {
		return nil, false
	}
	in, _ := strconv.ParseFloat(p[2], 64)
	if in == 9 {
		in = 9.25
	}
	f, _ := strconv.ParseFloat(p[3], 64)
	ap := in * 25.4

	return fake.Msg{"ModelName": name, "Design": p[1], "Aperture": ap, "FocalLength": ap * f, "ReducerRatio": 1.0, "MirrorFlip": false}, true
}

func defaultOptics() fake.Msg {
	o, _ := opticsModel("RASA6F2")

	return o
}

// opticsMsg answers the Optics module, which DefaultHandle refuses unless
// -optics; ok is false for anything else.
func (m *sim) opticsMsg(key string, req, r fake.Msg) bool {
	name, _ := req["ModelName"].(string)
	m.mu.Lock()
	defer m.mu.Unlock()
	switch key {
	case "Optics/GetSupportedModels":
		r["SupportedModels"] = slices.Clone(opticsModels)
	case "Optics/GetConfiguration":
		o := m.optics
		if name != "" && name != o["ModelName"] {
			var ok bool
			if o, ok = opticsModel(name); !ok {
				r["ErrorCode"], r["ErrorMessage"] = -3, "Unknown optics model "+name // INVALID_DATA

				return true
			}
		}
		maps.Copy(r, o)
	case "Optics/SetConfiguration":
		next := maps.Clone(m.optics)
		switch o, ok := opticsModel(name); {
		case name == "Custom":
			ap, _ := req["Aperture"].(float64)
			fl, _ := req["FocalLength"].(float64)
			if d, _ := req["Design"].(string); !slices.Contains([]string{"RASA", "SCT", "NEWT", "REFR", "MAK"}, d) || ap <= 0 || fl <= 0 {
				r["ErrorCode"], r["ErrorMessage"] = -3, "Custom optics need a Design, FocalLength and Aperture." // INVALID_DATA

				return true
			}
			next = fake.Msg{"ModelName": name, "Design": req["Design"], "Aperture": ap, "FocalLength": fl, "ReducerRatio": 1.0, "MirrorFlip": false}
		case ok:
			next = o
		case name != "":
			r["ErrorCode"], r["ErrorMessage"] = -3, "Unknown optics model "+name // INVALID_DATA

			return true
		}
		if v, ok := req["ReducerRatio"].(float64); ok && v > 0 {
			next["ReducerRatio"] = v
		}
		if v, ok := req["MirrorFlip"].(bool); ok {
			next["MirrorFlip"] = v
		}
		m.optics = next
	default:
		return false
	}

	return true
}
