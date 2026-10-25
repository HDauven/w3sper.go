package datadriver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// Source loads bytes without relying on a JavaScript process. Use Bytes, File,
// URL, or a context-aware function. Registry owns the loaded WASM instances.
type Source func(context.Context) ([]byte, error)

func Bytes(b []byte) Source {
	b = append([]byte(nil), b...)
	return func(ctx context.Context) ([]byte, error) { return append([]byte(nil), b...), ctx.Err() }
}
func File(path string) Source {
	return func(ctx context.Context) ([]byte, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		return os.ReadFile(path)
	}
}
func URL(location string, client *http.Client) Source {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return func(ctx context.Context) ([]byte, error) {
		u, e := url.Parse(location)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("datadriver: invalid URL")
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if e != nil {
			return nil, e
		}
		resp, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("datadriver: HTTP %d", resp.StatusCode)
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, 64<<20+1))
		if len(b) > 64<<20 {
			return nil, fmt.Errorf("datadriver: WASM exceeds 64 MiB")
		}
		return b, e
	}
}

type Registry struct {
	mu       sync.Mutex
	entries  map[string]*entry
	owned    []*Driver
	closed   bool
	lifetime context.Context
	cancel   context.CancelFunc
}
type entry struct {
	source  Source
	driver  *Driver
	loading chan struct{}
}

func NewRegistry() *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{entries: make(map[string]*entry), lifetime: ctx, cancel: cancel}
}
func (r *Registry) Register(key string, source Source) error {
	if key == "" || source == nil {
		return fmt.Errorf("datadriver: key and source required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.entries[key] = &entry{source: source}
	return nil
}

// Delete unregisters a source. Existing users retain their driver until Close.
func (r *Registry) Delete(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.entries[key]
	delete(r.entries, key)
	return ok
}
func (r *Registry) Has(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.entries[key] != nil
}
func (r *Registry) Get(ctx context.Context, key string) (*Driver, error) {
	ctx, cancel := readBudget(ctx)
	defer cancel()
	stop := context.AfterFunc(r.lifetime, cancel)
	defer stop()
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return nil, ErrClosed
		}
		en := r.entries[key]
		if en == nil {
			r.mu.Unlock()
			return nil, fmt.Errorf("datadriver: no driver registered for %s", key)
		}
		if en.driver != nil {
			d := en.driver
			r.mu.Unlock()
			return d, nil
		}
		if en.loading != nil {
			done := en.loading
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		en.loading = make(chan struct{})
		done := en.loading
		r.mu.Unlock()
		b, e := en.source(ctx)
		var d *Driver
		if e == nil {
			d, e = Load(ctx, b)
		}
		r.mu.Lock()
		en.loading = nil
		close(done)
		if e == nil {
			if r.closed {
				r.mu.Unlock()
				cleanup, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				_ = d.Close(cleanup)
				return nil, ErrClosed
			}
			en.driver = d
			r.owned = append(r.owned, d)
		}
		r.mu.Unlock()
		return d, e
	}
}
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	owned := append([]*Driver(nil), r.owned...)
	r.mu.Unlock()
	var first error
	for _, d := range owned {
		if e := d.Close(ctx); first == nil {
			first = e
		}
	}
	if first == nil {
		r.mu.Lock()
		r.owned = nil
		r.entries = make(map[string]*entry)
		r.mu.Unlock()
	}
	return first
}
