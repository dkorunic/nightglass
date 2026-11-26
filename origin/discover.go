// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DiscoveryPort is where Origins broadcast (DeviceConnection.CELESTRON_UDP_PORT).
const DiscoveryPort = 55555

// DiscoveryTime is how long the app listens (DeviceDiscovery.ORIGIN_UDP_TIMEOUT_MS).
const DiscoveryTime = 11 * time.Second

// ParseAnnouncement returns the IPv4 address in a broadcast naming an
// Origin: the token after the last space. An address with a port (which an
// Origin never sends) is origin-fake's, so several can run on one machine.
func ParseAnnouncement(b []byte) (string, bool) {
	s := strings.TrimRight(string(b), "\x00 \r\n")
	if !strings.Contains(s, "Origin") {
		return "", false
	}
	tok, port := s[strings.LastIndexByte(s, ' ')+1:], ""
	if h, p, err := net.SplitHostPort(tok); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", false
		}
		tok, port = h, p
	}
	ip := net.ParseIP(tok)
	if ip == nil || ip.To4() == nil {
		return "", false
	}
	if port != "" {
		return net.JoinHostPort(ip.String(), port), true
	}

	return ip.String(), true
}

// Listen collects announced addresses from pc until ctx ends.
func Listen(ctx context.Context, pc net.PacketConn) []string {
	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()
	var found []string
	buf := make([]byte, 1024)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return found
		}
		if ip, ok := ParseAnnouncement(buf[:n]); ok && !slices.Contains(found, ip) {
			found = append(found, ip)
		}
	}
}

// DirectConnectHost is the Origin's address on its own Wi-Fi network: the
// app probes TCP port 80 there (testOriginDirectConnectWithSkyQLinkAddress)
// when the phone's network name starts with "Origin-".
const DirectConnectHost = "1.2.3.4"

// dialTCP is replaced in tests.
var dialTCP = func(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
}

// probeDirect reports whether an Origin answers at DirectConnectHost.
// ponytail: a desktop cannot portably read the Wi-Fi network's name, so
// this probes on every search instead of only on an "Origin-" network;
// 1.2.3.4 is not normally reachable elsewhere.
func probeDirect(ctx context.Context) bool {
	c, err := dialTCP(ctx, net.JoinHostPort(DirectConnectHost, "80"))
	if err != nil {
		return false
	}
	_ = c.Close()

	return true
}

// Discover is the native AutoDetectOriginDevices: mDNS first, and exactly
// one Origin found there is the answer at once; otherwise (none, or two)
// those are dropped and broadcasts are listened for during d, with the
// direct-connect address probed alongside. A port already in use still
// returns the other results, with the bind error.
func Discover(ctx context.Context, d time.Duration) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	if mdns := resolveMDNS(ctx); len(mdns) == 1 {
		return mdns, nil
	}
	direct := make(chan bool, 1)
	go func() { direct <- probeDirect(ctx) }()
	udp := make(chan []string, 1)
	pc, bindErr := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", ":"+strconv.Itoa(DiscoveryPort))
	if bindErr == nil {
		defer pc.Close()
		go func() { udp <- Listen(ctx, pc) }()
	} else {
		bindErr = fmt.Errorf("origin: listen on UDP %d: %w", DiscoveryPort, bindErr)
		udp <- nil
	}
	found := <-udp
	if <-direct && !slices.Contains(found, DirectConnectHost) {
		found = append(found, DirectConnectHost)
	}

	return found, bindErr
}

// mdnsNames are what the native DetectOriginDevicesUsingMDNS resolves: the
// Origin and a second one on the same network.
var mdnsNames = []string{"origin.local", "origin-2.local"}

// lookupHost is replaced in tests.
var lookupHost = net.DefaultResolver.LookupHost

// resolveMDNS returns the IPv4 addresses of mdnsNames, as the native
// DetectOriginDevicesUsingMDNS: the second is asked only once the first
// resolved.
func resolveMDNS(ctx context.Context) []string {
	var found []string
	for i, name := range mdnsNames {
		if i > 0 && len(found) == 0 {
			break
		}
		addrs, _ := lookupHost(ctx, name)
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && ip.To4() != nil && !slices.Contains(found, a) {
				found = append(found, a)
			}
		}
	}

	return found
}

// NormalizeHost turns what a user types ("192.168.1.5", " http://10.0.0.2/ ",
// "origin.local:8080") into "host[:port]" for New.
func NormalizeHost(in string) (string, error) {
	s := strings.TrimSpace(in)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	host := s
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("origin: bad port in %q", in)
		}
		host = h
	}
	if host == "" || strings.ContainsAny(host, " \t@?#:[]") {
		return "", fmt.Errorf("origin: bad telescope address %q", in)
	}

	return s, nil
}
