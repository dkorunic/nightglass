// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"maps"
	"testing"
)

// The fake refuses what a real Origin refuses, so a misspelt command or a
// wrong path cannot pass the tests.
func TestDefaultHandleRefusals(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	code := func(dest, cmd string, extra Msg) any {
		req := Msg{"Destination": dest, "Command": cmd, "Source": "test", "SequenceID": 1, "Type": "Command"}
		maps.Copy(req, extra)

		return s.DefaultHandle(req)["ErrorCode"]
	}
	for _, c := range []struct {
		dest, cmd string
		extra     Msg
		want      int
	}{
		{"Mount", "GotoRaDec", nil, 0},
		{"Mount", "GoToRaDec", nil, -2},     // UNKNOWN_COMMAND
		{"Telescope", "GetStatus", nil, -1}, // UNKNOWN_DESTINATION
		{"ImageServer", "DeleteFile", Msg{"FilePath": "Astrophotography/M 42/x.zip"}, 0},
		{"ImageServer", "DeleteFile", Msg{"FilePath": "Calibration/c/dark.tiff"}, 0},
		{"ImageServer", "DeleteFile", Msg{"FilePath": "Temp/0.jpg"}, -3},
		{"TaskController", "RunImaging", nil, -4}, // no Uuid: MISSING_DATA
		{"Optics", "GetConfiguration", nil, -1},   // an Origin's optics are fixed
	} {
		if got := code(c.dest, c.cmd, c.extra); got != c.want {
			t.Errorf("%s/%s %v: ErrorCode %v, want %d", c.dest, c.cmd, c.extra, got, c.want)
		}
	}
	// An absent accessory answers everything with its error, then is back.
	s.SetAbsent("DewHeater", -19, "No response from the dew heater.")
	if got := code("DewHeater", "GetStatus", nil); got != -19 {
		t.Errorf("absent dew heater: %v", got)
	}
	s.SetAbsent("DewHeater", 0, "")
	if got := code("DewHeater", "GetStatus", nil); got != 0 {
		t.Errorf("dew heater back: %v", got)
	}
}
