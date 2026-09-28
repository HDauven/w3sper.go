// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package network

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var ErrDisconnected = errors.New("network: RUES session disconnected")
var ErrSlowConsumer = errors.New("network: RUES subscriber buffer exhausted")

type Event struct {
	Headers http.Header
	Origin  Origin
	Payload []byte
}
type Origin struct{ Scope, ID, Topic string }

func (o Origin) String() string {
	target := o.Scope
	if o.ID != "" {
		target += ":" + o.ID
	}
	return "/on/" + target + "/" + o.Topic
}
func (e Event) JSON(out any) error { return json.Unmarshal(e.Payload, out) }
func (e Event) GasPaid() (*big.Int, error) {
	var p struct {
		Gas   json.RawMessage `json:"gas_spent"`
		Inner struct {
			Fee struct {
				Price json.RawMessage `json:"gas_price"`
			} `json:"fee"`
		} `json:"inner"`
	}
	if err := e.JSON(&p); err != nil {
		return nil, err
	}
	a, err := decodeBigInt(p.Gas)
	if err != nil {
		return nil, err
	}
	b, err := decodeBigInt(p.Inner.Fee.Price)
	if err != nil {
		return nil, err
	}
	if !a.IsUint64() || !b.IsUint64() {
		return nil, fmt.Errorf("network: invalid executed fee")
	}
	return new(big.Int).Mul(a, b), nil
}
func (e Event) Memo() ([]byte, error) {
	var p struct {
		Inner struct {
			Memo string `json:"memo"`
		} `json:"inner"`
	}
	if err := e.JSON(&p); err != nil {
		return nil, err
	}
	return hex.DecodeString(p.Inner.Memo)
}
func (e Event) Call() (json.RawMessage, error) {
	var p struct {
		Inner struct {
			Call json.RawMessage `json:"call"`
		} `json:"inner"`
	}
	err := e.JSON(&p)
	return p.Inner.Call, err
}
func parseEvent(b []byte) (Event, error) {
	var out Event
	if len(b) < 4 {
		return out, fmt.Errorf("network: truncated RUES frame")
	}
	n := uint64(binary.LittleEndian.Uint32(b))
	if n > uint64(len(b)-4) {
		return out, fmt.Errorf("network: truncated RUES headers")
	}
	var headers map[string]string
	if e := json.Unmarshal(b[4:4+n], &headers); e != nil {
		return out, e
	}
	out.Headers = make(http.Header)
	for k, v := range headers {
		out.Headers.Set(k, v)
	}
	loc := strings.TrimPrefix(out.Headers.Get("content-location"), "/")
	parts := strings.SplitN(loc, "/", 3)
	if len(parts) != 3 || parts[0] != "on" || parts[1] == "" || parts[2] == "" {
		return out, fmt.Errorf("network: invalid event origin")
	}
	target := strings.SplitN(parts[1], ":", 2)
	out.Origin = Origin{Scope: target[0], Topic: parts[2]}
	if len(target) == 2 {
		out.Origin.ID = target[1]
	}
	out.Payload = append([]byte(nil), b[4+n:]...)
	return out, nil
}

type connectAttempt struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}
type ruesSession struct {
	client *Client
	socket *websocket.Conn
	id     string
	done   chan struct{}
	once   sync.Once
	write  chan struct{}
	mu     sync.Mutex
	closed bool
	reason error
	paths  map[string]*registration
	probes map[string]chan struct{}
}
type registration struct {
	gate                  chan struct{}
	path                  string
	registered, attempted bool                       // guarded by gate
	users                 map[*Subscription]struct{} // guarded by session.mu
}

func (r *registration) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.gate:
		return nil
	}
}
func (r *registration) unlock() { r.gate <- struct{}{} }

// Connect shares one in-flight handshake. The first caller's context controls
// it. Later requests use independent contexts, never the handshake's deadline.
func (c *Client) Connect(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	c.mu.Lock()
	if c.session != nil {
		c.mu.Unlock()
		return nil
	}
	if a := c.connecting; a != nil {
		c.mu.Unlock()
		select {
		case <-a.done:
			return a.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	handshake, cancel := c.ReadContext(ctx)
	a := &connectAttempt{done: make(chan struct{}), cancel: cancel}
	c.connecting = a
	c.mu.Unlock()
	defer cancel()
	u := c.endpoint("/on")
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	dialer := *websocket.DefaultDialer
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		dialer.Proxy = transport.Proxy
		dialer.NetDialContext = transport.DialContext
		dialer.TLSClientConfig = transport.TLSClientConfig
	}
	// Gorilla installs deadlines but does not interrupt a pending HTTP upgrade
	// when a context is canceled after TCP connect. Track and close that socket
	// until the complete RUES hello succeeds; established sessions are detached.
	dial := dialer.NetDialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	var dialMu sync.Mutex
	var pending net.Conn
	stopHandshake := context.AfterFunc(handshake, func() {
		dialMu.Lock()
		if pending != nil {
			_ = pending.Close()
		}
		dialMu.Unlock()
	})
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, e := dial(ctx, network, address)
		if e != nil {
			return nil, e
		}
		dialMu.Lock()
		defer dialMu.Unlock()
		if e = handshake.Err(); e != nil {
			_ = conn.Close()
			return nil, e
		}
		pending = conn
		return conn, nil
	}
	ws, resp, err := dialer.DialContext(handshake, u.String(), http.Header{"Rusk-Version": []string{c.version}})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	var session *ruesSession
	if err == nil {
		ws.SetReadLimit(64 << 20)
		if deadline, ok := handshake.Deadline(); ok {
			_ = ws.SetReadDeadline(deadline)
		}
		var kind int
		var hello []byte
		kind, hello, err = ws.ReadMessage()
		if err == nil {
			if kind != websocket.TextMessage || len(hello) == 0 {
				err = fmt.Errorf("network: invalid RUES handshake")
			} else {
				session = &ruesSession{client: c, socket: ws, id: string(hello), done: make(chan struct{}), write: make(chan struct{}, 1), paths: make(map[string]*registration), probes: make(map[string]chan struct{})}
				session.write <- struct{}{}
				_ = ws.SetReadDeadline(time.Time{})
			}
		}
		if e := handshake.Err(); e != nil {
			err = e
		}
		if err != nil {
			_ = ws.Close()
		}
	}
	stopHandshake()
	dialMu.Lock()
	pending = nil
	dialMu.Unlock()
	c.mu.Lock()
	if e := handshake.Err(); e != nil {
		err = e
		if ws != nil {
			_ = ws.Close()
		}
	}
	a.err = err
	if err == nil {
		c.session = session
		go session.readLoop()
		go session.keepAlive()
	}
	c.connecting = nil
	close(a.done)
	c.mu.Unlock()
	return err
}
func (c *Client) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.session != nil }

// WaitDisconnected waits for the current session to retire. Cancellation only
// stops this waiter; it never disconnects the session or cancels HTTP requests.
func (c *Client) WaitDisconnected(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s, e := c.currentSession()
	if e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.reason
	}
}
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	a, s := c.connecting, c.session
	if a != nil {
		a.cancel()
	}
	c.mu.Unlock()
	if a != nil {
		select {
		case <-a.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		c.mu.Lock()
		s = c.session
		c.mu.Unlock()
	}
	if s != nil {
		s.retire(ErrDisconnected)
	}
	return ctx.Err()
}
func (c *Client) currentSession() (*ruesSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil, ErrDisconnected
	}
	return c.session, nil
}
func (s *ruesSession) send(ctx context.Context, text string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrDisconnected
	case <-s.write:
	}
	defer func() { s.write <- struct{}{} }()
	if e := ctx.Err(); e != nil {
		return e
	}
	// A probe timeout only cancels its waiter; writes have an independent short
	// transport deadline and never install a read deadline on the shared socket.
	_ = s.socket.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.socket.WriteMessage(websocket.TextMessage, []byte(text))
}
func (s *ruesSession) keepAlive() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if e := s.send(context.Background(), ""); e != nil {
				s.retire(e)
				return
			}
		}
	}
}
func (s *ruesSession) readLoop() {
	for {
		kind, b, e := s.socket.ReadMessage()
		if e != nil {
			s.retire(e)
			return
		}
		if kind == websocket.TextMessage {
			text := string(b)
			if strings.HasPrefix(text, "rues:pong:") {
				nonce := strings.TrimPrefix(text, "rues:pong:")
				s.mu.Lock()
				if ch := s.probes[nonce]; ch != nil {
					close(ch)
					delete(s.probes, nonce)
				}
				s.mu.Unlock()
			}
			continue
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		event, e := parseEvent(b)
		if e != nil {
			s.retire(e)
			return
		}
		s.mu.Lock()
		for _, r := range s.paths {
			if r.path != event.Origin.String() && r.path != (Origin{Scope: event.Origin.Scope, Topic: event.Origin.Topic}).String() {
				continue
			}
			for sub := range r.users {
				copyEvent := Event{Headers: event.Headers.Clone(), Origin: event.Origin, Payload: append([]byte(nil), event.Payload...)}
				select {
				case sub.events <- copyEvent:
				default:
					s.finishLocked(sub, ErrSlowConsumer)
					// Terminal overflow must release remote registration too. Cleanup
					// is serialized after any in-flight GET and bounded to five seconds.
					go sub.closeWith(ErrSlowConsumer)
				}
			}
		}
		s.mu.Unlock()
	}
}
func (s *ruesSession) retire(reason error) {
	s.once.Do(func() {
		s.client.mu.Lock()
		if s.client.session == s {
			s.client.session = nil
		}
		s.client.mu.Unlock()
		_ = s.socket.Close()
		s.mu.Lock()
		s.closed = true
		s.reason = reason
		close(s.done)
		for _, r := range s.paths {
			for sub := range r.users {
				s.finishLocked(sub, reason)
			}
		}
		s.paths = make(map[string]*registration)
		s.probes = make(map[string]chan struct{})
		s.mu.Unlock()
	})
}
func (c *Client) PingWebSocket(ctx context.Context) error {
	ctx, cancel := c.ReadContext(ctx)
	defer cancel()
	s, e := c.currentSession()
	if e != nil {
		return e
	}
	var bytes [16]byte
	if _, e = rand.Read(bytes[:]); e != nil {
		return e
	}
	nonce := hex.EncodeToString(bytes[:])
	ch := make(chan struct{})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrDisconnected
	}
	s.probes[nonce] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.probes, nonce); s.mu.Unlock() }()
	if e = s.send(ctx, "rues:ping:"+nonce); e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrDisconnected
	case <-ch:
		return nil
	}
}

// Subscription is a bounded stream. Slow consumers fail explicitly rather than
// silently losing events. Close releases its share of the server registration.
type Subscription struct {
	session      *ruesSession
	registration *registration
	events       chan Event
	done         chan struct{}
	err          error
	stop         func() bool
	life         context.Context
	cancel       context.CancelFunc
}

// OperationContext also ends pending event decoding on stop/disconnect/error.
func (sub *Subscription) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(sub.life, cancel)
	return op, func() { stop(); cancel() }
}
func (sub *Subscription) Done() <-chan struct{} { return sub.done }
func (sub *Subscription) Next(ctx context.Context) (Event, error) {
	if e := ctx.Err(); e != nil {
		return Event{}, e
	}
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case event, ok := <-sub.events:
		if ok {
			return event, nil
		}
		sub.session.mu.Lock()
		e := sub.err
		sub.session.mu.Unlock()
		if e == nil {
			e = io.EOF
		}
		return Event{}, e
	}
}
func (s *ruesSession) finishLocked(sub *Subscription, err error) {
	select {
	case <-sub.done:
		return
	default:
	}
	sub.err = err
	sub.cancel()
	delete(sub.registration.users, sub)
	if sub.stop != nil {
		sub.stop()
	}
	close(sub.done)
	close(sub.events)
}

// Cancel stops delivery immediately and schedules bounded remote cleanup.
// Close additionally waits for that cleanup acknowledgement (at most 5 seconds).
func (sub *Subscription) Cancel() {
	s := sub.session
	s.mu.Lock()
	s.finishLocked(sub, nil)
	s.mu.Unlock()
	go sub.closeWith(nil)
}
func (sub *Subscription) Close() error { return sub.closeWith(nil) }
func (sub *Subscription) closeWith(reason error) error {
	s, r := sub.session, sub.registration
	s.mu.Lock()
	s.finishLocked(sub, reason)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := r.lock(ctx); e != nil {
		return e
	}
	defer r.unlock()
	s.mu.Lock()
	last, closed := len(r.users) == 0, s.closed
	s.mu.Unlock()
	if !last {
		return nil
	}
	var err error
	if !closed && r.attempted {
		r.registered = false
		r.attempted = false
		err = s.registrationRequest(ctx, r.path, http.MethodDelete)
	}
	s.mu.Lock()
	if len(r.users) == 0 && s.paths[r.path] == r {
		delete(s.paths, r.path)
	}
	s.mu.Unlock()
	return err
}
func (s *ruesSession) registrationRequest(ctx context.Context, path, method string) error {
	req, e := http.NewRequestWithContext(ctx, method, s.client.endpoint(path).String(), nil)
	if e != nil {
		return e
	}
	req.Header.Set("rusk-version", s.client.version)
	req.Header.Set("rusk-session-id", s.id)
	// Closing the session also aborts an outstanding registration HTTP call.
	request, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(sessionContext{s}, cancel)
	defer stop()
	resp, e := s.client.http.Do(req.WithContext(request))
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{resp.StatusCode, string(b), path}
	}
	return nil
}

// sessionContext ties registration I/O to the established session lifetime.
type sessionContext struct{ s *ruesSession }

func (x sessionContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (x sessionContext) Done() <-chan struct{}       { return x.s.done }
func (x sessionContext) Err() error {
	select {
	case <-x.s.done:
		return ErrDisconnected
	default:
		return nil
	}
}
func (x sessionContext) Value(any) any { return nil }

func (scope *Scope) Subscribe(ctx context.Context, topic string, buffer ...int) (*Subscription, error) {
	size := 64
	if len(buffer) > 0 {
		size = buffer[0]
	}
	if size < 1 {
		return nil, fmt.Errorf("network: subscription buffer must be positive")
	}
	path, e := scope.path(topic)
	if e != nil {
		return nil, e
	}
	s, e := scope.client.currentSession()
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrDisconnected
	}
	r := s.paths[path]
	if r == nil {
		r = &registration{gate: make(chan struct{}, 1), path: path, users: make(map[*Subscription]struct{})}
		r.gate <- struct{}{}
		s.paths[path] = r
	}
	life, end := context.WithCancel(context.Background())
	sub := &Subscription{session: s, registration: r, events: make(chan Event, size), done: make(chan struct{}), life: life, cancel: end}
	r.users[sub] = struct{}{}
	s.mu.Unlock()
	// Install before GET so events emitted before its ACK cannot be lost.
	operation, endOperation := sub.OperationContext(ctx)
	registrationCtx, cancel := scope.client.ReadContext(operation)
	if e = r.lock(registrationCtx); e == nil {
		if !r.registered {
			r.attempted = true
			e = s.registrationRequest(registrationCtx, path, http.MethodGet)
			r.registered = e == nil
		}
		r.unlock()
	}
	cancel()
	endOperation()
	if e != nil {
		s.mu.Lock()
		s.finishLocked(sub, e)
		s.mu.Unlock()
		go sub.closeWith(e)
		return nil, e
	}
	s.mu.Lock()
	if s.closed || sub.life.Err() != nil {
		err := sub.err
		if err == nil {
			err = ErrDisconnected
		}
		s.finishLocked(sub, err)
		s.mu.Unlock()
		go sub.Close()
		return nil, err
	}
	sub.stop = context.AfterFunc(ctx, func() { _ = sub.closeWith(ctx.Err()) })
	s.mu.Unlock()
	if e = ctx.Err(); e != nil {
		s.mu.Lock()
		s.finishLocked(sub, e)
		s.mu.Unlock()
		go sub.closeWith(e)
		return nil, e
	}
	return sub, nil
}
func (scope *Scope) Once(ctx context.Context, topic string) (Event, error) {
	ctx, cancel := scope.client.ReadContext(ctx)
	defer cancel()
	sub, e := scope.Subscribe(ctx, topic, 1)
	if e != nil {
		return Event{}, e
	}
	defer sub.Cancel()
	return sub.Next(ctx)
}
