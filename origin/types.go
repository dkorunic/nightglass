// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

// Payload structs for the fields the app reads (spec §3.5). Field names
// match the JSON keys, so they need no tags. Angles are radians.

// MountStatus is Mount/GetStatus.
type MountStatus struct {
	BatteryLevel, ChargerStatus                               string
	Date, Time, TimeZone                                      string
	Alt, Azm, Ra, Dec                                         float64
	NumAlignRefs                                              int
	BatteryVoltage, BatteryCurrent                            float64
	Latitude, Longitude                                       float64
	IsAligned, IsGotoOver, IsTracking, IsTrackingMovingTarget bool
}

// TaskStatus is TaskController/GetStatus; State is an enum name such as
// "IDLE" or "IMAGING_OBJECT".
// The per-task details are nil when absent; numbers are float64 because the
// app's JSON reader accepts any number.
type TaskStatus struct {
	CenteringInfo      *struct{ CenteringStatus string } // SLEWING, ALIGNING, FOCUSING
	InitializationInfo *struct {
		PercentageComplete float64
		CurrentStep        string // MOVING_MOUNT, ALIGNING, FOCUSING
	}
	AlignmentInfo       *struct{ NumPoints *float64 }
	PolarAlignInfo      *struct{ AzimuthError, AltitudeError float64 }
	DarkGenerationInfo  *struct{ PercentageComplete float64 }
	ComposingMosaicInfo *struct{ PercentComplete *float64 }
	// Progress the app does not read (firmware 1.5.6286 field names, as
	// Celestron's web app reads them; the spellings differ).
	FocusInfo, ZipInfo         *struct{ PercentageComplete *float64 }
	FocuserCalibrationInfo     *struct{ PercentComplete *float64 }
	FlatGenerationInfo         *struct{ PercentageCompleted *float64 }
	State, Stage               string
	ImagingInfo                ImagingInfo
	IsReady, IsFakeInitialized bool
}

// ImagingInfo is the current imaging run in TaskController/GetStatus.
type ImagingInfo struct {
	MosaicMetadata *struct {
		TilesTotal, TilesCompleted float64
		ListUuid                   string
	}
	ImageUuid, ListUuid, ListStatus   string
	ObjectInfoList                    []ObjectImagingInfo
	TotalObjects, NumObjectsRemaining int
}

// ObjectImagingInfo is one object of a run; unset numbers are nil.
type ObjectImagingInfo struct {
	StackDepth                         *float64
	TotalTime, RemainingTime           *float64 // seconds
	ObjectName, Uuid, MinimumStartTime string
	StackingStatus                     string // WAITING, MOVING_TO_OBJECT, RUNNING, …
}

// Current is the object being imaged: the ObjectInfoList entry for
// ImageUuid (Origin.getCachedObjectInfoForCurrentTarget), or nil.
func (i ImagingInfo) Current() *ObjectImagingInfo {
	for k := range i.ObjectInfoList {
		if o := &i.ObjectInfoList[k]; o.Uuid != "" && o.Uuid == i.ImageUuid {
			return o
		}
	}

	return nil
}

// FocuserStatus is Focuser/GetStatus.
type FocuserStatus struct {
	Position                            int
	Velocity                            float64
	IsMoveToOver, IsCalibrationComplete bool
}

// ImageReady is the ImageServer/NewImageReady notification.
type ImageReady struct {
	// The Origin's stretch for this master; unset or negative is the app's
	// default (0.8, 0.045).
	StretchStrength, StretchBackground *float64
	// MosaicGrid is a composed mosaic's tile grid, nil for other images.
	MosaicGrid                                            *struct{ GridWidth, GridHeight int }
	FileLocation, ImageType, ObjectName, Uuid, Date, Time string
	Ra, Dec, FovX, FovY, Orientation                      float64
	Latitude, Longitude                                   float64 // where it was taken; 0, 0 is unknown
	ExposureTime, FocusScore                              float64
	StackDepth, ISO, ImageWidth, ImageHeight              int
}

// CameraInfo is Camera/GetCameraInfo: the fields the exposure rules use,
// and the sensor (firmware 1.5.6286 keys; sizes as float64 so a "3840.0"
// still decodes) for the field before a frame reports one.
type CameraInfo struct {
	DefaultIso, MinIso, MaxIso    int
	MinExposure, MaxExposure      float64
	MaxImageWidth, MaxImageHeight float64
	PixelSize                     float64 // µm
	HasCooling                    bool    // a TEC camera (Origin-585C-TEC): Camera/SetCooling
}

// MountConfig is Mount/GetMountConfig.
type MountConfig struct {
	OnWedge, IsEquatorial bool
}
