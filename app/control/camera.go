// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"errors"
	"fmt"

	"github.com/dkorunic/nightglass/origin"
)

// The camera controls of Celestron's web client (firmware 1.5.6286) beyond
// the app's: the live stream switch, binning, bit depth, white balance, a
// single capture with calibration, and video. Exposure, ISO and the manual
// live view stay the app's settings (Snapshot, SnapshotExposureSec, ISO).

// SetLiveStream starts or stops the Origin's live view frames.
func (a *App) SetLiveStream(on bool) error {
	return a.set("LiveStream", "SetDisableLiveStream", map[string]any{"Disable": !on}, "GetDisableLiveStream")
}

// bitDepths are the camera's frame formats: RAW8, RAW16, RGB8, RGB16.
var bitDepths = map[int]bool{8: true, 16: true, 24: true, 48: true}

// SetCameraParameter sets one of the camera's Binning (1 to its maximum),
// BitDepth (8, 16, 24, 48) or ColorRBalance, ColorGBalance, ColorBBalance
// (0–255), only while the Origin is idle, as the web client allows.
func (a *App) SetCameraParameter(name string, v int) error {
	return a.with(func(c *origin.Client) error {
		switch name {
		case "Binning":
			ci, _ := c.CameraInfo()
			if hi := max(ci.MaxBinning, 1); v < 1 || v > hi {
				return fmt.Errorf("binning %d outside 1–%d", v, hi)
			}
		case "BitDepth":
			if !bitDepths[v] {
				return fmt.Errorf("bit depth %d is not 8, 16, 24 or 48", v)
			}
		case "ColorRBalance", "ColorGBalance", "ColorBBalance":
			if v < 0 || v > 255 {
				return fmt.Errorf("white balance %d outside 0–255", v)
			}
		default:
			return fmt.Errorf("unknown camera parameter %q", name)
		}
		if err := idle(c); err != nil {
			return err
		}
		ctx := context.Background()
		if _, err := c.Send(ctx, "Camera", "SetCaptureParameters", map[string]any{name: v}); err != nil {
			return err
		}

		return c.Post(ctx, "Camera", "GetCaptureParameters", nil)
	})
}

// CaptureSingle is the web client's Capture Single Image: a snapshot (the
// app's, exposure and ISO in manual live view only) calibrated with darks
// and flats as chosen, at the camera's binning and bit depth.
func (a *App) CaptureSingle() error {
	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		s := a.Settings()
		p := map[string]any{"Calibrate": s.Calibrate}
		if s.ManualImaging && s.Snapshot {
			_, exp, iso := a.capture(c)
			p["ExposureTime"], p["ISO"] = exp, iso
		}
		var cp struct{ Binning, BitDepth *int }
		if m, ok := c.Last("Camera/GetCaptureParameters"); ok && m.Decode(&cp) == nil {
			if cp.Binning != nil {
				p["Binning"] = *cp.Binning
			}
			if cp.BitDepth != nil {
				p["BitDepth"] = *cp.BitDepth
			}
		}
		_, err := c.Send(context.Background(), "TaskController", "RunSampleCapture", p)

		return err
	})
}

// VideoRequest is a video capture: a SER or AVI file of Frames frames (0:
// until stopped), 8 or 16 bits, of the region Roi (all of the frame when
// its size is zero), in image pixels.
type VideoRequest struct {
	Name      string
	Container string
	Roi       struct{ X, Y, W, H int }
	BitDepth  int
	Frames    int
}

// CaptureVideo records a video (TaskController/CaptureVideo), as the web
// client allows only for exposures under 0.1 s; HaltTasks stops it.
func (a *App) CaptureVideo(v VideoRequest) error {
	if v.Container != "ser" && v.Container != "avi" {
		return fmt.Errorf("video container %q is not ser or avi", v.Container)
	}
	if v.BitDepth != 8 && v.BitDepth != 16 {
		return fmt.Errorf("video bit depth %d is not 8 or 16", v.BitDepth)
	}
	if v.Frames < 0 || v.Frames > 9999 {
		return fmt.Errorf("video frames %d outside 1–9999", v.Frames)
	}
	if v.Roi.X < 0 || v.Roi.Y < 0 || v.Roi.W < 0 || v.Roi.H < 0 {
		return errors.New("video region outside the image")
	}
	name := v.Name
	if name == "" {
		name = "Video"
	}

	return a.with(func(c *origin.Client) error {
		if err := idle(c); err != nil {
			return err
		}
		s := a.Settings()
		p := map[string]any{"ObjectName": origin.FolderName(name), "Container": v.Container, "BitDepth": v.BitDepth}
		if s.ManualImaging && s.Snapshot {
			_, exp, iso := a.capture(c)
			if exp >= 0.1 {
				return errors.New("Capture Video: video needs an exposure under 0.1 s")
			}
			p["Exposure"], p["Iso"] = exp, iso
		}
		if v.Frames > 0 {
			p["Frames"] = v.Frames
		}
		if v.Roi.W > 0 && v.Roi.H > 0 {
			p["RoiStartX"], p["RoiStartY"], p["RoiWidth"], p["RoiHeight"] = v.Roi.X, v.Roi.Y, v.Roi.W, v.Roi.H
		}
		_, err := c.Send(context.Background(), "TaskController", "CaptureVideo", p)

		return err
	})
}

// FactoryReset resets the Origin's settings to the factory's
// (System/RunFactoryReset, as the web client: its development and
// settings partitions kept), after which it restarts; Nightglass
// reconnects as after a reboot.
func (a *App) FactoryReset() error {
	c, err := a.cl()
	if err != nil {
		return err
	}
	if err := a.with(func(c *origin.Client) error {
		_, err := c.Send(context.Background(), "System", "RunFactoryReset", map[string]any{"Dev": false, "Settings": false})

		return err
	}); err != nil {
		return err
	}
	a.reconnectAfter(c, a.restartDelay, true)

	return nil
}
