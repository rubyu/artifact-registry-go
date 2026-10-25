#!/usr/bin/env bash
set -euo pipefail
: "${VERSION:?VERSION is required}"
: "${COMMIT:?COMMIT is required}"
: "${DATE:?DATE is required}"

mkdir -p dist
package=github.com/rubyu/artifact-registry-go/internal/buildinfo
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  export GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0
  folder="dist/artifact-registry_${VERSION}_${GOOS}_${GOARCH}"
  mkdir -p "$folder"
  suffix=""
  if [ "$GOOS" = windows ]; then suffix=.exe; fi
  for command in artifact-registry-migrate artifact-registry-maintenance; do
    go build -trimpath -ldflags "-s -w -X ${package}.Version=${VERSION} -X ${package}.Commit=${COMMIT} -X ${package}.Date=${DATE}" -o "$folder/$command$suffix" "./cmd/$command"
  done
  cp dist/version.json "$folder/version.json"
  cp README.md "$folder/README.md"
  if [ "$GOOS" = windows ]; then
    (cd "$folder" && zip -q -r "../$(basename "$folder").zip" .)
  else
    tar -czf "$folder.tar.gz" -C "$folder" .
  fi
  rm -r "$folder"
done
(cd dist && sha256sum *.tar.gz *.zip version.json > checksums.txt)
printf 'Commit: `%s`\n\nGo module: `github.com/rubyu/artifact-registry-go@%s`\n\nCLI の `--version` と `version.json` に完全な commit hash を記録しています。\n' "$COMMIT" "$VERSION" > dist/RELEASE.md
