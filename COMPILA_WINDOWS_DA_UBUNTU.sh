#!/usr/bin/env bash
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o Letizia.exe .
echo "Creato: ./Letizia.exe"
