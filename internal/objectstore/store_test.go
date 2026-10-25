package objectstore

import (
	"github.com/minio/minio-go/v7/pkg/credentials"
	"testing"
	"time"
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
