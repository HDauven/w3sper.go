// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package network

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/HDauven/w3sper.go/datadriver"
)

const (
	TransferContract = "0100000000000000000000000000000000000000000000000000000000000000"
	StakeContract    = "0200000000000000000000000000000000000000000000000000000000000000"
)

// WithTimeout sets the default read/handshake budget. Zero disables it.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

type timeoutKey struct{}

// WithRequestTimeout overrides the network budget for a context (zero disables
// the default only; cancellation and parent deadlines remain effective).
func WithRequestTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, timeoutKey{}, d)
}
func (c *Client) ReadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	d := c.timeout
	if v, ok := ctx.Value(timeoutKey{}).(time.Duration); ok {
		d = v
	}
	// Preserve explicit timeout-zero semantics through data-driver loading and
	// decoding, rather than accidentally restoring its standalone default.
	ctx = datadriver.WithRequestTimeout(ctx, d)
	if d < 0 {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled, cancel
	}
	if d == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
func WithOperationName(name string) QueryOption { return func(r *Request) { r.OperationName = name } }

// HTTPError retains node rejection text and status, including preverification.
type HTTPError struct {
	Status int
	Body   string
	URL    string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("network: HTTP %d: %s", e.Status, e.Body) }

type Scope struct {
	client   *Client
	Name, ID string
}

func (c *Client) Scope(name string) *Scope { return &Scope{client: c, Name: name} }
func (s *Scope) WithID(id string) *Scope   { return &Scope{client: s.client, Name: s.Name, ID: id} }
func (c *Client) Contracts() *Scope        { return c.Scope("contracts") }
func (c *Client) Transactions() *Scope     { return c.Scope("transactions") }
func (c *Client) Blocks() *Scope           { return c.Scope("blocks") }
func (s *Scope) path(topic string) (string, error) {
	for _, v := range []string{s.Name, s.ID, topic} {
		if strings.ContainsAny(v, "/\\?#\r\n\x00") {
			return "", fmt.Errorf("network: invalid scope/id/topic")
		}
	}
	if s.Name == "" || topic == "" {
		return "", fmt.Errorf("network: scope and topic required")
	}
	target := s.Name
	if s.ID != "" {
		target += ":" + s.ID
	}
	return "/on/" + target + "/" + topic, nil
}

type CallOptions struct {
	Feeder  bool
	Headers http.Header
}

type budgetBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *budgetBody) Close() error { e := b.ReadCloser.Close(); b.cancel(); return e }
func (b *budgetBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil {
		b.cancel()
	}
	return n, e
}

// Stream returns a response body whose budget lasts through consumption. The
// caller MUST close the body. A canceled context closes even a paused stream.
func (s *Scope) Stream(ctx context.Context, topic string, body []byte, opts ...CallOptions) (*http.Response, error) {
	path, e := s.path(topic)
	if e != nil {
		return nil, e
	}
	body = bytes.Clone(body)
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	o.Headers = o.Headers.Clone()
	ctx, cancel := s.client.ReadContext(ctx)
	request := func(feeder bool) (*http.Response, error) {
		u := *s.client.URL()
		u.Path = path
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
		if e != nil {
			return nil, e
		}
		req.Header = o.Headers.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		req.Header.Set("rusk-version", s.client.version)
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/octet-stream")
		}
		if feeder {
			req.Header.Set("Rusk-feeder", "true")
		}
		return s.client.http.Do(req)
	}
	resp, e := request(o.Feeder)
	if e != nil {
		cancel()
		return nil, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			cancel()
			return nil, readErr
		}
		if !o.Feeder && strings.Contains(strings.ToLower(string(b)), "missing feed") {
			resp, e = request(true)
			if e != nil {
				cancel()
				return nil, e
			}
		} else {
			cancel()
			return nil, &HTTPError{resp.StatusCode, string(b), path}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		if e != nil {
			return nil, e
		}
		return nil, &HTTPError{resp.StatusCode, string(b), path}
	}
	resp.Body = &budgetBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}
func (s *Scope) Call(ctx context.Context, topic string, body []byte, opts ...CallOptions) ([]byte, error) {
	resp, e := s.Stream(ctx, topic, body, opts...)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
func (c *Client) Ping(ctx context.Context) error { _, e := NewNode(c).Info(ctx); return e }
func (c *Client) Prove(ctx context.Context, circuits []byte) ([]byte, error) {
	if len(circuits) == 0 {
		return nil, fmt.Errorf("network: circuits are empty")
	}
	return c.Scope("prover").Call(ctx, "prove", circuits)
}
func (c *Client) Preverify(ctx context.Context, tx []byte) error {
	if len(tx) == 0 {
		return fmt.Errorf("network: empty transaction")
	}
	_, e := c.Transactions().Call(ctx, "preverify", tx)
	return e
}
func (c *Client) Propagate(ctx context.Context, tx []byte) error {
	if len(tx) == 0 {
		return fmt.Errorf("network: empty transaction")
	}
	_, e := c.Transactions().Call(ctx, "propagate", tx)
	return e
}

// Execute preverifies before propagation. Successful return means accepted by
// the node, not included in a block. Use Subscribe/Once on transactions:<hash>.
func (c *Client) Execute(ctx context.Context, tx []byte) error {
	tx = bytes.Clone(tx)
	if e := c.Preverify(ctx, tx); e != nil {
		return e
	}
	return c.Propagate(ctx, tx)
}
func (c *Client) endpoint(path string) *url.URL { u := *c.URL(); u.Path = path; return &u }
