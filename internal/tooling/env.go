package tooling

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	registry "github.com/rubyu/artifact-registry-go"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/schema"
)

func required(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("missing required environment variable: %s", name)
	}
	return value, nil
}
func valueOr(name, def string) string {
	value, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	return value
}
func SchemaName() (string, error) {
	value := strings.TrimSpace(valueOr("ARTIFACT_REGISTRY_SCHEMA", "artifact_registry"))
	return value, dbcore.ValidateSchemaName(value)
}
func LoadDB(workload string) (dbcore.Config, error) {
	prefix := "ARTIFACT_REGISTRY_"
	if workload != "runtime" {
		prefix += strings.ToUpper(workload) + "_"
	}
	prefix += "POSTGRES_"
	values := map[string]string{}
	for _, key := range []string{"HOST", "PORT", "USER", "PASSWORD", "DATABASE"} {
		value, err := required(prefix + key)
		if err != nil {
			return dbcore.Config{}, err
		}
		values[key] = value
	}
	port, err := strconv.Atoi(values["PORT"])
	if err != nil || port < 1 || port > 65535 {
		return dbcore.Config{}, fmt.Errorf("invalid %sPORT", prefix)
	}
	mode := "require"
	switch os.Getenv("DEPLOYMENT_STAGE") {
	case "", "local", "testing", "ci":
		mode = "disable"
	}
	c := dbcore.Config{Host: values["HOST"], Port: port, User: values["USER"], Password: values["PASSWORD"], Database: values["DATABASE"], SSLMode: mode}
	if workload != "migration" {
		role, err := required("ARTIFACT_REGISTRY_DATABASE_" + strings.ToUpper(workload) + "_ROLE")
		if err != nil {
			return c, err
		}
		if err = dbcore.ValidateIdentifier(role); err != nil {
			return c, err
		}
		if c.User != role {
			return c, fmt.Errorf("%s database user must equal its configured workload role", workload)
		}
	}
	return c, nil
}
func LoadRoles() (schema.Roles, error) {
	var r schema.Roles
	for key, target := range map[string]*string{"OWNER": &r.Owner, "MIGRATOR": &r.Migrator, "RUNTIME": &r.Runtime, "MAINTENANCE": &r.Maintenance, "READINESS": &r.Readiness} {
		value, err := required("ARTIFACT_REGISTRY_DATABASE_" + key + "_ROLE")
		if err != nil {
			return r, err
		}
		*target = value
	}
	return r, r.Validate()
}
func positiveDuration(name, def string, unit time.Duration) (time.Duration, error) {
	raw := valueOr(name, def)
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > float64(math.MaxInt64)/float64(unit) {
		return 0, fmt.Errorf("invalid positive duration: %s", name)
	}
	return time.Duration(value * float64(unit)), nil
}
func LoadObjectStore() (registry.ObjectStoreConfig, error) {
	var c registry.ObjectStoreConfig
	for key, target := range map[string]*string{"ENDPOINT": &c.Endpoint, "ACCESS_KEY": &c.AccessKey, "SECRET_KEY": &c.SecretKey, "BUCKET": &c.Bucket} {
		value, err := required("ARTIFACT_REGISTRY_S3_" + key)
		if err != nil {
			return c, err
		}
		*target = value
	}
	c.BasePrefix = strings.Trim(valueOr("ARTIFACT_REGISTRY_BASE_PREFIX", "artifact-registry"), "/")
	switch valueOr("ARTIFACT_REGISTRY_S3_SECURE", "0") {
	case "1", "true", "TRUE", "yes":
		c.Secure = true
	}
	var err error
	c.ConnectTimeout, err = positiveDuration("ARTIFACT_REGISTRY_S3_CONNECT_TIMEOUT_SECONDS", "3", time.Second)
	if err != nil {
		return c, err
	}
	c.ReadTimeout, err = positiveDuration("ARTIFACT_REGISTRY_S3_READ_TIMEOUT_SECONDS", "10", time.Second)
	if err != nil {
		return c, err
	}
	c.RetryCount, err = strconv.Atoi(valueOr("ARTIFACT_REGISTRY_S3_RETRY_COUNT", "2"))
	if err != nil || c.RetryCount < 0 {
		return c, errors.New("invalid object storage retry count")
	}
	return c, nil
}
func LoadMaintenance() (registry.MaintenanceConfig, error) {
	var c registry.MaintenanceConfig
	var err error
	c.Runtime.Schema, err = SchemaName()
	if err != nil {
		return c, err
	}
	c.Runtime.DB, err = LoadDB("maintenance")
	if err != nil {
		return c, err
	}
	c.Runtime.ObjectStore, err = LoadObjectStore()
	if err != nil {
		return c, err
	}
	c.Cleanup.StagedRetention, err = positiveDuration("ARTIFACT_REGISTRY_STAGED_RETENTION_HOURS", "24", time.Hour)
	if err != nil {
		return c, err
	}
	c.Cleanup.AbortedRetention, err = positiveDuration("ARTIFACT_REGISTRY_ABORTED_RETENTION_HOURS", "24", time.Hour)
	if err != nil {
		return c, err
	}
	c.Cleanup.MaxBatchSize, err = strconv.Atoi(valueOr("ARTIFACT_REGISTRY_CLEANUP_MAX_BATCH_SIZE", "100"))
	if err != nil || c.Cleanup.MaxBatchSize <= 0 {
		return c, errors.New("invalid cleanup max batch size")
	}
	c.Cleanup.LockTimeout, err = positiveDuration("ARTIFACT_REGISTRY_CLEANUP_LOCK_TIMEOUT_SECONDS", "5", time.Second)
	if err != nil {
		return c, err
	}
	c.Cleanup.CycleTimeout, err = positiveDuration("ARTIFACT_REGISTRY_CLEANUP_CYCLE_TIMEOUT_SECONDS", "600", time.Second)
	return c, err
}
