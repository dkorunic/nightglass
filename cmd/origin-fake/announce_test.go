// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"net"
	"slices"
	"testing"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

func TestAnnouncements(t *testing.T) {
	ifs := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.IPv4(192, 168, 1, 20), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}
	for host, want := range map[string][]string{
		"127.0.0.1:8080": {"127.0.0.1:8080"},
		"0.0.0.0:80":     {"192.168.1.20"}, // a real Origin's port: the bare address
		"[::]:8081":      {"192.168.1.20:8081"},
	} {
		msgs, err := announcements(host, ifs)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range msgs {
			if a, ok := origin.ParseAnnouncement([]byte(m)); ok {
				got = append(got, a)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %q parse as %v, want %v", host, msgs, got, want)
		}
	}
	if announceTo("127.0.0.1:8080") != "127.0.0.1:55555" || announceTo("0.0.0.0:80") != "255.255.255.255:55555" {
		t.Error("announcement destinations")
	}
}

// What the fake announces is what Nightglass's search hears.
func TestAnnounceHeard(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if err := announce(t.Context(), "127.0.0.1:8123", pc.LocalAddr().String(), 10*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := origin.ParseAnnouncement(buf[:n]); !ok || a != "127.0.0.1:8123" {
		t.Errorf("heard %q as %q", buf[:n], a)
	}
}
