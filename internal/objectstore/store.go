package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type Config struct {
	Endpoint, AccessKey, SecretKey, Bucket, BasePrefix string
	Secure                                             bool
	ConnectTimeout, ReadTimeout                        time.Duration
	RetryCount                                         int
	Credentials                                        *credentials.Credentials
}
type Store struct {
	client    *minio.Client
	config    Config
	transport *http.Transport
}

func New(c Config) (*Store, error) {
	if c.Endpoint == "" || c.Bucket == "" {
		return nil, errors.New("object storage endpoint and bucket are required")
	}
	if c.ConnectTimeout < 0 || c.ReadTimeout < 0 || c.RetryCount < 0 {
		return nil, errors.New("invalid object storage timeout or retry count")
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 3 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 10 * time.Second
	}
	creds := c.Credentials
	if creds == nil {
		if c.AccessKey == "" || c.SecretKey == "" {
			return nil, errors.New("object storage credentials are required")
		}
		creds = credentials.NewStaticV4(c.AccessKey, c.SecretKey, "")
	} else if !c.Secure || c.AccessKey != "" || c.SecretKey != "" {
		return nil, errors.New("dynamic credentials require TLS and cannot be combined with static credentials")
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: c.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: c.ConnectTimeout, ResponseHeaderTimeout: c.ReadTimeout, IdleConnTimeout: 90 * time.Second}
	client, err := minio.New(c.Endpoint, &minio.Options{Creds: creds, Secure: c.Secure, Transport: transport, MaxRetries: c.RetryCount + 1})
	if err != nil {
		return nil, err
	}
	return &Store{client, c, transport}, nil
}
func (s *Store) Close()         { s.transport.CloseIdleConnections() }
func (s *Store) Prefix() string { return strings.Trim(s.config.BasePrefix, "/") }
func (s *Store) EnsureBucket(ctx context.Context) error {
	if err := txscope.AssertExternalIOAllowed(ctx); err != nil {
		return err
	}
	if s.config.Credentials != nil {
		return nil
	}
	exists, err := s.client.BucketExists(ctx, s.config.Bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	err = s.client.MakeBucket(ctx, s.config.Bucket, minio.MakeBucketOptions{})
	if response := minio.ToErrorResponse(err); response.Code == "BucketAlreadyOwnedByYou" {
		return nil
	}
	return err
}
func (s *Store) Put(ctx context.Context, path string, payload []byte) error {
	if err := txscope.AssertExternalIOAllowed(ctx); err != nil {
		return err
	}
	if err := s.EnsureBucket(ctx); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, s.config.Bucket, path, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}
func (s *Store) Get(ctx context.Context, path string, start, length int64) ([]byte, error) {
	if err := txscope.AssertExternalIOAllowed(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.ReadTimeout)
	defer cancel()
	options := minio.GetObjectOptions{}
	if length >= 0 {
		if err := options.SetRange(start, start+length-1); err != nil {
			return nil, err
		}
	}
	object, err := s.client.GetObject(ctx, s.config.Bucket, path, options)
	if err != nil {
		return nil, err
	}
	defer object.Close()
	return io.ReadAll(object)
}
func (s *Store) Delete(ctx context.Context, path string) error {
	if err := txscope.AssertExternalIOAllowed(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.ReadTimeout)
	defer cancel()
	return s.client.RemoveObject(ctx, s.config.Bucket, path, minio.RemoveObjectOptions{})
}
