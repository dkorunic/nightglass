// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

func ctl(t *testing.T, s *fake.Server, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code := run(ctx, append([]string{"-host", s.Host(), "-timeout", "2s"}, args...), &out, &errb)

	return code, out.String(), errb.String()
}

func newFake(t *testing.T) *fake.Server {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	return s
}

func TestGotoTarget(t *testing.T) {
	s := newFake(t)
	if code, _, errs := ctl(t, s, "goto", "-target", "M42"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	m, ok := s.Wait(fake.Is("Mount", "GotoRaDec"), time.Second)
	if !ok || math.Abs(m["Dec"].(float64)-(-5.39*math.Pi/180)) > 0.01 {
		t.Errorf("goto payload %v", m)
	}
	if code, _, _ := ctl(t, s, "goto", "-target", "Nothing 0"); code != 1 {
		t.Errorf("unknown target: exit %d, want 1", code)
	}
}

func TestStatusAndSend(t *testing.T) {
	s := newFake(t)
	code, out, errs := ctl(t, s, "status")
	if code != 0 || !strings.Contains(out, "Mount/GetStatus ") || !strings.Contains(out, "TaskController/GetStatus ") {
		t.Fatalf("status exit %d: %s %s", code, out, errs)
	}
	code, out, _ = ctl(t, s, "send", "System", "GetVersion", `{"X":1}`)
	if code != 0 || !strings.Contains(out, `"Command":"GetVersion"`) {
		t.Errorf("send: %d %s", code, out)
	}
}

func TestSlewThenStop(t *testing.T) {
	s := newFake(t)
	if code, _, errs := ctl(t, s, "slew", "-alt", "4", "-for", "50ms"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var rates []float64
	for end := time.Now().Add(2 * time.Second); len(rates) < 2 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		rates = rates[:0] // the stop is posted, so it may land a moment later
		for _, m := range s.Received() {
			if fake.Is("Mount", "Slew")(m) {
				rates = append(rates, m["AltRate"].(float64))
			}
		}
	}
	if len(rates) != 2 || rates[0] != 4 || rates[1] != 0 {
		t.Errorf("slew rates sent %v, want [4 0]", rates)
	}
}

func TestFetch(t *testing.T) {
	s := newFake(t)
	jpg := fake.JPEG(0)
	s.SetFile("live/a.jpg", jpg)
	p := filepath.Join(t.TempDir(), "a.jpg")
	if code, _, errs := ctl(t, s, "fetch", "-o", p, "live/a.jpg"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, jpg) {
		t.Error("fetched file differs")
	}
}

func TestUsageErrors(t *testing.T) {
	s := newFake(t)
	if code, _, _ := ctl(t, s, "bogus"); code != 2 {
		t.Errorf("unknown command: exit %d, want 2", code)
	}
	if code, _, _ := ctl(t, s, "init", "-lat", "45"); code != 1 {
		t.Errorf("init without -lon: exit %d, want 1", code)
	}
}

// Ctrl-C mid-slew must still stop the mount: the stop goes out on a link
// that outlives the interrupted command.
func TestSlewInterruptedStillStops(t *testing.T) {
	s := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var out, errb bytes.Buffer
		done <- run(ctx, []string{"-host", s.Host(), "-timeout", "2s", "slew", "-alt", "2", "-for", "30s"}, &out, &errb)
	}()
	if _, ok := s.Wait(fake.Is("Mount", "Slew"), 2*time.Second); !ok {
		t.Fatal("no slew sent")
	}
	cancel()
	<-done
	stop := func(m fake.Msg) bool {
		return fake.Is("Mount", "Slew")(m) && m["AltRate"] == 0.0 && m["AzmRate"] == 0.0
	}
	if _, ok := s.Wait(stop, 2*time.Second); !ok {
		t.Fatal("interrupted slew left the mount moving: no Slew 0,0")
	}
}

func TestWatchReportsMalformed(t *testing.T) {
	s := newFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"-host", s.Host(), "watch"}, &out, &errb) }()
	if _, ok := s.Wait(fake.Is("Mount", "GetStatus"), time.Second); !ok {
		t.Fatal("watch did not connect")
	}
	time.Sleep(50 * time.Millisecond)
	s.SendRaw([]byte("garbage"))
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	if !strings.Contains(errb.String(), "malformed frame") || !strings.Contains(errb.String(), "garbage") {
		t.Errorf("stderr %q, want the malformed frame reported", errb.String())
	}
}

// image works like the app: where the telescope points, with a Uuid, the
// catalog target only naming it; exposure and ISO only with -manual.
func TestImageLikeApp(t *testing.T) {
	s := newFake(t)
	if code, _, errs := ctl(t, s, "image", "-target", "M42"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	m, _ := s.Wait(fake.Is("TaskController", "RunImaging"), time.Second)
	if m["Name"] != "M 42" || m["Uuid"] == nil || m["ObjectSize"] == nil {
		t.Errorf("payload %v", m)
	}
	for _, k := range []string{"Ra", "Dec", "ExposureTime", "ISO"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s sent", k)
		}
	}
	if code, _, errs := ctl(t, s, "image", "-name", "Moon", "-manual", "-exp", "0.01", "-iso", "100"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var last fake.Msg
	for _, m := range s.Received() {
		if fake.Is("TaskController", "RunImaging")(m) {
			last = m
		}
	}
	if last["Name"] != "Moon" || last["ExposureTime"] != 0.01 || last["ISO"] != 100.0 {
		t.Errorf("manual payload %v", last)
	}
}

func TestCaptureModes(t *testing.T) {
	s := newFake(t)
	if code, _, errs := ctl(t, s, "capture", "-manual", "-snapshot", "-exp", "0.5", "-iso", "500"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	m, ok := s.Wait(fake.Is("Camera", "SetCaptureParameters"), time.Second)
	if _, manual := s.Wait(fake.Is("LiveStream", "SetEnableManual"), time.Second); !ok || !manual || m["Exposure"] != 0.5 || m["ISO"] != 500.0 {
		t.Errorf("manual snapshot capture: %v", m)
	}
	s2 := newFake(t)
	if code, _, _ := ctl(t, s2, "capture"); code != 0 {
		t.Fatal("capture (auto) failed")
	}
	if _, ok := s2.Wait(fake.Is("LiveStream", "SetEnableAuto"), time.Second); !ok {
		t.Error("automatic capture did not send SetEnableAuto")
	}
	if code, _, _ := ctl(t, s2, "live", "on"); code != 2 {
		t.Errorf("live (removed, as the app has no live-off): exit %d, want 2", code)
	}
}

func TestFinishAndCancel(t *testing.T) {
	s := newFake(t)
	s.SetStatus("TaskController", fake.Msg{"State": "IMAGING_OBJECT", "ImagingInfo": fake.Msg{"ImageUuid": "r1"}})
	if code, _, errs := ctl(t, s, "finish"); code != 0 {
		t.Fatalf("finish: exit %d: %s", code, errs)
	}
	if m, ok := s.Wait(fake.Is("ImageServer", "GetFinalStackedMaster"), time.Second); !ok || m["ImageUuid"] != "r1" {
		t.Errorf("finish: %v", m)
	}
	if code, _, _ := ctl(t, s, "cancel"); code != 0 {
		t.Fatal("cancel failed")
	}
	if _, ok := s.Wait(fake.Is("TaskController", "CancelImaging"), time.Second); !ok {
		t.Error("cancel did not send CancelImaging")
	}
	if code, _, _ := ctl(t, s, "stop"); code != 2 {
		t.Errorf("stop (replaced by finish/cancel): exit %d, want 2", code)
	}
}
