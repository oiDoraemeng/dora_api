package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamctx"
)

func TestUpstreamDisconnectCancelsHeaderWait(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(upstreamctx.Detached(parent), http.MethodGet, server.URL, nil)
	finished := make(chan error, 1)
	go func() {
		resp, err := doUpstreamRequest(server.Client(), req)
		if resp != nil {
			resp.Body.Close()
		}
		finished <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled header wait succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream still running after client left")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe connection cancellation")
	}
}

func TestUpstreamDisconnectPreservesFinalUsageAfterHeaders(t *testing.T) {
	finish := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-finish
		_, _ = io.WriteString(w, "data: final usage\n\n")
	}))
	defer server.Close()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(upstreamctx.Detached(parent), http.MethodGet, server.URL, nil)
	resp, err := doUpstreamRequest(server.Client(), req)
	if err != nil {
		close(finish)
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cancel()
	close(finish)
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "data: final usage\n\n" {
		t.Fatalf("lost final usage: %q %v", body, err)
	}
}
