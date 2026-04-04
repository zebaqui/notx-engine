#!/usr/bin/env bash
# scripts/build_ios.sh — build the notx engine as an iOS xcframework via gomobile bind
set -euo pipefail

# Requirements:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init
#   Xcode 15+ with iOS 16+ SDK

PACKAGE="github.com/zebaqui/notx-engine/mobile"
OUTPUT="build/NotxEngine.xcframework"

mkdir -p build

gomobile bind \
  -target ios,iossimulator \
  -o "${OUTPUT}" \
  -iosversion 16.0 \
  -tags ios \
  "${PACKAGE}"

echo "Built ${OUTPUT}"
