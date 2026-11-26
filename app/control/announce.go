// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// announcementURL is the app's Origin announcement feed (AnnouncementChecker);
// tests replace it.
var announcementURL = "https://news.skysafariastronomy.com/announcements/origin/android/announcement.txt"

// Announcement is one announcement: a title, its text and an optional link.
type Announcement struct {
	Title, Body, URL string
}

// CheckAnnouncement is the app's checkForAnnouncement, only when chosen in
// Settings: the feed's text (title, body and an optional link, one per
// line) is returned once, the first time it differs from the last one
// shown; nil otherwise, or on any failure.
func (a *App) CheckAnnouncement() *Announcement {
	if !a.Settings().Announcements {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, announcementURL, nil)
	if err != nil {
		return nil
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	if text == "" || text == strings.TrimSpace(a.Settings().LastAnnouncement) {
		return nil
	}
	a.update(func(s *Settings) { s.LastAnnouncement = text })
	var lines []string
	for l := range strings.SplitSeq(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 {
		return nil
	}
	n := &Announcement{Title: lines[0], Body: lines[1]}
	// Only a web link is offered (the feed is a third party's).
	if len(lines) > 2 {
		if u, err := url.Parse(lines[2]); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			n.URL = u.String()
		}
	}

	return n
}
