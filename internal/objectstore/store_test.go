package objectstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestConfigurationValidation(t *testing.T) {
	base := Config{Endpoint: "localhost:9000", Bucket: "test", AccessKey: "access", SecretKey: "secret", RetryCount: 2}
	for _, change := range []func(*Config){func(c *Config) { c.Endpoint = "" }, func(c *Config) { c.Bucket = "" }, func(c *Config) { c.SecretKey = "" }, func(c *Config) { c.ConnectTimeout = -time.Second }, func(c *Config) { c.RetryCount = -1 }, func(c *Config) { c.Credentials = credentials.NewStaticV4("a", "b", "") }} {
		c := base
		change(&c)
		if _, err := New(c); err == nil {
			t.Fatal(c)
		}
	}
	store, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
}

func TestDeleteResponseBodyIsBounded(t *testing.T) {
	for _, callerCancellation := range []bool{false, true} {
		name := "read timeout"
		if callerCancellation {
			name = "caller cancellation"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Query().Has("location") {
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
					return
				}
				if r.Method != http.MethodDelete {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				// header を受信できても、error body が止まる場合を再現する。
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusForbidden)
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			readTimeout := 100 * time.Millisecond
			if callerCancellation {
				readTimeout = time.Minute
			}
			store, err := New(Config{Endpoint: strings.TrimPrefix(server.URL, "http://"), Bucket: "test-bucket", AccessKey: "access", SecretKey: "secret", ReadTimeout: readTimeout})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- store.Delete(ctx, "object") }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("DELETE did not start")
			}
			if callerCancellation {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled DELETE succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("DELETE exceeded its read timeout or ignored caller cancellation")
			}
		})
	}
}
