#!/usr/bin/env bash
set -e
cd "$(dirname "$0")"
if ! command -v go >/dev/null 2>&1; then
  echo "Go non è installato. Installa Go e riprova."
  exit 1
fi
go run .
