// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin/fake"
)

func newFake(t *testing.T) *fake.Server {
	t.Helper()
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	return s
}

// connect starts c.Run against s and waits for the first connection.
// Polling is off unless cfg turns it on.
func connect(t *testing.T, s *fake.Server, cfg func(*Client)) *Client {
	t.Helper()
	c := New(s.Host())
	c.PollInterval = -1
	c.MinBackoff = 10 * time.Millisecond
	if cfg != nil {
		cfg(c)
	}
	up := make(chan struct{}, 16)
	user := c.OnState
	c.OnState = func(ok bool, err error) {
		if user != nil {
			user(ok, err)
		}
		if ok {
			up <- struct{}{}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	select {
	case <-up:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not connect")
	}

	return c
}

func TestSendRoutesOutOfOrderResponses(t *testing.T) {
	s := newFake(t)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		if req["Command"] == "Slow" {
			time.Sleep(200 * time.Millisecond)
		}
		r := fake.Reply(req)
		r["Echo"] = req["Command"]

		return r
	})
	c := connect(t, s, nil)
	type result struct {
		cmd string
		m   Message
		err error
	}
	out := make(chan result, 2)
	for _, cmd := range []string{"Slow", "Fast"} {
		go func() {
			m, err := c.Send(context.Background(), "System", cmd, nil)
			out <- result{cmd, m, err}
		}()
		time.Sleep(20 * time.Millisecond) // Slow goes out first
	}
	first := <-out
	if first.cmd != "Fast" {
		t.Errorf("first answer was %s, want Fast", first.cmd)
	}
	for _, r := range []result{first, <-out} {
		if r.err != nil || r.m.Fields()["Echo"] != r.cmd {
			t.Errorf("%s got %v, %v", r.cmd, r.m.Fields(), r.err)
		}
	}
}

// A response arriving after its Send timed out must not be taken as the
// answer to a later command.
func TestLateResponseIsNotMisrouted(t *testing.T) {
	s := newFake(t)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		if req["Command"] == "Late" {
			time.Sleep(300 * time.Millisecond)
		}
		r := fake.Reply(req)
		r["Echo"] = req["Command"]

		return r
	})
	c := connect(t, s, func(c *Client) { c.Timeout = 100 * time.Millisecond })
	if _, err := c.Send(context.Background(), "System", "Late", nil); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Late: err %v, want ErrTimeout", err)
	}
	time.Sleep(350 * time.Millisecond) // the late response has arrived by now
	m, err := c.Send(context.Background(), "System", "Ping", nil)
	if err != nil || m.Fields()["Echo"] != "Ping" {
		t.Fatalf("Ping got %v, %v", m.Fields(), err)
	}
}

func TestCommandError(t *testing.T) {
	s := newFake(t)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		r := fake.Reply(req)
		r["ErrorCode"], r["ErrorMessage"] = -5, "nope"

		return r
	})
	c := connect(t, s, nil)
	_, err := c.Send(context.Background(), "Mount", "GotoRaDec", nil)
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Code != -5 || ce.Message != "nope" || ce.Command != "GotoRaDec" {
		t.Fatalf("err %v, want CommandError -5 nope", err)
	}
}

func TestNotificationsReachSubscribersAndCache(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	sub := c.Subscribe()
	s.Notify("Mount", "GetStatus", fake.Msg{"Alt": 0.25})
	for done := false; !done; {
		select {
		case m := <-sub:
			if m.Type == TypeResponse {
				continue // a late answer to the on-connect queries
			}
			if m.Type != TypeNotification || m.Key() != "Mount/GetStatus" {
				t.Errorf("got %+v", m.Header)
			}
			done = true
		case <-time.After(time.Second):
			t.Fatal("no notification")
		}
	}
	m, ok := c.Last("Mount/GetStatus")
	var ms MountStatus
	if !ok || m.Decode(&ms) != nil || ms.Alt != 0.25 {
		t.Errorf("cache: %v %+v", ok, ms)
	}
}

// A subscriber that stops reading keeps the newest 64 messages and never
// blocks the read loop (spec §4.1).
func TestSlowSubscriberDropsOldest(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	sub := c.Subscribe()
	for i := range 100 {
		s.Notify("Mount", "GetStatus", fake.Msg{"NumAlignRefs": i})
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var ms MountStatus
		if m, ok := c.Last("Mount/GetStatus"); ok && m.Decode(&ms) == nil && ms.NumAlignRefs == 99 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read loop stalled behind a full subscriber")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(sub) != 64 {
		t.Fatalf("subscriber holds %d, want 64", len(sub))
	}
	var newest MountStatus
	for range 63 {
		<-sub
	}
	_ = (<-sub).Decode(&newest)
	if newest.NumAlignRefs != 99 {
		t.Errorf("newest kept %d, want 99", newest.NumAlignRefs)
	}
}

func TestReconnectAfterDrop(t *testing.T) {
	s := newFake(t)
	states := make(chan bool, 16)
	c := connect(t, s, func(c *Client) { c.OnState = func(ok bool, _ error) { states <- ok } })
	<-states // the first connect
	s.DropAll()
	for _, want := range []bool{false, true} {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state %v, want %v", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no state change to %v", want)
		}
	}
	if _, err := c.Send(context.Background(), "Mount", "GetStatus", nil); err != nil {
		t.Fatalf("send after reconnect: %v", err)
	}
}

func TestSendWhileDisconnected(t *testing.T) {
	c := New("127.0.0.1:1")
	if _, err := c.Send(context.Background(), "Mount", "GetStatus", nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("err %v, want ErrDisconnected", err)
	}
}

func TestStatusQueriedOnConnectAndPolled(t *testing.T) {
	s := newFake(t)
	connect(t, s, func(c *Client) { c.PollInterval = 30 * time.Millisecond })
	for _, q := range StatusQueries {
		if _, ok := s.Wait(fake.Is(q[0], q[1]), time.Second); !ok {
			t.Errorf("no %s/%s on connect", q[0], q[1])
		}
	}
	time.Sleep(300 * time.Millisecond)
	n := 0
	for _, m := range s.Received() {
		if fake.Is("Mount", "GetStatus")(m) {
			n++
		}
	}
	if n < 3 {
		t.Errorf("Mount/GetStatus sent %d times in 300 ms of 30 ms polling, want >= 3", n)
	}
}

// A frame that is not a JSON object reaches OnMalformed instead of
// vanishing, so the hardware pass can see it (originctl watch).
func TestMalformedFrameReported(t *testing.T) {
	s := newFake(t)
	got := make(chan string, 1)
	connect(t, s, func(c *Client) {
		c.OnMalformed = func(raw []byte, err error) {
			if err != nil {
				got <- string(raw)
			}
		}
	})
	s.SendRaw([]byte("not json"))
	select {
	case raw := <-got:
		if raw != "not json" {
			t.Errorf("OnMalformed got %q", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("malformed frame dropped silently")
	}
}

// The app's on-connect queries the GUI relies on: camera limits, wedge,
// live view mode.
func TestCaptureStateQueried(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	for _, k := range [][2]string{{"Camera", "GetCameraInfo"}, {"Mount", "GetMountConfig"}, {"LiveStream", "GetEnableManual"}} {
		if _, ok := s.Wait(fake.Is(k[0], k[1]), time.Second); !ok {
			t.Errorf("%s/%s not queried on connect", k[0], k[1])
		}
	}
	for end := time.Now().Add(time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		ci, okc := c.CameraInfo()
		mc, okm := c.MountConfig()
		if okc && okm && ci.DefaultIso == 200 && ci.MaxExposure == 30 && !mc.OnWedge {
			return
		}
	}
	t.Fatal("camera info / mount config not cached")
}

// Whole numbers written as decimals decode into integer fields, as the
// app's org.json getInt reads them; a frame is not lost for it.
func TestDecodeWholeDecimals(t *testing.T) {
	m, err := parse([]byte(`{"Source":"TaskController","Command":"GetStatus","Type":"Notification","State":"IMAGING_OBJECT","ImagingInfo":{"TotalObjects":3.0,"NumObjectsRemaining":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	var ts TaskStatus
	if err := m.Decode(&ts); err != nil || ts.State != "IMAGING_OBJECT" || ts.ImagingInfo.TotalObjects != 3 {
		t.Errorf("decoded %+v, %v", ts, err)
	}
	m, _ = parse([]byte(`{"Source":"Focuser","Command":"GetStatus","Position":"x"}`))
	if m.Decode(&FocuserStatus{}) == nil {
		t.Error("a string decoded into an int")
	}
}

// The app's close policy: a dropped link reconnects; a normal close from
// the telescope ends the connection.
func TestNormalCloseEndsRun(t *testing.T) {
	s := newFake(t)
	c := New(s.Host())
	c.MinBackoff, c.PollInterval = 10*time.Millisecond, -1
	ups := make(chan bool, 8)
	c.OnState = func(ok bool, _ error) { ups <- ok }
	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background()) }()
	if !<-ups {
		t.Fatal("not connected")
	}
	s.DropAll()
	if <-ups || !<-ups {
		t.Fatal("no reconnect after a dropped link")
	}
	s.CloseAll()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run kept going after a normal close")
	}
}

// A Response to a Post reaches subscribers as not waited for, so its
// error can be shown; a Send's is waited for.
func TestPostResponseNotWaited(t *testing.T) {
	s := newFake(t)
	c := connect(t, s, nil)
	sub := c.Subscribe()
	if err := c.Post(context.Background(), "Optics", "GetConfiguration", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(context.Background(), "Camera", "GetFilter", nil); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for end := time.After(2 * time.Second); ; {
		_, mount := seen["Optics/GetConfiguration"]
		_, focuser := seen["Camera/GetFilter"]
		if mount && focuser {
			break
		}
		select {
		case m := <-sub:
			if m.Type == TypeResponse && (m.Key() == "Optics/GetConfiguration" || m.Key() == "Camera/GetFilter") {
				seen[m.Key()] = m.Waited()
			}
		case <-end:
			t.Fatalf("responses %v", seen)
		}
	}
	if seen["Optics/GetConfiguration"] || !seen["Camera/GetFilter"] {
		t.Errorf("waited %v", seen)
	}
}

// With GiveUp, Run ends with ErrGaveUp when a connection drops, instead
// of reconnecting; a first dial that fails is retried until it connects.
func TestGiveUp(t *testing.T) {
	c := New("127.0.0.1:1") // nothing listens there
	c.GiveUp, c.MinBackoff = true, time.Millisecond
	fails := 0
	c.OnState = func(ok bool, _ error) {
		if !ok {
			fails++
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); errors.Is(err, ErrGaveUp) || fails < 2 {
		t.Errorf("first dials: %v after %d failures, want them retried", err, fails)
	}
	s := newFake(t)
	c2 := New(s.Host())
	c2.GiveUp = true
	done := make(chan error, 1)
	go func() { done <- c2.Run(context.Background()) }()
	for !c2.Connected() {
		time.Sleep(5 * time.Millisecond)
	}
	s.DropAll() // the link drops
	select {
	case err := <-done:
		if !errors.Is(err, ErrGaveUp) {
			t.Errorf("after the drop: %v, want ErrGaveUp", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still reconnecting")
	}
}
