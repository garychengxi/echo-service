#!/usr/bin/env sh
set -eu
out="${1:-output/echo-service-docker.tar.gz}"
mkdir -p "$(dirname "$out")"
tar -czf "$out" \
  --exclude='.env' \
  --exclude='data' \
  --exclude='./.git' \
  --exclude='./data' \
  --exclude='./output' \
  go.mod go.sum vendor cmd internal service sdk internalsdk conf deploy/docker scripts/package-docker.sh README.md .dockerignore
printf '%s\n' "$out"
