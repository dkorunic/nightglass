// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

// commands are the commands an Origin accepts, by module: the command list
// of Origin firmware 1.5.6286's own web client (protocol names only). An
// Origin answers any other with UNKNOWN_DESTINATION (-1) for a module it
// lacks, or UNKNOWN_COMMAND (-2), and so does the fake.
var commands = map[string][]string{
	"Autoguider": {
		"GetStatus",
	},
	"Camera": {
		"GetCameraInfo", "GetCaptureParameters", "GetCooling", "GetFilter", "GetSavePlateSolves",
		"SetCaptureParameters", "SetCooling", "SetFilter", "SetSavePlateSolves",
	},
	"DewHeater": {
		"EnableAuto", "EnableManual", "GetStatus",
	},
	"Disk": {
		"GetStatus",
	},
	"ElPanel": {
		"GetLightLevel", "GetStatus", "IsLightOn", "SetLightLevel", "TurnOffLight", "TurnOnLight",
	},
	"Environment": {
		"GetFans", "GetSensors", "GetStatus", "RecalibrateEnvironmentSensor", "SetFans",
	},
	"FactoryCalibrationController": {
		"BeginFactoryCalibration", "CancelAutoCapture", "CaptureFAImage", "CheckCameraOrientation",
		"ClearFocuserLimits", "FATestCompleted", "FactoryCollimationCompleted", "FakeFactoryCalibration",
		"GenerateFAImagesZipFile", "GetCertification", "GetCurrentLocation", "GetFAImageCaptureMode", "GetMountId",
		"GetRemainingDays", "GetRtcTime", "GetSKUNumber", "GetSerialNumber", "GetStatus", "GetVersionInfo",
		"HaltTasks", "HotSpotCentered", "MoveStar", "Ping", "RunAutoFAImageCapture", "RunFactoryAutoFocus",
		"RunFocuserCalibration", "RunManualFAImageCapture", "SetSKUNumber", "SetSerialNumber",
		"ShutdownWithoutMount", "TransferFAImagesToFlashdrive", "VerifiedLedRing", "VerifiedOrientationSensor",
	},
	"Focuser": {
		"AutoFocusAfterGoto", "AutoFocusOnTemperatureChange", "GetCalibration", "GetFocuserAdvancedSettings",
		"GetPosition", "GetPositionLimits", "GetStatus", "GetVelocity", "HaltCalibration", "IsMoveToOver", "Move",
		"MoveTo",
	},
	"ImageServer": {
		"DeleteAllImageDirectories", "DeleteAllImagingLists", "DeleteFile", "DeleteImageDirectory",
		"DeleteImagingList", "GetAvailableImagingLists", "GetAvailableStackedMasters", "GetFinalStackedMaster",
		"GetFsmFormat", "GetListOfAvailableDirectories", "GetStackedMasterFromDirectory", "GetStretch",
		"GetThumbnail", "SetFsmFormat", "SetStretch",
	},
	"LampController": {
		"GetBrightnessLevel", "SetBrightnessLevel",
	},
	"LedRing": {
		"GetBrightnessLevel", "SetBrightnessLevel",
	},
	"LiveStream": {
		"CloseLiveStream", "GetDisableLiveStream", "GetEnableManual", "SetDisableLiveStream", "SetEnableAuto",
		"SetEnableManual",
	},
	"Mosaic": {
		"DeleteAllMosaics", "DeleteMosaic", "GetAvailableMosaics", "GetMaximumTiles", "GetMosaicMetadata",
		"GetMosaicTiles",
	},
	"Mount": {
		"AddAlignRef", "DeleteAllAlignRefs", "EnableTracking", "GetMountConfig", "GetPosition", "GetStatus",
		"GotoAltAzm", "GotoEnc", "GotoRaDec", "IsAligned", "IsGotoOver", "IsTracking", "SetMountConfig", "Slew",
		"TrackMovingTarget",
	},
	"Network": {
		"5GHzAccessPoint", "AddWiFiNetwork", "ConnectToSpecificNetwork", "DeleteWiFiNetwork", "ForceDirectConnect",
		"Get5GHzAccessPoint", "GetDirectConnectPassword", "GetForceDirectConnect", "GetStatus", "GetVisibleWiFi",
		"GetWiFiNetworks", "HasInternetConnection", "ReconnectDirectConnect", "ReconnectWiFi",
		"RemoveAllKnownNetworks", "SetDirectConnectPassword", "SetRegulatoryDomain",
	},
	"Optics": {
		"GetConfiguration", "GetSupportedModels", "SetConfiguration",
	},
	"OrientationSensor": {
		"GetAccelerometer", "GetStatus", "ZeroCalibrate",
	},
	"System": {
		"EnableCustomerMode", "EnableFTPAccess", "GetAutomaticUpdates", "GetLogs", "GetModel", "GetUpdateChannel",
		"GetVersion", "HasUpdateAvailable", "PowerDownOrigin", "Reboot", "RunFactoryReset", "SetAutomaticUpdates",
		"SetUpdateChannel", "ShutDownCore", "SwapBootPartition", "Update",
	},
	"TaskController": {
		"CalibrateFocuser", "CancelImaging", "CaptureVideo", "CopyAllDirectoriesToFlashDrive",
		"CopyDirectoryToFlashDrive", "GetObservingListInfo", "GetStatus", "HaltTasks", "ImageNextTarget",
		"MeasureDarkCurrent", "PolarAlign", "RecaptureImagingListObject", "RecaptureMosaicTile",
		"ResetManualFocus", "RestackImage", "RestoreDarksToFactory", "RestoreFlatsToFactory", "ResumeImagingList",
		"RunAddReference", "RunCaptureMosaic", "RunCenterTarget", "RunComposeMosaic", "RunGenerateAmplitudeVsGain",
		"RunGenerateNewDarks", "RunGenerateNewFlat", "RunImaging", "RunInfinityAutoFocus", "RunInitialize",
		"RunManualFocus", "RunObservingList", "RunSampleCapture", "RunTemperatureShiftAutoFocus",
		"RunTerrestrialAutoFocus", "ZipDirectory",
	},
}
