// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Package origin speaks the Celestron Origin protocol: JSON frames over a
// WebSocket, image files over HTTP, and the UDP discovery broadcast.
package origin

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
)

// Wire constants, from the Origin app's native library (spec §3).
const (
	WSPath   = "/SmartScope-1.0/mountControlEndpoint"
	FilePath = "/SmartScope-1.0/dev2/"
	Source   = "OriginMobileApp"

	TypeCommand      = "Command"
	TypeResponse     = "Response"
	TypeNotification = "Notification"
)

// Header is the envelope every frame carries beside its payload fields.
type Header struct {
	Command      string
	Destination  string
	Source       string
	Type         string
	ErrorMessage string
	SequenceID   int
	ErrorCode    int
}

// Message is one received frame: its header and the whole JSON object.
type Message struct {
	Raw json.RawMessage

	Header

	waited bool // a Response a Send was waiting for
}

// Waited reports whether m is the Response to a Send that waited for it;
// a Response to a Post, or to a Send that gave up, was not.
func (m Message) Waited() bool { return m.waited }

// modules are the Destination names of spec §3.3.
var modules = map[string]bool{
	"Autoguider": true, "Camera": true, "DewHeater": true, "Disk": true, "ElPanel": true,
	"Environment": true, "Error": true, "FactoryCalibrationController": true, "Focuser": true,
	"ImageServer": true, "LampController": true, "LedRing": true, "LiveStream": true, "Mosaic": true,
	"Mount": true, "Network": true, "Optics": true, "OrientationSensor": true, "System": true,
	"TaskController": true,
}

// Module is the module a frame concerns. A command names it in Destination;
// a reply or notification names itself in Source (the native
// ResponseHeaders::GetCommandEnum does not read Destination), so a Source
// that is a module name wins there.
func (m Message) Module() string {
	if m.Type != TypeCommand && modules[m.Source] {
		return m.Source
	}

	return m.Destination
}

// Key names the module and command, e.g. "Mount/GetStatus".
func (m Message) Key() string { return m.Module() + "/" + m.Command }

// Decode unmarshals the frame's payload fields into v. Like the app's
// org.json getInt, an integer field accepts a whole number written as a
// decimal (12.0): Go would reject the whole frame, so it is decoded again
// after re-encoding, which writes whole floats as integers.
func (m Message) Decode(v any) error {
	err := json.Unmarshal(m.Raw, v)
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) || !strings.HasPrefix(te.Value, "number") {
		return err
	}
	var g any
	if json.Unmarshal(m.Raw, &g) != nil {
		return err
	}
	b, merr := json.Marshal(g)
	if merr != nil {
		return err
	}

	return json.Unmarshal(b, v)
}

// Fields returns the frame as a map, for forwarding to the GUI.
func (m Message) Fields() map[string]any {
	var f map[string]any
	_ = json.Unmarshal(m.Raw, &f)

	return f
}

// parse reads a frame's header; the Message keeps b, which the caller must
// not reuse (websocket.Conn.Read returns a new slice for each frame).
func parse(b []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m.Header); err != nil {
		return Message{}, err
	}
	m.Raw = b

	return m, nil
}

// encodeCommand builds one flat Command frame; header fields win over
// payload keys of the same name.
func encodeCommand(dest, cmd string, seq int, payload map[string]any) ([]byte, error) {
	m := make(map[string]any, len(payload)+5)
	maps.Copy(m, payload)
	m["Command"], m["Destination"], m["SequenceID"], m["Source"], m["Type"] = cmd, dest, seq, Source, TypeCommand

	return json.Marshal(m)
}
