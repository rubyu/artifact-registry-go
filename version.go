package artifactregistry

import "github.com/rubyu/artifact-registry-go/internal/buildinfo"

// Version は取得した Go module の version、または開発 checkout の dev を返す。
func Version() string { return buildinfo.LibraryVersion() }
