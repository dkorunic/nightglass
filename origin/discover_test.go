// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
	"time"
)

func TestParseAnnouncement(t *testing.T) {
	for in, want := range map[string]string{
		"Origin 192.168.1.10":                  "192.168.1.10",
		"Celestron Origin-123 at 10.0.0.5\x00": "10.0.0.5",
		"NexStar 10.0.0.5":                     "",
		"Origin nonsense":                      "",
		"Origin 10.0.0.256":                    "",
		"Origin fe80::1":                       "",
		// The format the OriginSimulator project records for a real Origin.
		"Identity:Origin-140020Z Origin IP Address = 192.168.4.7": "192.168.4.7",
		// origin-fake's, with a port.
		"Identity:Origin-8080Z Origin IP Address = 127.0.0.1:8080": "127.0.0.1:8080",
		"Origin 127.0.0.1:0":     "",
		"Origin 127.0.0.1:99999": "",
		"Origin [fe80::1]:80":    "",
	} {
		got, ok := ParseAnnouncement([]byte(in))
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestListen(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	tx, err := net.Dial("udp4", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	for _, m := range []string{"Origin 192.168.1.10", "junk", "Origin 192.168.1.10", "Origin 192.168.1.11"} {
		_, _ = tx.Write([]byte(m))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	got := Listen(ctx, pc)
	if !slices.Equal(got, []string{"192.168.1.10", "192.168.1.11"}) {
		t.Errorf("got %v", got)
	}
}

// An Origin on Wi-Fi and Ethernet announces each address with one identity
// (wifi-controller's udpBroadcastThread): it is one telescope, listed at the
// first address heard.
func TestListenOneIdentity(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	tx, err := net.Dial("udp4", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	for _, m := range []string{
		"Identity:Origin-140020Z Origin IP Address = 192.168.4.7",
		"Identity:Origin-140020Z Origin IP Address = 10.0.0.9",
		"Identity:Origin-150030Z Origin IP Address = 10.0.0.10",
		"Identity: Origin IP Address = 10.0.0.11", // no identity: by address
		"Identity: Origin IP Address = 10.0.0.12",
	} {
		_, _ = tx.Write([]byte(m))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	want := []string{"192.168.4.7", "10.0.0.10", "10.0.0.11", "10.0.0.12"}
	if got := Listen(ctx, pc); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// What users type into the manual address box (Review Focus).
func TestNormalizeHost(t *testing.T) {
	for in, want := range map[string]string{
		" 192.168.1.5 ":                     "192.168.1.5",
		"http://10.0.0.2/":                  "10.0.0.2",
		"ws://10.0.0.2:8080/SmartScope-1.0": "10.0.0.2:8080",
		"origin.local":                      "origin.local",
		"10.0.0.2:8080":                     "10.0.0.2:8080",
		"":                                  "",
		"a b":                               "",
		"10.0.0.2:99999":                    "",
		"10.0.0.2:":                         "",
	} {
		got, err := NormalizeHost(in)
		if got != want || (err == nil) != (want != "") {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
}

// Like the native DetectOriginDevicesUsingMDNS: origin.local and origin-2.local.
func TestDiscoverResolvesBothMDNSNames(t *testing.T) {
	asked := map[string]bool{}
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		asked[host] = true
		switch host {
		case "origin.local":
			return []string{"10.0.0.5"}, nil
		case "origin-2.local":
			return []string{"10.0.0.6", "fe80::1"}, nil
		}

		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupHost = net.DefaultResolver.LookupHost })
	found := resolveMDNS(context.Background())
	if !asked["origin.local"] || !asked["origin-2.local"] || !slices.Equal(found, []string{"10.0.0.5", "10.0.0.6"}) {
		t.Errorf("asked %v, found %v", asked, found)
	}
}

// The direct-connect address is listed when something answers on its
// port 80, as the app's testOriginDirectConnectWithSkyQLinkAddress.
func TestDiscoverProbesDirectConnect(t *testing.T) {
	lookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	t.Cleanup(func() { lookupHost = net.DefaultResolver.LookupHost })
	orig := dialTCP
	t.Cleanup(func() { dialTCP = orig })
	for _, answers := range []bool{true, false} {
		var asked string
		dialTCP = func(_ context.Context, addr string) (net.Conn, error) {
			asked = addr
			if !answers {
				return nil, errors.New("timeout")
			}
			c, s := net.Pipe()
			_ = s.Close()

			return c, nil
		}
		found, _ := Discover(context.Background(), 50*time.Millisecond) // the UDP port may be taken
		if asked != "1.2.3.4:80" || slices.Contains(found, "1.2.3.4") != answers {
			t.Errorf("answers %v: asked %q, found %v", answers, asked, found)
		}
	}
}

// Like the native code: origin-2.local is asked only after origin.local
// resolved, and exactly one Origin found by mDNS is the answer at once,
// without the broadcast wait.
func TestDiscoverMDNSFirst(t *testing.T) {
	t.Cleanup(func() { lookupHost = net.DefaultResolver.LookupHost })
	orig := dialTCP
	t.Cleanup(func() { dialTCP = orig })
	dialTCP = func(context.Context, string) (net.Conn, error) { return nil, errors.New("timeout") }
	asked := map[string]bool{}
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		asked[host] = true

		return nil, errors.New("no such host")
	}
	if found := resolveMDNS(context.Background()); len(found) != 0 || asked["origin-2.local"] {
		t.Errorf("origin.local unresolved: asked %v, found %v", asked, found)
	}
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host == "origin.local" {
			return []string{"10.0.0.5"}, nil
		}

		return nil, errors.New("no such host")
	}
	start := time.Now()
	found, err := Discover(context.Background(), 5*time.Second)
	if err != nil || !slices.Equal(found, []string{"10.0.0.5"}) || time.Since(start) > time.Second {
		t.Errorf("one mDNS Origin: %v %v after %v, want it at once", found, err, time.Since(start))
	}
}
