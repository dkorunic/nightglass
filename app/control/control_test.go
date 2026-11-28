// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

// zagreb at 2026-01-15 21:00 UTC: M 42 stands at 38.7°.
var evening = time.Date(2026, 1, 15, 21, 0, 0, 0, time.UTC)

type recorder struct {
	mu  sync.Mutex
	got []struct {
		name string
		data any
	}
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, struct {
		name string
		data any
	}{name, data})
}

// wait returns the first event named name for which ok holds.
func (r *recorder) wait(t *testing.T, name string, ok func(any) bool) any {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		r.mu.Lock()
		for _, e := range r.got {
			if e.name == name && ok(e.data) {
				r.mu.Unlock()

				return e.data
			}
		}
		r.mu.Unlock()
	}
	t.Fatalf("no %q event", name)

	return nil
}

// newApp makes an App with settings in a temp dir, a fixed clock and a
// Zagreb location.
func newApp(t *testing.T) (*App, *recorder) {
	t.Helper()
	rec := &recorder{}
	dir, saveDir := tempDir(t), tempDir(t)
	a, err := New(filepath.Join(dir, "settings.json"), rec.emit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Disconnect) // runs before the folders are removed
	a.now = func() time.Time { return evening }
	a.thumbDir = filepath.Join(dir, "thumbs")
	a.tune = func(c *origin.Client) { c.MinBackoff, c.PollInterval = 10*time.Millisecond, -1 }
	a.discover = func(context.Context) ([]string, error) { return nil, nil } // no network search
	a.inhibit = func(string, bool) func() { return func() {} }               // no OS sleep inhibitor
	s := a.Settings()
	s.LatDeg, s.LonDeg, s.TimeZone, s.SaveDir = 45.815, 15.982, "Europe/Zagreb", saveDir
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}

	return a, rec
}

// tempDir is t.TempDir for folders background work (onConnected, automatic
// saves) may still write to as the test ends: removing it is retried
// rather than failing the test.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nightglass-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; os.RemoveAll(dir) != nil && i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
		}
	})

	return dir
}

func connected(t *testing.T) (*App, *recorder, *fake.Server) {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	// After a drop the app searches anew; the search finds this telescope.
	a.discover = func(context.Context) ([]string, error) { return []string{s.Host()}, nil }
	if err := a.Connect(" http://" + s.Host() + "/ "); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
	c, _ := a.cl()
	// The task state and the camera's limits, as most tests rely on them.
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, ok := c.CameraInfo(); ok && c.TaskState() != "" {
			break
		}
	}

	return a, rec, s
}

// Corrupt settings reach the user as a startup warning, not a crash.
func TestNewWithCorruptSettings(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o644)
	a, err := New(bad, func(string, any) {})
	if err != nil || len(a.Warnings()) != 1 || a.Settings().ISO != 0 {
		t.Errorf("New with corrupt settings: %v %v", a.Warnings(), err)
	}
}

func TestSaveSettingsRoundTrip(t *testing.T) {
	a, _ := newApp(t)
	s, err := LoadSettings(a.settingsPath)
	if err != nil || s.LatDeg != 45.815 || s.TimeZone != "Europe/Zagreb" {
		t.Errorf("reloaded %+v %v", s, err)
	}
	if err := a.SaveSettings(Settings{LatDeg: 100}); err == nil {
		t.Error("invalid settings saved")
	}
}

func TestCatalogsTargetsPositions(t *testing.T) {
	a, _ := newApp(t)
	total := 0
	for _, c := range a.Catalogs() {
		total += c.Count
	}
	// The 34 lists and the Moon and planets.
	if len(a.Catalogs()) != 35 || total != 44028 {
		t.Fatalf("%d catalogs, %d entries", len(a.Catalogs()), total)
	}
	if n := len(a.Targets("")); n != 44028 {
		t.Errorf("all: %d rows", n)
	}
	rows := a.Targets("Messier")
	pos := a.Positions("Messier")
	if len(rows) != 110 || len(pos) != 220 {
		t.Fatalf("Messier: %d rows, %d positions", len(rows), len(pos))
	}
	if a.Settings().Catalog != "Messier" {
		t.Error("catalog choice not remembered")
	}
	for i, r := range rows {
		if r.Name == "M 42" && math.Abs(float64(pos[2*i])-38.715) > 0.02 {
			t.Errorf("M 42 altitude %v, want 38.715", pos[2*i])
		}
	}
}

func TestGotoChecksWindow(t *testing.T) {
	a, _, s := connected(t)
	if err := a.Goto(83.85, -5.45); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Wait(fake.Is("Mount", "GotoRaDec"), time.Second)
	if !ok || math.Abs(m["Ra"].(float64)-83.85*math.Pi/180) > 1e-12 {
		t.Errorf("goto payload %v", m)
	}
	// Dec -80 never rises at 45.8° N.
	if err := a.Goto(0, -80); err == nil || !strings.Contains(err.Error(), "Command Failure: The scope can't slew to the target coordinates because they are below the horizon.") {
		t.Errorf("below horizon: err %v", err)
	}
	var gotos int
	for _, m := range s.Received() {
		if fake.Is("Mount", "GotoRaDec")(m) {
			gotos++
		}
	}
	if gotos != 1 {
		t.Errorf("%d gotos sent, want 1", gotos)
	}
}

func TestNotConnected(t *testing.T) {
	a, _ := newApp(t)
	if err := a.Goto(83.85, -5.45); err != ErrNotConnected {
		t.Errorf("err %v, want ErrNotConnected", err)
	}
}

// A frame is fetched and served at /frame/<kind>; a failed download warns
// and keeps the previous frame.
func TestFramesAndFailedDownload(t *testing.T) {
	a, rec, s := connected(t)
	jpg := fake.JPEG(3)
	s.SetFile("live/1.jpg", jpg)
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/1.jpg", "ImageType": "LIVE"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "live" && d.(FrameEvent).Seq == 1 })

	get := func() []byte {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "/live", nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("GET /live: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
		b, _ := io.ReadAll(w.Body)

		return b
	}
	if !bytes.Equal(get(), jpg) {
		t.Fatal("served frame differs")
	}
	// The app only logs a failed download: no toast, and the frame stays.
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "live/missing.jpg", "ImageType": "LIVE"})
	time.Sleep(200 * time.Millisecond)
	if n := countEvents(rec, "toast"); n != 0 {
		t.Errorf("%d toasts for a failed download", n)
	}
	if !bytes.Equal(get(), jpg) {
		t.Error("previous frame lost after a failed download")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/final", nil))
	if w.Code != 404 {
		t.Errorf("no final yet: %d", w.Code)
	}
}

func TestStatusForwarded(t *testing.T) {
	_, rec, s := connected(t)
	s.Notify("Mount", "GetStatus", fake.Msg{"Alt": 0.5})
	rec.wait(t, "status", func(d any) bool {
		st := d.(Status)

		return st.Key == "Mount/GetStatus" && st.Fields["Alt"] == 0.5
	})
}

// A drop mid-slew may leave the mount moving: the first thing after the
// reconnect is a stop.
func TestSlewStoppedAfterReconnect(t *testing.T) {
	a, rec, s := connected(t)
	if err := a.Slew(4, 0); err != nil {
		t.Fatal(err)
	}
	// The slew is posted without waiting: count it as before the drop.
	if _, ok := s.Wait(fake.Is("Mount", "Slew"), time.Second); !ok {
		t.Fatal("slew not sent")
	}
	dropped := len(s.Received())
	s.DropAll()
	rec.wait(t, "conn", func(d any) bool { return !d.(Conn).Connected && d.(Conn).Host != "" })
	stop := func(m fake.Msg) bool {
		return fake.Is("Mount", "Slew")(m) && m["AltRate"] == 0.0 && m["AzmRate"] == 0.0
	}
	if _, ok := s.Wait(stop, 3*time.Second); !ok {
		t.Fatal("no Slew 0,0 after reconnect")
	}
	// The stop is the first motion command on the new link.
	for _, m := range s.Received()[dropped:] {
		if cmd, _ := m["Command"].(string); m["Destination"] == "Mount" && !strings.HasPrefix(cmd, "Get") {
			if !stop(m) {
				t.Errorf("first motion command after reconnect was %v, want Slew 0,0", m)
			}

			break
		}
	}
}

// Focus jog (Celestron's web app's Move In/Out): Focuser/Move with Value
// -3..3 (positive moves in) while held, 0 to stop, only while idle. Like a
// slew, a jog cut off by a drop is stopped first thing after the reconnect.
func TestFocusJog(t *testing.T) {
	a, rec, s := connected(t)
	if err := a.FocusJog(2); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Focuser", "Move"); m["Value"] != 2.0 {
		t.Errorf("Move %v", m)
	}
	if err := a.FocusJog(0); err != nil {
		t.Fatal(err)
	}
	if m := lastSent(t, s, "Focuser", "Move"); m["Value"] != 0.0 {
		t.Errorf("stop %v", m)
	}
	if a.FocusJog(4) == nil || a.FocusJog(-4) == nil {
		t.Error("an out-of-range jog accepted")
	}
	if err := a.FocusJog(-3); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Wait(func(m fake.Msg) bool { return fake.Is("Focuser", "Move")(m) && m["Value"] == -3.0 }, time.Second); !ok {
		t.Fatal("jog out not sent")
	}
	dropped := len(s.Received())
	s.DropAll()
	rec.wait(t, "conn", func(d any) bool { return !d.(Conn).Connected && d.(Conn).Host != "" })
	// The first focuser move on the new link is the stop.
	var first fake.Msg
	for end := time.Now().Add(3 * time.Second); first == nil && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		for _, m := range s.Received()[dropped:] {
			if fake.Is("Focuser", "Move")(m) {
				first = m

				break
			}
		}
	}
	if first == nil || first["Value"] != 0.0 {
		t.Fatalf("first focuser move after reconnect %v, want Move 0", first)
	}
	setTask(t, a, s, fake.Msg{"State": "FOCUSING"})
	if a.FocusJog(1) == nil {
		t.Error("jog while busy accepted")
	}
}

// An Origin set to save its final image as JPEG 2000 (SetFsmFormat "jp2")
// sends one Nightglass cannot show: the file is kept as it came, and the
// toast says so.
func TestFinalJPEG2000Kept(t *testing.T) {
	a, rec, s := connected(t)
	jp2 := []byte("\x00\x00\x00\x0cjP  \r\n\x87\nrest of the file")
	s.SetFile("FinalStackedMaster.jp2", jp2)
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "FinalStackedMaster.jp2", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "M 42"})
	rec.wait(t, "toast", func(d any) bool { return strings.Contains(d.(Toast).Message, "JPEG 2000") })
	files, _ := filepath.Glob(filepath.Join(a.Settings().SaveDir, "*.jp2"))
	if len(files) != 1 {
		t.Fatalf("saved %v", files)
	}
	if b, _ := os.ReadFile(files[0]); !bytes.Equal(b, jp2) || filepath.Base(files[0]) != "M_42-20260115-220000.jp2" {
		t.Errorf("%s: kept %q", files[0], b)
	}
}

func TestSaveFinal(t *testing.T) {
	a, rec, s := connected(t)
	if _, err := a.SaveFinal(); err == nil {
		t.Error("SaveFinal with no image: want error")
	}
	tif := fake.TIFF16()
	s.SetFile("final.tiff", tif)
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER", "ObjectName": "M 42"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "final" })
	// The final image is saved as it arrives; saving it again with the
	// test's fixed clock makes a second file beside it.
	rec.wait(t, "toast", func(d any) bool { return strings.HasPrefix(d.(Toast).Message, "Saved ") })
	p, err := a.SaveFinal()
	if err != nil || filepath.Base(p) != "Orion_Nebula-20260115-220000-1.png" {
		t.Fatalf("SaveFinal: %s %v", p, err)
	}
	// Saved as the app saves it: the TIFF as a PNG, with its metadata.
	b, _ := os.ReadFile(p)
	if !bytes.HasPrefix(b, []byte("\x89PNG")) || !bytes.Contains(b, []byte("eXIf")) {
		t.Error("saved file is not a PNG with EXIF")
	}
}

// While the telescope answers on TCP but its WebSocket does not open (an
// Origin still starting), the dials are retried, as the app's WebSocket
// library does, the outage is reported once and no failure is shown.
func TestOutageReportedOnce(t *testing.T) {
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	web := httptest.NewServer(http.NotFoundHandler()) // TCP yes, WebSocket no
	t.Cleanup(web.Close)
	if err := a.Connect(strings.TrimPrefix(web.URL, "http://")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // about ten 10 ms+ backoff dials
	down := 0
	rec.mu.Lock()
	for _, e := range rec.got {
		if c, ok := e.data.(Conn); ok && e.name == "conn" && !c.Connected && c.Host != "" {
			down++
		}
	}
	rec.mu.Unlock()
	if down != 1 {
		t.Errorf("%d disconnected events during one outage, want 1", down)
	}
	if countEvents(rec, "alert") != 0 {
		t.Error("failure shown while the WebSocket is retried")
	}
	a.mu.Lock()
	dialling := a.client != nil
	a.mu.Unlock()
	if !dialling {
		t.Error("gave up dialling")
	}
}

// A replaced client's state changes must not reach the GUI or act on the
// mount.
func TestReplacedClientEventsIgnored(t *testing.T) {
	a, rec, s := connected(t)
	a.mu.Lock()
	old := a.client
	a.mu.Unlock()
	s2, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	if err := a.Connect(s2.Host()); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	before := len(rec.got)
	rec.mu.Unlock()
	old.OnState(true, nil) // a dial of the old client finishing late
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, e := range rec.got[before:] {
		if c, ok := e.data.(Conn); ok && c.Host == s.Host() {
			t.Errorf("stale event from the replaced client: %+v", c)
		}
	}
}

// Frames belong to one telescope: connecting to another forgets them.
func TestConnectClearsFrames(t *testing.T) {
	a, rec, s := connected(t)
	s.SetFile("final.tiff", fake.TIFF16())
	s.Notify("ImageServer", "NewImageReady", fake.Msg{"FileLocation": "final.tiff", "ImageType": "FINAL_STACKED_MASTER"})
	rec.wait(t, "frame", func(d any) bool { return d.(FrameEvent).Kind == "final" })
	// Its automatic save done, so the temporary directory can be removed.
	rec.wait(t, "toast", func(d any) bool { return strings.HasPrefix(d.(Toast).Message, "Saved ") })
	s2, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	if err := a.Connect(s2.Host()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveFinal(); err == nil {
		t.Error("SaveFinal saved the previous telescope's image")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/final", nil))
	if w.Code != 404 {
		t.Errorf("GET /final after switching telescopes: %d, want 404", w.Code)
	}
}

func TestTargetsIgnoresUnknownList(t *testing.T) {
	a, _ := newApp(t)
	a.Targets("Caldwell")
	if rows := a.Targets("NoSuchList"); len(rows) != 0 {
		t.Errorf("unknown list: %d rows", len(rows))
	}
	if got := a.Settings().Catalog; got != "Caldwell" {
		t.Errorf("unknown list name persisted: Catalog = %q", got)
	}
}

func TestStatusIncludesLiveStream(t *testing.T) {
	a, _, _ := connected(t)
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		for _, st := range a.Status() {
			if st.Key == "LiveStream/GetEnableManual" {
				return
			}
		}
	}
	t.Fatal("Status() has no live view mode")
}

// A telescope that stops answering is searched for anew, as the app's
// reconnect: found at another address, that one is connected to; found at
// the same address (or not at all), the client keeps redialling.
func TestReconnectSearchesAgain(t *testing.T) {
	old, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(moved.Close)
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	a.discover = func(context.Context) ([]string, error) { return []string{moved.Host()}, nil }
	if err := a.Connect(old.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected && d.(Conn).Host == old.Host() })
	old.Close() // the Origin restarts at a new address
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected && d.(Conn).Host == moved.Host() })
	if a.Settings().Host != moved.Host() {
		t.Errorf("host %q, want the new address %q", a.Settings().Host, moved.Host())
	}
}

// The computer is kept awake while the Origin images, as the app's wake
// lock, and let sleep again once it is idle.
func TestKeepAwakeWhileImaging(t *testing.T) {
	a, _, s := connected(t)
	var mu sync.Mutex
	held := 0
	a.actMu.Lock()
	a.inhibit = func(string, bool) func() {
		mu.Lock()
		held++
		mu.Unlock()

		return func() { mu.Lock(); held--; mu.Unlock() }
	}
	a.actMu.Unlock()
	count := func() int { mu.Lock(); defer mu.Unlock(); return held }
	setTask(t, a, s, fake.Msg{"State": "IMAGING_OBJECT"})
	for end := time.Now().Add(2 * time.Second); count() != 1 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if count() != 1 {
		t.Fatalf("inhibitors held while imaging: %d, want 1", count())
	}
	setTask(t, a, s, fake.Msg{"State": "IDLE"})
	for end := time.Now().Add(2 * time.Second); count() != 0 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if count() != 0 {
		t.Errorf("inhibitors held when idle: %d", count())
	}
}
