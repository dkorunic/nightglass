// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var (
	// ErrDisconnected means there is no connection to send on, or it dropped
	// while waiting for the response.
	ErrDisconnected = errors.New("origin: not connected")
	// ErrTimeout means the telescope did not answer in time.
	ErrTimeout = errors.New("origin: no response")
	// ErrClosed ends Run: the telescope closed the connection normally,
	// which the app takes as a disconnect without reconnecting.
	ErrClosed = errors.New("origin: connection closed by the telescope")
	// ErrGaveUp ends Run when an established connection drops, with GiveUp set.
	ErrGaveUp = errors.New("origin: telescope not reachable")
	// ErrSilent drops a connection that carried nothing for five poll
	// intervals: a link gone without a TCP close.
	ErrSilent = errors.New("origin: telescope stopped answering")
)

// CommandError is a Response with a non-zero ErrorCode.
type CommandError struct {
	Destination, Command string
	Message              string
	Code                 int
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("origin: %s/%s failed: %s", e.Destination, e.Command, ErrorText(e.Code, e.Message))
}

// Localized reports whether the app has its own message for an error code,
// which it then shows itself (onOriginMessageRelay → handleOriginError).
func Localized(code int) bool { return errorMessages[code] != "" }

// ErrorText is what the app shows for an error code: its own message for
// the code (ErrorCodeEnum_GetLocalizedMessage), else the telescope's
// message, else the code's name.
func ErrorText(code int, message string) string {
	if m := errorMessages[code]; m != "" {
		return m
	}
	if message != "" {
		return message
	}
	if n := errorNames[code]; n != "" {
		return fmt.Sprintf("%s (error %d)", n, code)
	}

	return fmt.Sprintf("error %d", code)
}

// StatusQueries are what the Origin app asks for as soon as the socket
// opens: every module status the GUI and "originctl status" show.
var StatusQueries = [][2]string{
	{"Mount", "GetStatus"},
	{"TaskController", "GetStatus"},
	{"Focuser", "GetStatus"},
	{"Environment", "GetStatus"},
	{"DewHeater", "GetStatus"},
	{"Disk", "GetStatus"},
	{"Camera", "GetCaptureParameters"},
	{"Camera", "GetCameraInfo"},
	{"Mount", "GetMountConfig"},
	{"LiveStream", "GetEnableManual"},
	{"FactoryCalibrationController", "GetStatus"},
	{"Focuser", "GetPositionLimits"},
	{"ImageServer", "GetStretch"},
	{"System", "GetVersion"},
	{"System", "GetModel"},
	{"System", "GetUpdateChannel"},
	{"Network", "HasInternetConnection"},
	{"FactoryCalibrationController", "GetSerialNumber"},
	{"Autoguider", "GetStatus"}, // ScopeStatusFragment asks as it opens; Nightglass always shows it
	// Also asked by native CelestronOriginHandleWebSocketOpened.
	{"Environment", "GetSensors"},
	{"LedRing", "GetBrightnessLevel"},
	{"Focuser", "GetFocuserAdvancedSettings"},
	// Not asked by the app; Celestron's web app asks it on connect for
	// MirrorFlip, which RunCenterTarget's offsets depend on.
	{"Optics", "GetConfiguration"},
}

// polled are re-asked when no update arrived for two poll intervals, in case
// a telescope only answers instead of pushing (spec §8.3).
var polled = [][2]string{{"Mount", "GetStatus"}, {"TaskController", "GetStatus"}}

// Client talks to one telescope. Set the fields before Run.
type Client struct {
	// OnState, if set, is called on each connect (true, nil) and each
	// disconnect or failed dial (false, err). It must not block.
	OnState func(connected bool, err error)
	// OnMalformed, if set, receives frames that are not a JSON object (they
	// are otherwise dropped). It runs on the read loop and must not block.
	OnMalformed func(raw []byte, err error)

	conn    *websocket.Conn
	pending map[int]chan Message
	last    map[string]Message
	lastAt  map[string]time.Time
	heard   time.Time // the newest frame on the connection

	// MapError, if set, replaces the error Send returns for an error-coded
	// response (the GUI turns one it has already shown into a silent one).
	MapError func(error) error

	host string
	subs []chan Message
	// Timeout bounds each Send, and the dial unless DialTimeout is set
	// (default 10 s).
	Timeout time.Duration
	// DialTimeout bounds each WebSocket handshake (default Timeout).
	DialTimeout time.Duration
	// MinBackoff and MaxBackoff bound the reconnect delay (default 1 s, 30 s).
	MinBackoff, MaxBackoff time.Duration
	// GiveUp, when set, ends Run with ErrGaveUp when an established
	// connection drops, instead of reconnecting: the caller then does what
	// the app does (connectOrigin). Dials before the first connection are
	// still retried, as the app's WebSocket library does by itself. Unset,
	// Run keeps reconnecting.
	GiveUp bool
	// PollInterval is the stale-status check period (default 2 s; negative
	// disables polling).
	PollInterval time.Duration
	seq          int
	mu           sync.Mutex
}

// New returns a client for host ("ip" or "ip:port"; see NormalizeHost).
func New(host string) *Client {
	return &Client{host: host, pending: map[int]chan Message{}, last: map[string]Message{}, lastAt: map[string]time.Time{}}
}

// Host is the telescope address the client dials.
func (c *Client) Host() string { return c.host }

// Connected reports whether a connection is up.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.conn != nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}

	return d
}

// Run connects and reconnects until ctx ends, then returns ctx.Err().
func (c *Client) Run(ctx context.Context) error {
	minB, maxB := orDefault(c.MinBackoff, time.Second), orDefault(c.MaxBackoff, 30*time.Second)
	backoff := minB
	everUp := false
	for {
		connected, err := c.session(ctx)
		everUp = everUp || connected
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.OnState != nil {
			c.OnState(false, err)
		}
		if closedNormally(err) {
			return fmt.Errorf("%w: %w", ErrClosed, err)
		}
		if c.GiveUp && everUp {
			return fmt.Errorf("%w: %w", ErrGaveUp, err)
		}
		if connected {
			backoff = minB
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, maxB)
	}
}

// closedNormally is the app's onWebSocketClosedRelay: a close frame other
// than 1011 (the app reconnects after "Ping timeout") ends the connection;
// a dropped link (no close frame, 1006) reconnects. The app sets no ping
// interval (ixwebsocket's default -1), so neither does this client.
func closedNormally(err error) bool {
	code := websocket.CloseStatus(err)

	return code != -1 && code != websocket.StatusAbnormalClosure && code != websocket.StatusInternalError
}

// session dials once and serves the connection until it drops.
func (c *Client) session(ctx context.Context) (bool, error) {
	dctx, cancel := context.WithTimeout(ctx, orDefault(c.DialTimeout, orDefault(c.Timeout, 10*time.Second)))
	conn, _, err := websocket.Dial(dctx, "ws://"+c.host+WSPath, nil) //nolint:bodyclose // the library closes it (Dial docs)
	cancel()
	if err != nil {
		return false, err
	}
	conn.SetReadLimit(8 << 20)
	c.mu.Lock()
	c.conn, c.heard = conn, time.Now()
	c.mu.Unlock()
	if c.OnState != nil {
		c.OnState(true, nil)
	}
	sctx, stop := context.WithCancelCause(ctx)
	go c.query(sctx, stop)
	err = c.read(sctx, conn)
	if cause := context.Cause(sctx); errors.Is(cause, ErrSilent) {
		err = cause
	}
	stop(nil)
	c.mu.Lock()
	c.conn = nil
	for seq, ch := range c.pending {
		close(ch)
		delete(c.pending, seq)
	}
	c.mu.Unlock()
	_ = conn.CloseNow()

	return true, err
}

func (c *Client) read(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		m, err := parse(b)
		if err != nil {
			if c.OnMalformed != nil {
				c.OnMalformed(b, err)
			}

			continue
		}
		c.dispatch(m)
	}
}

// dispatch hands a Response to its waiting Send, caches successful
// responses and notifications, and fans everything out to subscribers.
func (c *Client) dispatch(m Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heard = time.Now()
	if m.Type == TypeResponse {
		if ch, ok := c.pending[m.SequenceID]; ok {
			delete(c.pending, m.SequenceID)
			m.waited = true
			ch <- m
		}
	}
	if m.ErrorCode == 0 || failedTask(m) {
		c.last[m.Key()] = m
		c.lastAt[m.Key()] = time.Now()
	}
	for _, s := range c.subs {
		offer(s, m)
	}
}

// failedTask reports whether m is a TaskController status that carries a
// State beside its ErrorCode: a task that ended early is reported that way,
// in every push until the next task (Celestron's web app, firmware 1.5.6286),
// and its State is still the Origin's.
func failedTask(m Message) bool {
	if m.ErrorCode == 0 || m.Key() != "TaskController/GetStatus" {
		return false
	}
	var ts struct{ State string }

	return m.Decode(&ts) == nil && ts.State != ""
}

// offer sends m, dropping the oldest queued message when ch is full, so a
// slow subscriber never stalls the read loop.
func offer(ch chan Message, m Message) {
	for {
		select {
		case ch <- m:
			return
		default:
			select {
			case <-ch:
			default:
			}
		}
	}
}

// Subscribe returns a channel of every Response and Notification from now
// on, across reconnects. It holds the newest 64.
// ponytail: no Unsubscribe; the GUI and the CLI each subscribe once per client.
func (c *Client) Subscribe() <-chan Message {
	ch := make(chan Message, 64)
	c.mu.Lock()
	c.subs = append(c.subs, ch)
	c.mu.Unlock()

	return ch
}

// Last returns the newest successful message for key ("Mount/GetStatus").
func (c *Client) Last(key string) (Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.last[key]

	return m, ok
}

// Send sends one command and waits for its Response.
func (c *Client) Send(ctx context.Context, dest, cmd string, payload map[string]any) (Message, error) {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()

		return Message{}, ErrDisconnected
	}
	c.seq++
	seq := c.seq
	ch := make(chan Message, 1)
	c.pending[seq] = ch
	c.mu.Unlock()

	b, err := encodeCommand(dest, cmd, seq, payload)
	if err != nil {
		c.forget(seq)

		return Message{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, orDefault(c.Timeout, 10*time.Second))
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		c.forget(seq)

		return Message{}, fmt.Errorf("%w: %w", ErrDisconnected, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return Message{}, ErrDisconnected
		}
		if m.ErrorCode != 0 {
			var err error = &CommandError{Destination: dest, Command: cmd, Code: m.ErrorCode, Message: m.ErrorMessage}
			if c.MapError != nil {
				err = c.MapError(err)
			}

			return m, err
		}

		return m, nil
	case <-ctx.Done():
		c.forget(seq)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Message{}, fmt.Errorf("%w to %s/%s", ErrTimeout, dest, cmd)
		}

		return Message{}, ctx.Err()
	}
}

// Post sends one command without waiting for its Response, as the app
// sends most of its commands; the Response reaches subscribers only, with
// Waited false, so an error in it is reported there.
func (c *Client) Post(ctx context.Context, dest, cmd string, payload map[string]any) error {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()

		return ErrDisconnected
	}
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	b, err := encodeCommand(dest, cmd, seq, payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, orDefault(c.Timeout, 10*time.Second))
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("%w: %w", ErrDisconnected, err)
	}

	return nil
}

// forget drops a pending Send; a Response arriving later finds no waiter
// and only reaches subscribers.
func (c *Client) forget(seq int) {
	c.mu.Lock()
	delete(c.pending, seq)
	c.mu.Unlock()
}

// query asks for every status once, then re-asks the polled ones when stale,
// and drops the connection (ErrSilent) when nothing at all arrived for five
// intervals: an Origin pushes TaskController status every few seconds and
// answers the re-asks, so only a dead link is that quiet (not app parity: the
// app has no such check; Celestron's own web app gives up after 4 s).
func (c *Client) query(ctx context.Context, drop context.CancelCauseFunc) {
	for _, q := range StatusQueries {
		go func() { _ = c.Post(ctx, q[0], q[1], nil) }()
	}
	if c.PollInterval < 0 {
		return
	}
	iv := orDefault(c.PollInterval, 2*time.Second)
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.mu.Lock()
			silent := time.Since(c.heard) > 5*iv
			c.mu.Unlock()
			if silent {
				drop(ErrSilent)

				return
			}
			for _, q := range polled {
				c.mu.Lock()
				stale := time.Since(c.lastAt[q[0]+"/"+q[1]]) > 2*iv
				c.mu.Unlock()
				if stale {
					go func() { _ = c.Post(ctx, q[0], q[1], nil) }()
				}
			}
		}
	}
}
