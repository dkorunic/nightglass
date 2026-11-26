// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Package fake is an in-process Celestron Origin for tests and offline GUI
// work: the WebSocket envelope at WSPath and files under FilePath. It does
// not import package origin, so origin's own tests can use it.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Wire paths; TestFakeMatchesWire in package origin keeps them in step.
const (
	WSPath   = "/SmartScope-1.0/mountControlEndpoint"
	FilePath = "/SmartScope-1.0/dev2/"
)

// Msg is one flat JSON frame.
type Msg = map[string]any

// Server is a fake Origin. Set Handle before clients connect.
type Server struct {
	// Handle answers one command; a nil reply sends nothing. It runs in its
	// own goroutine per command, so it may sleep. Nil means DefaultHandle.
	Handle func(req Msg) Msg
	// OnConnect, if set, runs (in its own goroutine) as each client
	// connects, e.g. to push every status as a real Origin does.
	OnConnect func()

	srv      *httptest.Server
	conns    map[*websocket.Conn]struct{}
	status   map[string]Msg
	info     map[string]Msg // other Get* answers, by "Dest/Cmd"
	files    map[string][]byte
	received []Msg
	// PingInterval > 0 pings each client that often, as a real Origin's
	// WebSocket server does (every 5 s in captures).
	PingInterval time.Duration

	mu   sync.Mutex
	down bool // refusing connections, as an Origin rebooting or off
}

// New starts a fake on addr ("" picks a free 127.0.0.1 port).
func New(addr string) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		conns: map[*websocket.Conn]struct{}{},
		files: map[string][]byte{},
		info: map[string]Msg{
			"Camera/GetCameraInfo":              {"DefaultIso": 200, "MinIso": 100, "MaxIso": 20000, "MinExposure": 0.001, "MaxExposure": 30.0, "IsColor": true},
			"Mount/GetMountConfig":              {"OnWedge": false, "IsEquatorial": false},
			"LiveStream/GetEnableManual":        {"IsManual": false},
			"Camera/GetFilter":                  {"Filter": "Clear"},
			"Focuser/GetPositionLimits":         {"Min": 1000, "Max": 35000},
			"ImageServer/GetStretch":            {"Background": 0.5, "Strength": 0.5},
			"LedRing/GetBrightnessLevel":        {"BrightnessLevel": 0.5},
			"LampController/GetBrightnessLevel": {"BrightnessLevel": 0.5},
			"Focuser/GetFocuserAdvancedSettings": {
				"AutoFocusAfterGotoIsEnabled": false, "TemperatureShiftAutoFocusIsEnabled": false, "ThresholdTemperatureC": 5.0,
			},
			"ImageServer/GetListOfAvailableDirectories":    {"DirectoryList": []any{}},
			"ImageServer/GetStackedMasterFromDirectory":    {"FileExists": false},
			"System/GetVersion":                            {"Number": "0.0.0-fake"},
			"System/GetModel":                              {"Value": "Origin (simulated)"},
			"System/GetUpdateChannel":                      {"Value": "stable"},
			"System/GetAutomaticUpdates":                   {"Enabled": false},
			"Network/HasInternetConnection":                {"Value": false},
			"FactoryCalibrationController/GetSerialNumber": {"SerialNumber": "SIM-0001"},
		},
		status: map[string]Msg{
			"Mount": {
				"Alt": 0.0, "Azm": 0.0, "Ra": 0.0, "Dec": 0.0, "IsAligned": false, "IsGotoOver": true,
				"IsTracking": false, "BatteryLevel": "HIGH", "ChargerStatus": "DISCHARGING", "BatteryVoltage": 12.1, "NumAlignRefs": 0,
			},
			"TaskController": {"State": "IDLE", "Stage": "COMPLETE", "IsReady": true},
			"Focuser":        {"Position": 30000, "Velocity": 0.0, "IsMoveToOver": true, "IsCalibrationComplete": true},
			"Environment": {
				"AmbientTemperature": 12.5, "Humidity": 61.0, "DewPoint": 5.2, "CpuTemperature": 41.0,
				"OtaFanOn": true, "CpuFanOn": true, "Recalibrating": false,
			},
			"DewHeater":                    {"Mode": "Auto", "HeaterLevel": 0.2, "Aggression": 5, "ManualPowerLevel": 0.5},
			"ElPanel":                      {"Connected": false, "LightOn": false},
			"Disk":                         {"Capacity": int64(64 << 30), "FreeBytes": int64(50 << 30), "Level": "OK"},
			"Camera":                       {"Exposure": 10.0, "ISO": 200, "Binning": 1, "BitDepth": 16},
			"FactoryCalibrationController": {"IsCalibrated": true, "CurrentPhase": "COMPLETE"},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(WSPath, s.ws)
	mux.HandleFunc(FilePath, s.file)
	s.srv = httptest.NewUnstartedServer(mux)
	s.srv.Listener.Close()
	s.srv.Listener = l
	s.srv.Start()

	return s, nil
}

// Host is the server's "ip:port", what an Origin client dials.
func (s *Server) Host() string { return s.srv.Listener.Addr().String() }

// Close disconnects every client and stops the server.
func (s *Server) Close() {
	s.DropAll()
	s.srv.Close()
}

// DropAll closes every client connection without stopping the server, as a
// Wi-Fi drop would.
func (s *Server) DropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		_ = c.CloseNow()
		delete(s.conns, c)
	}
}

// CloseAll closes every client connection with a normal close frame, as a
// telescope ending the session would.
func (s *Server) CloseAll() { s.CloseAllWith(websocket.StatusNormalClosure, "") }

// SetDown makes the fake refuse connections, as an Origin rebooting or
// switched off, closing the open ones; SetDown(false) accepts them again.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	s.down = down
	s.mu.Unlock()
	if down {
		s.CloseAllWith(websocket.StatusGoingAway, "")
	}
}

// Down reports whether the fake is refusing connections.
func (s *Server) Down() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.down
}

// CloseAllWith closes every client connection with code and reason, e.g.
// 1011 "Ping timeout" as a real Origin's server sends after missed pongs.
func (s *Server) CloseAllWith(code websocket.StatusCode, reason string) {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
		delete(s.conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close(code, reason)
	}
}

// SetHandle replaces Handle while clients may be connected (the server
// reads Handle under its lock).
func (s *Server) SetHandle(h func(req Msg) Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Handle = h
}

// Reply is the bare successful Response to req. Like the telescope, it
// names the module in Source and addresses the app in Destination, so the
// client's tests exercise Message.Module on inbound frames.
func Reply(req Msg) Msg {
	// A real Origin addresses the reply to the requester's Source.
	return Msg{
		"Command": req["Command"], "Destination": req["Source"], "SequenceID": req["SequenceID"],
		"Source": req["Destination"], "Type": "Response", "ErrorCode": 0, "ErrorMessage": "",
	}
}

// DefaultHandle acknowledges every command, answers GetStatus and
// GetCaptureParameters with the module's status and the other known Get*
// queries from SetInfo, and refuses a RunImaging without its required Uuid.
func (s *Server) DefaultHandle(req Msg) Msg {
	r := Reply(req)
	cmd, _ := req["Command"].(string)
	dest, _ := req["Destination"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case cmd == "GetStatus" || cmd == "GetCaptureParameters":
		maps.Copy(r, s.status[dest])
	case s.info[dest+"/"+cmd] != nil:
		maps.Copy(r, s.info[dest+"/"+cmd])
	case dest == "TaskController" && cmd == "RunImaging" && req["Uuid"] == nil:
		r["ErrorCode"], r["ErrorMessage"] = -1, "missing required field Uuid"
	}

	return r
}

// SetInfo merges fields into the answer to a Get* query such as
// "Mount/GetMountConfig".
func (s *Server) SetInfo(key string, fields Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info[key] == nil {
		s.info[key] = Msg{}
	}
	maps.Copy(s.info[key], fields)
}

// SetStatus merges fields into a module's status.
func (s *Server) SetStatus(dest string, fields Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[dest] == nil {
		s.status[dest] = Msg{}
	}
	maps.Copy(s.status[dest], fields)
}

// Status returns a copy of the module's current status.
func (s *Server) Status(dest string) Msg {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.status[dest])
}

// NotifyStatus pushes the module's current status as a GetStatus
// Notification (the Camera's as GetCaptureParameters, as an Origin does).
func (s *Server) NotifyStatus(dest string) {
	s.mu.Lock()
	m := Msg{}
	maps.Copy(m, s.status[dest])
	s.mu.Unlock()
	cmd := "GetStatus"
	if dest == "Camera" {
		cmd = "GetCaptureParameters"
	}
	s.Notify(dest, cmd, m)
}

// Notify pushes a Notification about module dest to every client.
func (s *Server) Notify(dest, cmd string, fields Msg) {
	m := Msg{"Command": cmd, "Destination": "All", "SequenceID": 0, "Source": dest, "Type": "Notification"}
	maps.Copy(m, fields)
	b, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("fake: notification %s/%s: %v", dest, cmd, err))
	}
	s.SendRaw(b)
}

// SendRaw writes b as one text frame to every client, for malformed-frame tests.
func (s *Server) SendRaw(b []byte) {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = c.Write(ctx, websocket.MessageText, b)
		cancel()
	}
}

// SetFile serves b at FilePath+location.
func (s *Server) SetFile(location string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[location] = b
}

// Received returns every command received so far, oldest first.
func (s *Server) Received() []Msg {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Msg(nil), s.received...)
}

// Wait returns the first received command matching pred, polling for up to d.
func (s *Server) Wait(pred func(Msg) bool, d time.Duration) (Msg, bool) {
	for end := time.Now().Add(d); ; time.Sleep(5 * time.Millisecond) {
		for _, m := range s.Received() {
			if pred(m) {
				return m, true
			}
		}
		if time.Now().After(end) {
			return nil, false
		}
	}
}

// Is matches a command by destination and name, for Wait.
func Is(dest, cmd string) func(Msg) bool {
	return func(m Msg) bool { return m["Destination"] == dest && m["Command"] == cmd }
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	if s.Down() {
		http.Error(w, "down", http.StatusServiceUnavailable)

		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(8 << 20)
	s.mu.Lock()
	s.conns[c] = struct{}{}
	onConnect, ping := s.OnConnect, s.PingInterval
	s.mu.Unlock()
	if onConnect != nil {
		go onConnect()
	}
	if ping > 0 {
		pctx, stopPing := context.WithCancel(context.Background())
		defer stopPing()
		go func() {
			t := time.NewTicker(ping)
			defer t.Stop()
			for {
				select {
				case <-pctx.Done():
					return
				case <-t.C:
					cctx, cancel := context.WithTimeout(pctx, ping)
					err := c.Ping(cctx)
					cancel()
					if err != nil {
						return
					}
				}
			}
		}()
	}
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = c.CloseNow()
	}()
	ctx := context.Background()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req Msg
		if json.Unmarshal(b, &req) != nil {
			continue
		}
		s.mu.Lock()
		s.received = append(s.received, req)
		h := s.Handle
		s.mu.Unlock()
		if h == nil {
			h = s.DefaultHandle
		}
		go func() {
			rep := h(req)
			if rep == nil {
				return
			}
			out, err := json.Marshal(rep)
			if err != nil {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := c.Write(wctx, websocket.MessageText, out); err != nil && !errors.Is(err, net.ErrClosed) {
				return
			}
		}()
	}
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b, ok := s.files[strings.TrimPrefix(r.URL.Path, FilePath)]
	s.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><body>Not Found</body></html>"))

		return
	}
	_, _ = w.Write(b)
}
