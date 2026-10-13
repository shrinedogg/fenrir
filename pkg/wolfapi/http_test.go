package wolfapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"games-on-whales.github.io/direwolf/pkg/wolfapi"
)

// The session controller calls the wolf-agent through the session Service
// ClusterIP. Right after the Service is created the CNI may not have
// programmed it yet and SYNs are silently dropped; without a timeout the
// kernel's connect timeout (~130s) blocks the reconcile worker past the
// 1-minute dead-session reaper. The client must give up on its own.
func TestHTTPClientTimesOutOnUnresponsiveServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	client := wolfapi.NewClient(srv.URL, wolfapi.NewHTTPClient(time.Second, 200*time.Millisecond))

	start := time.Now()
	_, err := client.ListSessions(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ListSessions against a hung server returned no error")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("ListSessions took %v; the request timeout was not applied", elapsed)
	}
}

func TestDefaultHTTPClientHasTimeout(t *testing.T) {
	c := wolfapi.NewHTTPClient(wolfapi.DefaultDialTimeout, wolfapi.DefaultRequestTimeout)
	if c.Timeout <= 0 {
		t.Fatalf("default wolf HTTP client has no request timeout (Timeout=%v)", c.Timeout)
	}
}
