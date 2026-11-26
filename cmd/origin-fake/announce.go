// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// announcements are what the fake broadcasts for host (its listen
// address), in the format the OriginSimulator project records for a real
// Origin: "Identity:Origin-<id>Z Origin IP Address = <ip>". A real Origin
// serves on port 80; on any other port the address carries it, which
// origin.ParseAnnouncement accepts, so several fakes can run on one machine.
// An unspecified host is announced once per IPv4 interface address.
func announcements(host string, ifAddrs func() ([]net.Addr, error)) ([]string, error) {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return nil, err
	}
	ips := []string{h}
	if ip := net.ParseIP(h); ip == nil || ip.IsUnspecified() {
		ips = nil
		addrs, err := ifAddrs()
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				ips = append(ips, n.IP.String())
			}
		}
	}
	id, _ := strconv.Atoi(port)
	msgs := make([]string, 0, len(ips))
	for _, ip := range ips {
		addr := ip
		if port != "80" {
			addr = net.JoinHostPort(ip, port)
		}
		msgs = append(msgs, fmt.Sprintf("Identity:Origin-%dZ Origin IP Address = %s", id, addr))
	}

	return msgs, nil
}

// announceTo is where the announcements go: the loopback address for a fake
// listening there (a local Nightglass hears it), else the broadcast address.
func announceTo(host string) string {
	h, _, _ := net.SplitHostPort(host)
	dst := "255.255.255.255"
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		dst = h
	}

	return net.JoinHostPort(dst, strconv.Itoa(origin.DiscoveryPort))
}

// announce broadcasts host's announcements every iv until ctx ends, except
// while down says the fake is down (nil: never).
func announce(ctx context.Context, host, dst string, iv time.Duration, down func() bool) error {
	msgs, err := announcements(host, net.InterfaceAddrs)
	if err != nil {
		return err
	}
	to, err := net.ResolveUDPAddr("udp4", dst)
	if err != nil {
		return err
	}
	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", ":0") // Go sets SO_BROADCAST
	if err != nil {
		return err
	}
	go func() {
		defer pc.Close()
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			for _, m := range msgs {
				if down == nil || !down() {
					_, _ = pc.WriteTo([]byte(m), to)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	return nil
}
