package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	registry "github.com/rubyu/artifact-registry-go"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/txscope"
)

func run(ctx context.Context) error {
	port, err := strconv.Atoi(os.Getenv("ARTIFACT_REGISTRY_POSTGRES_PORT"))
	if err != nil {
		return err
	}
	config := registry.Config{DB: dbcore.Config{Host: os.Getenv("ARTIFACT_REGISTRY_POSTGRES_HOST"), Port: port, User: os.Getenv("ARTIFACT_REGISTRY_POSTGRES_USER"), Password: os.Getenv("ARTIFACT_REGISTRY_POSTGRES_PASSWORD"), Database: os.Getenv("ARTIFACT_REGISTRY_POSTGRES_DATABASE"), SSLMode: "require"}, Schema: os.Getenv("ARTIFACT_REGISTRY_SCHEMA"), ObjectStore: registry.ObjectStoreConfig{Endpoint: os.Getenv("ARTIFACT_REGISTRY_S3_ENDPOINT"), AccessKey: os.Getenv("ARTIFACT_REGISTRY_S3_ACCESS_KEY"), SecretKey: os.Getenv("ARTIFACT_REGISTRY_S3_SECRET_KEY"), Bucket: os.Getenv("ARTIFACT_REGISTRY_S3_BUCKET"), BasePrefix: "example", Secure: true, RetryCount: 2}}
	r, err := registry.New(ctx, config)
	if err != nil {
		return err
	}
	defer r.Close()
	prepared, err := r.PrepareBytes(ctx, []byte("artifact example"))
	if err != nil {
		return err
	}
	if err = r.RunTx(ctx, func(tx *txscope.Scope) error { _, err := r.CommitPrepared(tx.Context(), tx, prepared); return err }); err != nil {
		return err
	}
	var artifact *registry.CommittedArtifact
	if err = r.RunTx(ctx, func(tx *txscope.Scope) error {
		var err error
		artifact, err = r.LookupCommittedPublication(tx.Context(), tx, prepared.Ref())
		return err
	}); err != nil {
		return err
	}
	if artifact == nil {
		return fmt.Errorf("committed artifact was not found")
	}
	payload, err := r.ReadBytes(ctx, artifact)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", artifact.Ref(), payload)
	return nil
}
func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
