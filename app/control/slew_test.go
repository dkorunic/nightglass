// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin"
	"github.com/dkorunic/nightglass/origin/fake"
)

func isStop(m fake.Msg) bool {
	return fake.Is("Mount", "Slew")(m) && m["AltRate"] == 0.0 && m["AzmRate"] == 0.0
}

// A slew is not waited for (native MoveCelestronOrigin), so one whose
// response is lost still counts as moving: it is owed a stop when the
// link returns.
func TestLostSlewResponseIsStoppedAfterReconnect(t *testing.T) {
	s, err := fake.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetHandle(func(req fake.Msg) fake.Msg {
		if fake.Is("Mount", "Slew")(req) && req["AltRate"] != 0.0 {
			return nil // the slew arrives, its response is lost
		}

		return s.DefaultHandle(req)
	})
	a, rec := newApp(t)
	t.Cleanup(a.Disconnect)
	a.tune = func(c *origin.Client) {
		c.MinBackoff, c.PollInterval, c.Timeout = 10*time.Millisecond, -1, 200*time.Millisecond
	}
	a.discover = func(context.Context) ([]string, error) { return []string{s.Host()}, nil } // found again after the drop
	if err := a.Connect(s.Host()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "conn", func(d any) bool { return d.(Conn).Connected })
	// The pad works only once the Origin reports idle (as the app's).
	waitTask(t, a, "IDLE")
	if err := a.Slew(4, 0); err != nil {
		t.Fatalf("slew: %v", err)
	}
	s.DropAll()
	if _, ok := s.Wait(isStop, 3*time.Second); !ok {
		t.Fatal("no Slew 0,0 after reconnect for a slew whose response was lost")
	}
}

// Disconnecting (or quitting) mid-slew stops the mount first (spec §5).
func TestDisconnectStopsSlew(t *testing.T) {
	a, _, s := connected(t)
	if err := a.Slew(4, 0); err != nil {
		t.Fatal(err)
	}
	a.Disconnect()
	if _, ok := s.Wait(isStop, time.Second); !ok {
		t.Fatal("Disconnect left the mount moving: no Slew 0,0")
	}
}
