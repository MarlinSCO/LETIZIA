#!/usr/bin/env bash
set -e
cd "$(dirname "$0")"
go test ./...
go build -trimpath -ldflags="-s -w" -o Letizia .
echo "Creato: ./Letizia"
