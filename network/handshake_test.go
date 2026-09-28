package network

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCancelPendingHTTPAndTLSUpgrade(t *testing.T) {
	for _, tls := range []bool{false, true} {
		name := "HTTP"
		if tls {
			name = "TLS"
		}
		t.Run(name, func(t *testing.T) {
			entered, ended := make(chan struct{}), make(chan struct{})
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(ended) }))
			if tls {
				s.StartTLS()
			} else {
				s.Start()
			}
			defer s.Close()
			c, e := New(s.URL, WithHTTPClient(s.Client()), WithTimeout(10*time.Second))
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- c.Connect(ctx) }()
			<-entered
			cancel()
			select {
			case e = <-result:
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("HTTP upgrade wasn't interrupted")
			}
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("server socket leaked")
			}
			if c.Connected() {
				t.Fatal("late session after cancellation")
			}
		})
	}
}
