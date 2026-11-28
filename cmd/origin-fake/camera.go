// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

// cameraInfo is what the simulated camera reports beyond fake's defaults;
// the figures are an IMX178's, the names the simulator's.
var cameraInfo = fake.Msg{
	"CameraModel": "Origin IMX178 (simulated)", "CameraID": "CAM-SIM0001", "FirmwareVersion": "1.0.0", "SdkVersion": "1.0.0",
	"MaxImageWidth": 3072, "MaxImageHeight": 2048, "PixelSize": 2.4, "MaxBinning": 2,
}

// bitDepths are the Camera's capture bit depths.
var bitDepths = []float64{8, 16, 24, 48}

// badCapture names the first capture parameter in req out of the camera's
// range, "" when all are in range (or absent).
func (m *sim) badCapture(req fake.Msg, exposure, iso string) string {
	ci := m.s.DefaultHandle(fake.Msg{"Destination": "Camera", "Command": "GetCameraInfo"})
	num := func(k string) float64 {
		switch v := ci[k].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		}

		return 0
	}
	in := map[string]func(float64) bool{
		exposure:        func(v float64) bool { return v >= num("MinExposure") && v <= num("MaxExposure") },
		iso:             func(v float64) bool { return v >= num("MinIso") && v <= num("MaxIso") },
		"Binning":       func(v float64) bool { return v >= 1 && v <= num("MaxBinning") && v == float64(int(v)) },
		"BitDepth":      func(v float64) bool { return slices.Contains(bitDepths, v) },
		"ColorRBalance": func(v float64) bool { return v >= 0 && v <= 255 },
		"ColorGBalance": func(v float64) bool { return v >= 0 && v <= 255 },
		"ColorBBalance": func(v float64) bool { return v >= 0 && v <= 255 },
	}
	for k, ok := range in {
		if x, set := req[k]; set {
			if v, isNum := x.(float64); !isNum || !ok(v) {
				return k
			}
		}
	}

	return ""
}

// cameraMsg answers capture parameters, the live stream switch, snapshots'
// extra keys and video capture; ok is false for anything else.
func (m *sim) cameraMsg(key string, req, r fake.Msg) bool {
	switch key {
	case "Camera/SetCaptureParameters":
		if k := m.badCapture(req, "Exposure", "ISO"); k != "" {
			r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("%s %v is out of range.", k, req[k]) // INVALID_DATA

			return true
		}
		// Reported back by the Camera status, as an Origin does.
		m.setStatus("Camera", req, "Exposure", "ISO", "Binning", "BitDepth", "Offset", "ColorRBalance", "ColorGBalance", "ColorBBalance")
		m.s.NotifyStatus("Camera")
	case "LiveStream/SetDisableLiveStream":
		off, _ := req["Disable"].(bool)
		m.s.SetInfo("LiveStream/GetDisableLiveStream", fake.Msg{"IsDisabled": off})
	case "TaskController/RunSampleCapture":
		// Binning, BitDepth and Calibrate are taken; the frame is the same.
		if k := m.badCapture(req, "ExposureTime", "ISO"); k != "" {
			r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("%s %v is out of range.", k, req[k]) // INVALID_DATA

			return true
		}

		return false // the snapshot itself is handle's
	case "TaskController/CaptureVideo":
		m.captureVideo(req, r)
	default:
		return false
	}

	return true
}

// liveDisabled reports LiveStream/SetDisableLiveStream.
func (m *sim) liveDisabled() bool {
	r := m.s.DefaultHandle(fake.Msg{"Destination": "LiveStream", "Command": "GetDisableLiveStream"})
	off, _ := r["IsDisabled"].(bool)

	return off
}

// captureVideo records a video for 6 steps (HaltTasks ends it early) as
// VIDEO_CAPTURE, then lists it in a folder of its own in the file manager.
// Where and under what name an Origin keeps videos is not known.
func (m *sim) captureVideo(req, r fake.Msg) {
	ct, _ := req["Container"].(string)
	if !slices.Contains([]string{"ser", "avi"}, ct) {
		r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("Container %v is not ser or avi.", req["Container"]) // INVALID_DATA

		return
	}
	if d, ok := req["BitDepth"]; ok && d != 8.0 && d != 16.0 {
		r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("BitDepth %v is not 8 or 16.", d) // INVALID_DATA

		return
	}
	if k := m.badCapture(req, "Exposure", "Iso"); k != "" {
		r["ErrorCode"], r["ErrorMessage"] = -3, fmt.Sprintf("%s %v is out of range.", k, req[k]) // INVALID_DATA

		return
	}
	name, _ := req["ObjectName"].(string)
	dir := fmt.Sprintf("%s_%s", cmp.Or(name, "Video"), time.Now().Format("20060102_150405"))
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.halt != nil {
		m.halt()
	}
	m.halt = cancel
	m.mu.Unlock()
	m.state("VIDEO_CAPTURE", "")
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(6 * m.step):
		}
		m.s.SetFile("Images/Astrophotography/"+dir+"/video."+ct, []byte("origin-fake video\n"))
		m.mu.Lock()
		m.videos = append(m.videos, dir)
		m.mu.Unlock()
		m.state("IDLE", "")
	}()
}
