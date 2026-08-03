#!/bin/sh
set -eu

repository=$(pwd)
consumer=$(mktemp -d)
trap 'rm -rf "$consumer"' EXIT INT TERM

cp testdata/consumer/main.go "$consumer/main.go"
cd "$consumer"
go mod init example.com/odp-consumer
go mod edit -replace "github.com/offering-protocol/odp-go=$repository"
go mod tidy
result=$(go run .)
test "$result" = "1.0"
