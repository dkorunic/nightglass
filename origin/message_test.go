// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"strings"
	"testing"

	"github.com/dkorunic/nightglass/origin/fake"
)

func TestFakeMatchesWire(t *testing.T) {
	if fake.WSPath != WSPath || fake.FilePath != FilePath {
		t.Fatal("fake paths differ from origin's")
	}
}

func TestEncodeCommandGolden(t *testing.T) {
	b, err := encodeCommand("Mount", "GotoRaDec", 42, map[string]any{"Ra": 1.4596, "Dec": -0.0943})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Command":"GotoRaDec","Dec":-0.0943,"Destination":"Mount","Ra":1.4596,"SequenceID":42,"Source":"OriginMobileApp","Type":"Command"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	m, err := parse([]byte(`{"Command":"GetStatus","Destination":"Mount","SequenceID":7,"Type":"Response","ErrorCode":-3,"ErrorMessage":"x","Alt":0.5}`))
	if err != nil || m.Key() != "Mount/GetStatus" || m.SequenceID != 7 || m.ErrorCode != -3 || m.Fields()["Alt"] != 0.5 {
		t.Errorf("parse: %+v %v", m, err)
	}
}

// Replies and notifications name their module in Source (native
// ResponseHeaders::GetCommandEnum does not read Destination); a Source that is
// not a module name falls back to Destination.
func TestInboundKeyUsesSourceModule(t *testing.T) {
	for raw, want := range map[string]string{
		`{"Command":"GetStatus","Source":"Mount","Destination":"App","Type":"Response"}`:            "Mount/GetStatus",
		`{"Command":"GetStatus","Source":"Mount","Destination":"All","Type":"Notification"}`:        "Mount/GetStatus",
		`{"Command":"GetStatus","Source":"SmartScope","Destination":"Focuser","Type":"Response"}`:   "Focuser/GetStatus",
		`{"Command":"GotoRaDec","Source":"OriginMobileApp","Destination":"Mount","Type":"Command"}`: "Mount/GotoRaDec",
	} {
		m, err := parse([]byte(raw))
		if err != nil || m.Key() != want {
			t.Errorf("%s: key %q, want %q (%v)", raw, m.Key(), want, err)
		}
	}
}

func TestErrorText(t *testing.T) {
	if got := ErrorText(-9, "raw"); got != "Failed to communicate with mount during initialization. Please contact Celestron technical support." {
		t.Errorf("-9: %q", got)
	}
	if got := ErrorText(-43, "raw message"); got != "raw message" {
		t.Errorf("-43 without app text: %q, want the telescope's message", got)
	}
	if got := ErrorText(-43, ""); got != "AUTOCAPTURE_FAILED (error -43)" {
		t.Errorf("-43 bare: %q", got)
	}
	if got := ErrorText(-123456, ""); got != "error -123456" {
		t.Errorf("unknown: %q", got)
	}
	e := &CommandError{Destination: "Mount", Command: "GotoRaDec", Code: -9}
	if !strings.Contains(e.Error(), "Failed to communicate with mount") {
		t.Errorf("CommandError text %q", e.Error())
	}
}
