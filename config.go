package artifactregistry

import (
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"time"
)

type ObjectStoreConfig struct {
	Endpoint, AccessKey, SecretKey, Bucket, BasePrefix string
	Secure                                             bool
	ConnectTimeout, ReadTimeout                        time.Duration
	RetryCount                                         int
	Credentials                                        *credentials.Credentials
}
type Config struct {
	DB          dbcore.Config
	Schema      string
	ObjectStore ObjectStoreConfig
}
type CleanupSettings struct {
	StagedRetention, AbortedRetention time.Duration
	MaxBatchSize                      int
	LockTimeout, CycleTimeout         time.Duration
}
type MaintenanceConfig struct {
	Runtime Config
	Cleanup CleanupSettings
}
type Clock interface{ Now() time.Time }
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type options struct{ clock Clock }
type Option func(*options)

func WithClock(clock Clock) Option { return func(o *options) { o.clock = clock } }
