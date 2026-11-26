// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Command origin-fake runs a simulated Origin for developing the GUI
// without a telescope: status, go-to, live frames, imaging with stacked
// frames, a final TIFF master on stop, snapshots, reframing, the image
// folders with USB copies, and the advanced settings.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/dkorunic/nightglass/origin/fake"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "address to serve on")
	live := flag.Duration("live", 2*time.Second, "live frame interval (0 disables)")
	push := flag.Bool("push", true, "push module status as a real Origin does")
	ping := flag.Duration("ping", 5*time.Second, "WebSocket ping interval (0 disables)")
	close1011 := flag.Duration("close1011", 0, `close every connection with 1011 "Ping timeout" this often (0 never)`)
	announceIv := flag.Duration("announce", 2*time.Second, "UDP discovery announcement interval (0 disables)")
	reboot := flag.Duration("reboot", 20*time.Second, "how long System/Reboot keeps the fake down")
	elpanel := flag.Bool("elpanel", false, "report a connected flat panel (ElPanel)")
	survey := flag.String("sky", "", "HiPS survey for frames via CDS hips2fits, e.g. CDS/P/DSS2/color, or legacy:ls-dr10 via the Legacy Survey viewer; a comma-separated list uses the first covering each field, e.g. CDS/P/PanSTARRS/DR1/color-z-zg-g,CDS/P/DSS2/color (empty: procedural stars)")
	flag.Parse()
	if l := strings.Split(*survey, ","); slices.ContainsFunc(l[:len(l)-1], func(s string) bool { return strings.HasPrefix(s, "legacy:") }) {
		log.Fatal("-sky: a legacy: survey can only come last (its gaps cannot be told from sky)")
	}
	s, err := fake.New(*listen)
	if err != nil {
		log.Fatal(err)
	}
	sim := newSim(s, 3*time.Second)
	sim.rebootTime = *reboot
	sim.survey = *survey
	captured(s)
	if *elpanel {
		s.SetStatus("ElPanel", fake.Msg{"Connected": true, "LightOn": false, "BrightnessLevel": 50})
	}
	s.PingInterval = *ping
	fmt.Printf("fake Origin at %s (connect the app to this address)\n", s.Host())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go sim.mount.run(ctx, 250*time.Millisecond)
	if *live > 0 {
		go sim.liveFeed(ctx, *live)
	}
	if *announceIv > 0 {
		if err := announce(ctx, s.Host(), announceTo(s.Host()), *announceIv, s.Down); err != nil {
			log.Printf("discovery announcements off: %v", err)
		}
	}
	if *push {
		s.OnConnect = func() { pushAll(s) }
		go pushStatus(ctx, s)
	}
	if *close1011 > 0 {
		go func() {
			t := time.NewTicker(*close1011)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					s.CloseAllWith(websocket.StatusInternalError, "Ping timeout")
				}
			}
		}()
	}
	<-ctx.Done()
	s.Close()
}

// captured sets status values seen in packet captures of a real Origin
// (firmware 1.1.4248, as recorded by the OriginSimulator community project):
// facts about the telescope, not code.
func captured(s *fake.Server) {
	s.SetStatus("Mount", fake.Msg{
		"BatteryLevel": "HIGH", "BatteryVoltage": 10.38, "ChargerStatus": "CHARGING", "NumAlignRefs": 3,
		"Enc0": 0.0, "Enc1": 0.0, "TimeZone": "Europe/London",
	})
	s.SetStatus("Focuser", fake.Msg{
		"Position": 18447, "Backlash": 255, "CalibrationLowerLimit": 1975, "CalibrationUpperLimit": 37527,
		"IsCalibrationComplete": true, "PercentageCalibrationComplete": 100, "NeedAutoFocus": false, "RequiresCalibration": false,
	})
	s.SetStatus("Camera", fake.Msg{
		"Binning": 1, "BitDepth": 24, "ColorRBalance": 78.0, "ColorGBalance": 58.0, "ColorBBalance": 120.0,
		"Exposure": 0.5, "ISO": 2000, "Offset": 0,
	})
	s.SetStatus("Environment", fake.Msg{
		"AmbientTemperature": 15.988, "CameraTemperature": 24.3, "CpuTemperature": 42.842, "DewPoint": 8.108,
		"FrontCellTemperature": 11.35, "Humidity": 67.0,
	})
	s.SetStatus("Disk", fake.Msg{"Capacity": int64(58281033728), "FreeBytes": int64(52705251328), "Level": "OK"})
	s.SetInfo("System/GetVersion", fake.Msg{
		"Number": "1.1.4248", "Version": "1.1.4248\n (C++ = 09-04-2024 18:19, Java = 09-04-2024 18:19)",
	})
}

// pushes are how often each module's status is pushed: a real Origin
// pushes them unasked; these intervals are the simulator project's.
var pushes = []struct {
	module string
	every  time.Duration
}{
	{"Mount", time.Second},
	{"Focuser", 2 * time.Second},
	{"Camera", 3 * time.Second},
	{"TaskController", 5 * time.Second},
	{"Environment", 10 * time.Second},
	{"Disk", 10 * time.Second},
	{"DewHeater", 15 * time.Second},
}

// pushAll pushes every module's status, as an Origin does just after a
// client connects.
func pushAll(s *fake.Server) {
	time.Sleep(time.Second)
	for _, p := range pushes {
		s.NotifyStatus(p.module)
	}
}

// pushStatus pushes each module's status on its interval until ctx ends.
func pushStatus(ctx context.Context, s *fake.Server) {
	for _, p := range pushes {
		go func() {
			t := time.NewTicker(p.every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if p.module == "Environment" {
						jitter(s)
					}
					s.NotifyStatus(p.module)
				}
			}
		}()
	}
}

// sim gives the fake's default answers some behavior, following the app's
// imaging protocol: RunImaging starts a run named by its Uuid; HaltTasks
// ends it and keeps its master; GetFinalStackedMaster delivers the master;
// CancelImaging ends it and discards it.
type sim struct {
	s          *fake.Server
	run        context.CancelFunc // the imaging run, nil when idle
	kept       map[string]string  // halted runs' masters: uuid -> name
	copying    context.CancelFunc // a USB copy in progress, nil when none
	halt       context.CancelFunc // a task HaltTasks ends, nil when none
	mosaics    map[string]*simMosaic
	fields     map[string]fake.Sky // the field each kept master shows, by Uuid
	grids      map[string][2]int   // a composition\'s tile grid, by its Uuid
	mount      *mount
	focusStop  context.CancelFunc // the focuser move under way, nil when none
	country    string             // the last Network/SetRegulatoryDomain
	uuid       string             // the running run
	name       string             // its object name
	survey     string             // HiPS survey frames are cut from, "" for procedural stars
	rebootTime time.Duration      // how long a reboot keeps the fake down
	seed       atomic.Uint64      // noise seeds, one per frame
	stackIv    time.Duration
	frame      int
	step       time.Duration // pace of snapshots, centering and USB copies
	mu         sync.Mutex
}

func newSim(s *fake.Server, stackIv time.Duration) *sim {
	m := &sim{s: s, stackIv: stackIv, kept: map[string]string{}, step: 500 * time.Millisecond, mosaics: map[string]*simMosaic{}, fields: map[string]fake.Sky{}, grids: map[string][2]int{}}
	m.mount = newMount(s, 51.48, 0, time.Now) // Greenwich until RunInitialize says where
	s.Handle = m.handle

	return m
}

//nolint:maintidx // one case per simulated command
func (m *sim) handle(req fake.Msg) fake.Msg {
	r := m.s.DefaultHandle(req)
	key := fmt.Sprint(req["Destination"], "/", req["Command"])
	if m.mosaicMsg(key, req, r) {
		return r
	}
	switch key {
	case "ImageServer/GetThumbnail":
		go m.thumbnail(fmt.Sprint(req["ImageUuid"]))
	case "System/Update":
		go m.update()
	case "TaskController/RunInitialize":
		m.mount.site(req["Latitude"], req["Longitude"])
		m.s.SetStatus("TaskController", fake.Msg{
			"InitializationInfo": fake.Msg{"PercentageComplete": 50, "CurrentStep": "ALIGNING"}, "Stage": "IN_PROGRESS",
		})
		m.state("INITIALIZING", "")
		go func() {
			time.Sleep(4 * m.step)
			m.s.SetStatus("Mount", fake.Msg{"IsAligned": true, "NumAlignRefs": 3})
			m.mount.setTracking(true)
			m.s.SetStatus("TaskController", fake.Msg{"Stage": "COMPLETE", "IsReady": true, "IsFakeInitialized": req["FakeInitialize"] == true})
			m.state("INITIALIZING", "")
			m.state("IDLE", "")
		}()
	case "TaskController/PolarAlign":
		m.task("POLAR_ALIGNING", fake.Msg{"PolarAlignInfo": fake.Msg{"AzimuthError": 0.02, "AltitudeError": -0.01}}, 0)
	case "TaskController/RunGenerateNewDarks":
		m.task("GENERATING_DARKS", fake.Msg{"DarkGenerationInfo": fake.Msg{"PercentageComplete": 0}}, 4*m.step)
	case "TaskController/RunGenerateNewFlat":
		m.task("GENERATING_FLATS", nil, 4*m.step)
	case "System/SetAutomaticUpdates":
		m.setInfo("System/GetAutomaticUpdates", req, "Enabled")
	case "System/SetUpdateChannel":
		m.setInfo("System/GetUpdateChannel", req, "Value")
	case "Mount/GotoRaDec":
		ra, _ := req["Ra"].(float64)
		dec, _ := req["Dec"].(float64)
		m.mount.gotoRaDec(ra, dec)
	case "Mount/Slew":
		alt, _ := req["AltRate"].(float64)
		azm, _ := req["AzmRate"].(float64)
		m.mount.slew(int(alt), int(azm), false)
	case "Mount/AbortAxisMovement":
		m.mount.slew(0, 0, true)
	case "Mount/EnableTracking":
		on, _ := req["Value"].(bool)
		m.mount.setTracking(on)
	case "Camera/SetCaptureParameters":
		// Reported back by the Camera status, as an Origin does.
		m.setStatus("Camera", req, "Exposure", "ISO", "Binning", "Offset", "ColorRBalance", "ColorGBalance", "ColorBBalance")
		m.s.NotifyStatus("Camera")
	case "System/Reboot":
		go m.powerCycle(true)
	case "System/PowerDownOrigin":
		go m.powerCycle(false)
	case "ElPanel/TurnOnLight", "ElPanel/TurnOffLight", "ElPanel/SetLightLevel":
		m.elPanel(key, req, r)
	case "Environment/RecalibrateEnvironmentSensor":
		m.s.SetStatus("Environment", fake.Msg{"Recalibrating": true})
		m.s.NotifyStatus("Environment")
		go func() {
			time.Sleep(4 * m.step)
			m.s.SetStatus("Environment", fake.Msg{"Recalibrating": false})
			m.s.NotifyStatus("Environment")
		}()
	case "Network/SetRegulatoryDomain":
		// Nothing reads it back: shown so the country sent can be checked.
		m.mu.Lock()
		m.country = fmt.Sprint(req["CountryCode"])
		m.mu.Unlock()
		log.Printf("regulatory domain set to %v", req["CountryCode"])
	case "Focuser/MoveTo":
		if v, ok := req["Value"].(float64); ok {
			go m.moveFocuser(m.startFocuser(context.Background()), v)
		}
	case "TaskController/RunInfinityAutoFocus":
		start, _ := req["StartingPosition"].(float64)
		m.autofocus(start, false)
	case "TaskController/RunTerrestrialAutoFocus":
		m.autofocus(0, true)
	case "TaskController/RunImaging":
		if r["ErrorCode"] == 0 {
			uuid := fmt.Sprint(req["Uuid"])
			m.startImaging(uuid, fmt.Sprint(req["Name"]))
			if d, ok := req["TotalDuration"].(float64); ok && d > 0 {
				// A timed run: the Origin ends it itself and keeps its master.
				time.AfterFunc(time.Duration(d*float64(time.Second)), func() {
					m.mu.Lock()
					current := m.uuid == uuid
					m.mu.Unlock()
					if current {
						m.stopImaging(true)
					}
				})
			}
		}
	case "TaskController/HaltTasks":
		m.stopImaging(true)
		m.mu.Lock()
		if m.halt != nil { // a polar alignment or calibration task
			m.halt()
			m.halt = nil
		}
		m.mu.Unlock()
		m.mu.Lock()
		if m.copying != nil {
			m.copying()
			m.copying = nil
		}
		m.mu.Unlock()
	case "TaskController/RunSampleCapture":
		m.state("SAMPLE_CAPTURE", "")
		go func() {
			time.Sleep(m.step)
			loc := m.tempFile()
			f := m.skyHere(fullW, fullH, 1)
			m.s.SetFile(loc, f.JPEG())
			m.s.Notify("ImageServer", "NewImageReady", withField(fake.Msg{"FileLocation": loc, "ImageType": "SAMPLE_CAPTURE"}, f))
			m.state("IDLE", "")
		}()
	case "TaskController/RunCenterTarget":
		// The point clicked becomes the centre: OffsetX, OffsetY are
		// fractions of the image (the rotation is taken as fake.Sky's).
		x, _ := req["OffsetX"].(float64)
		y, _ := req["OffsetY"].(float64)
		ra, dec := m.skyHere(fullW, fullH, 1).At(x, y)
		m.mount.gotoRaDec(ra*math.Pi/180, dec*math.Pi/180)
		m.s.SetStatus("TaskController", fake.Msg{"CenteringInfo": fake.Msg{"CenteringStatus": "SLEWING"}})
		m.state("CENTERING_TARGET", "")
		go func() {
			for end := time.Now().Add(10 * time.Second); m.mount.moving() && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
				m.mount.step()
			}
			m.state("IDLE", "")
		}()
	case "ImageServer/GetListOfAvailableDirectories":
		var dirs []any
		m.mu.Lock()
		for uuid, name := range m.kept {
			dirs = append(dirs, dirName(uuid, name))
		}
		m.mu.Unlock()
		r["DirectoryList"] = dirs
	case "ImageServer/GetStackedMasterFromDirectory":
		uuid, ok := m.dirUuid(fmt.Sprint(req["DirectoryName"]))
		r["FileExists"] = ok
		if ok {
			m.deliverFinal(uuid)
		}
	case "ImageServer/DeleteImageDirectory":
		if uuid, ok := m.dirUuid(fmt.Sprint(req["DirectoryName"])); ok {
			m.mu.Lock()
			delete(m.kept, uuid)
			m.mu.Unlock()
		}
	case "ImageServer/DeleteAllImageDirectories":
		m.mu.Lock()
		clear(m.kept)
		m.mu.Unlock()
	case "System/GetLogs":
		// The Origin bundles its logs, reporting progress, then names the file.
		go func() {
			for _, pct := range []int{30, 70, 100} {
				time.Sleep(m.step)
				m.s.Notify("TaskController", "FileTransferStatus", fake.Msg{"PercentageComplete": pct})
			}
			m.s.SetFile("logs/origin-logs.tar.gz", []byte("origin-fake log bundle\n"))
			m.s.Notify("System", "LogsReady", fake.Msg{"DownloadLink": "logs/origin-logs.tar.gz"})
		}()
	case "TaskController/CopyDirectoryToFlashDrive", "TaskController/CopyAllDirectoriesToFlashDrive":
		m.copyToUSB()
	case "ImageServer/SetStretch":
		m.setInfo("ImageServer/GetStretch", req, "Background", "Strength")
	case "LedRing/SetBrightnessLevel":
		m.setInfo("LedRing/GetBrightnessLevel", req, "BrightnessLevel")
	case "LampController/SetBrightnessLevel":
		m.setInfo("LampController/GetBrightnessLevel", req, "BrightnessLevel")
	case "Mount/SetMountConfig":
		m.setInfo("Mount/GetMountConfig", req, "OnWedge", "IsEquatorial")
	case "Focuser/AutoFocusAfterGoto":
		m.s.SetInfo("Focuser/GetFocuserAdvancedSettings", fake.Msg{"AutoFocusAfterGotoIsEnabled": req["Value"]})
	case "Focuser/AutoFocusOnTemperatureChange":
		v := fake.Msg{"TemperatureShiftAutoFocusIsEnabled": req["EnableTemperatureShiftAutoFocus"]}
		if t, ok := req["TemperatureThresholdC"]; ok {
			v["ThresholdTemperatureC"] = t
		}
		m.s.SetInfo("Focuser/GetFocuserAdvancedSettings", v)
	case "Environment/SetFans":
		m.setStatus("Environment", req, "OtaFanOn", "CpuFanOn")
	case "DewHeater/EnableAuto":
		m.s.SetStatus("DewHeater", fake.Msg{"Mode": "Auto", "Aggression": req["Aggression"]})
	case "DewHeater/EnableManual":
		m.s.SetStatus("DewHeater", fake.Msg{"Mode": "Manual", "ManualPowerLevel": req["PowerLevel"], "HeaterLevel": req["PowerLevel"]})
	case "TaskController/CancelImaging":
		m.stopImaging(false)
	case "ImageServer/GetFinalStackedMaster":
		uuid := fmt.Sprint(req["ImageUuid"])
		// The app sends HaltTasks and this back to back; the Origin handles
		// them in order, so the run asked about is ended first here too.
		m.mu.Lock()
		running := m.uuid == uuid
		m.mu.Unlock()
		if running {
			m.stopImaging(true)
		}
		m.mu.Lock()
		_, ok := m.kept[uuid]
		m.mu.Unlock()
		r["FileExists"] = ok
		if ok {
			m.deliverFinal(uuid)
		}
	case "ImageServer/GetAvailableStackedMasters":
		avail := fake.Msg{}
		list, _ := req["UuidList"].([]any)
		m.mu.Lock()
		for _, u := range list {
			if _, ok := m.kept[fmt.Sprint(u)]; ok {
				avail[fmt.Sprint(u)] = finalFile(fmt.Sprint(u), m.kept[fmt.Sprint(u)])
			}
		}
		m.mu.Unlock()
		r["AvailableStackedMasters"] = avail
	case "Camera/SetFilter":
		m.s.SetInfo("Camera/GetFilter", fake.Msg{"Filter": req["Filter"]})
	case "LiveStream/SetEnableAuto", "LiveStream/SetEnableManual":
		m.s.SetInfo("LiveStream/GetEnableManual", fake.Msg{"IsManual": req["Command"] == "SetEnableManual"})
	}

	return r
}

// task runs state with fields until HaltTasks, or for d when d > 0, then
// returns to IDLE.
func (m *sim) task(state string, fields fake.Msg, d time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.halt != nil {
		m.halt()
	}
	m.halt = cancel
	m.mu.Unlock()
	if fields != nil {
		m.s.SetStatus("TaskController", fields)
	}
	m.state(state, "")
	go func() {
		var done <-chan time.Time
		if d > 0 {
			done = time.After(d)
		}
		select {
		case <-ctx.Done():
		case <-done:
		}
		m.state("IDLE", "")
	}()
}

// deliverFinal serves a kept run's final master and announces it.
func (m *sim) deliverFinal(uuid string) {
	m.mu.Lock()
	name := m.kept[uuid]
	m.mu.Unlock()
	loc := finalFile(uuid, name)
	f := m.field(uuid)
	m.s.SetFile(loc, f.TIFF())
	msg := withField(fake.Msg{"FileLocation": loc, "ImageType": "FINAL_STACKED_MASTER", "Uuid": uuid, "ObjectName": name}, f)
	m.mu.Lock()
	if g, ok := m.grids[uuid]; ok { // a composition carries its grid
		msg["MosaicGrid"] = fake.Msg{"GridWidth": g[0], "GridHeight": g[1]}
	}
	m.mu.Unlock()
	go m.s.Notify("ImageServer", "NewImageReady", msg)
}

// tempFile is the next live or snapshot frame's file: a real Origin reuses
// Images/Temp/0.jpg … 9.jpg in turn (as the OriginSimulator project records).
func (m *sim) tempFile() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frame = (m.frame + 1) % 10

	return fmt.Sprintf("Images/Temp/%d.jpg", m.frame)
}

// finalFile is a run's final master, in its folder under
// Images/Astrophotography as on a real Origin.
func finalFile(uuid, name string) string {
	return "Images/Astrophotography/" + dirName(uuid, name) + "/FinalStackedMaster.tiff"
}

// dirName is a kept run's image folder on the Origin.
func dirName(uuid, name string) string { return fmt.Sprintf("%s_%.8s", name, uuid) }

func (m *sim) dirUuid(dir string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for uuid, name := range m.kept {
		if dirName(uuid, name) == dir {
			return uuid, true
		}
	}

	return "", false
}

// copyToUSB reports a copy to the USB drive in 10 % steps; HaltTasks stops it.
func (m *sim) copyToUSB() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.copying != nil {
		m.copying()
	}
	m.copying = cancel
	m.mu.Unlock()
	go func() {
		for pct := 0; pct <= 100; pct += 10 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.step / 5):
			}
			m.s.Notify("TaskController", "FileTransferStatus", fake.Msg{"PercentageComplete": pct})
		}
	}()
}

// setInfo and setStatus copy the named fields present in req.
func (m *sim) setInfo(key string, req fake.Msg, fields ...string) {
	if v := pick(req, fields); len(v) > 0 {
		m.s.SetInfo(key, v)
	}
}

func (m *sim) setStatus(dest string, req fake.Msg, fields ...string) {
	if v := pick(req, fields); len(v) > 0 {
		m.s.SetStatus(dest, v)
	}
}

func pick(req fake.Msg, fields []string) fake.Msg {
	v := fake.Msg{}
	for _, f := range fields {
		if x, ok := req[f]; ok && x != nil {
			v[f] = x
		}
	}

	return v
}

func (m *sim) state(s, uuid string) {
	m.s.SetStatus("TaskController", fake.Msg{"State": s, "ImagingInfo": fake.Msg{"ImageUuid": uuid}})
	m.s.NotifyStatus("TaskController")
}

func (m *sim) startImaging(uuid, name string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.run != nil {
		m.run()
	}
	m.run, m.uuid, m.name = cancel, uuid, name
	m.mu.Unlock()
	m.state("IMAGING_OBJECT", uuid)
	go func() {
		t := time.NewTicker(m.stackIv)
		defer t.Stop()
		for depth := 1; ; depth++ {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// The object's stack, as TaskController reports it (10 s subs).
				m.s.SetStatus("TaskController", fake.Msg{"ImagingInfo": fake.Msg{"ImageUuid": uuid, "ObjectInfoList": []any{fake.Msg{
					"Uuid": uuid, "ObjectName": name, "StackingStatus": "RUNNING", "StackDepth": depth, "TotalTime": float64(10 * depth),
				}}}})
				m.s.NotifyStatus("TaskController")
				loc := fmt.Sprintf("Images/Astrophotography/%s/Stacked_%d.jpg", dirName(uuid, name), depth)
				f := m.skyHere(fullW, fullH, depth)
				m.mu.Lock()
				m.fields[uuid] = f // the final master shows the last stack
				m.mu.Unlock()
				m.s.SetFile(loc, f.JPEG())
				m.s.Notify("ImageServer", "NewImageReady", withField(fake.Msg{
					"FileLocation": loc, "ImageType": "STACKED_MASTER", "StackDepth": depth, "ObjectName": name, "Uuid": uuid,
				}, f))
			}
		}
	}()
}

// stopImaging ends the run; keep holds its master for GetFinalStackedMaster.
func (m *sim) stopImaging(keep bool) {
	m.mu.Lock()
	run, uuid, name := m.run, m.uuid, m.name
	m.run, m.uuid, m.name = nil, "", ""
	if run != nil && keep {
		m.kept[uuid] = name
	}
	m.mu.Unlock()
	if run == nil {
		return
	}
	run()
	m.state("IDLE", "")
}

func (m *sim) liveFeed(ctx context.Context, iv time.Duration) {
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f := m.skyHere(liveW, liveH, 1)
			loc := m.tempFile()
			m.s.SetFile(loc, f.JPEG())
			m.s.Notify("ImageServer", "NewImageReady", withField(fake.Msg{"FileLocation": loc, "ImageType": "LIVE"}, f))
		}
	}
}

// Image sizes: live frames, and snapshots and masters (the Origin's field
// shape, 1.27° × 0.85°).
const (
	liveW, liveH = 640, 429
	fullW, fullH = 1200, 804
)

// skyHere is the star field where the mount points now.
func (m *sim) skyHere(w, h, depth int) fake.Sky {
	ra, dec := m.mount.pointing()

	return fake.Sky{
		Survey: m.survey, RA: ra, Dec: dec, Width: fake.OriginField, Rotation: fake.OriginOrientation, W: w, H: h, Depth: depth,
		Blur: m.blur() * float64(w) / 1200, NoiseSeed: m.seed.Add(1),
	}
}

// withField adds where an image points to its NewImageReady, in radians,
// as a real Origin sends it.
func withField(msg fake.Msg, f fake.Sky) fake.Msg {
	const d = math.Pi / 180
	msg["Ra"], msg["Dec"], msg["Orientation"] = f.RA*d, f.Dec*d, f.Rotation*d
	msg["FovX"], msg["FovY"] = f.Width*d, f.Height()*d

	return msg
}

// field is the field a kept master shows: the one recorded for it, else
// where the mount points, as a long stack.
func (m *sim) field(uuid string) fake.Sky {
	m.mu.Lock()
	f, ok := m.fields[uuid]
	m.mu.Unlock()
	if !ok {
		f = m.skyHere(fullW, fullH, 30)
	}

	return f
}

// jitter varies the environment readings slightly before each push, as
// real sensors do, by the amounts and within the bounds the OriginSimulator
// project uses (ambient 15–17 °C, CPU 42–45 °C).
func jitter(s *fake.Server) {
	st := s.Status("Environment")
	step := func(key string, by, lo, hi float64) {
		if v, ok := st[key].(float64); ok {
			st[key] = min(hi, max(lo, v+(rand.Float64()*2-1)*by)) //nolint:gosec // sensor noise
		}
	}
	step("AmbientTemperature", 0.005, 15, 17)
	step("CpuTemperature", 0.01, 42, 45)
	step("DewPoint", 0.003, math.Inf(-1), math.Inf(1))
	s.SetStatus("Environment", st)
}

// powerCycle goes down just after answering System/Reboot or
// PowerDownOrigin, refusing connections. A reboot comes back after
// rebootTime needing initialization again; powered off stays down until
// origin-fake is restarted, as an Origin needs its power switch.
func (m *sim) powerCycle(reboot bool) {
	time.Sleep(200 * time.Millisecond) // the answer goes out first
	m.stopImaging(false)
	m.mu.Lock()
	if m.halt != nil {
		m.halt()
		m.halt = nil
	}
	m.mu.Unlock()
	if !reboot {
		m.s.SetDown(true)
		log.Print("powered down: restart origin-fake to switch it back on")

		return
	}
	// What a restarted Origin reports, set before it goes down.
	m.mount.setTracking(false)
	m.s.SetStatus("Mount", fake.Msg{"IsAligned": false, "NumAlignRefs": 0})
	m.s.SetStatus("TaskController", fake.Msg{"State": "IDLE", "IsReady": false, "Stage": "COMPLETE"})
	m.s.SetDown(true)
	log.Printf("rebooting: back in %v", m.rebootTime)
	time.Sleep(m.rebootTime)
	m.s.SetDown(false)
}

// elPanel drives a connected flat panel (the GIOTTO flat generator); one
// not connected leaves its status as it is.
func (m *sim) elPanel(key string, req, r fake.Msg) {
	if c, _ := m.s.Status("ElPanel")["Connected"].(bool); !c {
		r["ErrorCode"], r["ErrorMessage"] = -1, "no flat panel connected"

		return
	}
	switch key {
	case "ElPanel/TurnOnLight":
		m.s.SetStatus("ElPanel", fake.Msg{"LightOn": true})
	case "ElPanel/TurnOffLight":
		m.s.SetStatus("ElPanel", fake.Msg{"LightOn": false})
	default:
		m.s.SetStatus("ElPanel", fake.Msg{"BrightnessLevel": req["LightLevel"]})
	}
	m.s.NotifyStatus("ElPanel")
}
