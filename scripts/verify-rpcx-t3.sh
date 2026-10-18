#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"
mkdir -p runs/go-cache runs/module-cache
export GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local
export GOFLAGS=-mod=readonly
export GOCACHE="$repo_root/runs/go-cache"
export GOMODCACHE="$repo_root/runs/module-cache"
go run ./scenarios/rpcx/acceptance/cmd/verify "$@"
