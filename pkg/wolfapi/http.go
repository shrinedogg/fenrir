package wolfapi

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	// DefaultDialTimeout bounds the TCP connect to the wolf-agent. Right after
	// a session Service is created the CNI may not have programmed its
	// ClusterIP yet and SYNs are silently dropped; the kernel default connect
	// timeout (~130s) outlives the 1-minute dead-session reaper.
	DefaultDialTimeout = 5 * time.Second
	// DefaultRequestTimeout bounds a whole request/response exchange. Wolf
	// answers the session endpoints immediately (sessions/add does not wait
	// for the stream), so 30s is generous.
	DefaultRequestTimeout = 30 * time.Second
)

// NewHTTPClient returns the HTTP client used for request/response calls to the
// wolf-agent API. The agent serves a self-signed certificate, hence
// InsecureSkipVerify. Do not use it for SubscribeToEvents: the request
// timeout would cut the long-lived SSE stream.
func NewHTTPClient(dialTimeout, requestTimeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: dialTimeout}).DialContext,
			TLSHandshakeTimeout: dialTimeout,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		},
	}
}
