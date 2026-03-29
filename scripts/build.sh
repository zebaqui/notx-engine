#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# scripts/build.sh — full notx build
#
# 1. Builds the admin UI (Vite → ui/admin/dist/)
# 2. Compiles the Go binary (embeds the dist/ output)
#
# Usage:
#   ./scripts/build.sh [--skip-ui] [--output <path>]
#
# Options:
#   --skip-ui      Skip the npm build step (use an existing dist/)
#   --output PATH  Where to write the binary (default: bin/notx)
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── Resolve project root (directory containing this script's parent) ─────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── Defaults ─────────────────────────────────────────────────────────────────
SKIP_UI=false
OUTPUT="${ROOT_DIR}/bin/notx"

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-ui)
      SKIP_UI=true
      shift
      ;;
    --output)
      OUTPUT="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1" >&2
      echo "Usage: $0 [--skip-ui] [--output <path>]" >&2
      exit 1
      ;;
  esac
done

# ── Helpers ───────────────────────────────────────────────────────────────────
log()  { printf '\033[1;34m▶\033[0m  %s\n' "$*"; }
ok()   { printf '\033[1;32m✓\033[0m  %s\n' "$*"; }
fail() { printf '\033[1;31m✗\033[0m  %s\n' "$*" >&2; exit 1; }

# ── Step 1: Admin UI ──────────────────────────────────────────────────────────
ADMIN_DIR="${ROOT_DIR}/ui/admin"
DIST_DIR="${ADMIN_DIR}/dist"

if [[ "${SKIP_UI}" == "true" ]]; then
  log "Skipping UI build (--skip-ui)"
  if [[ ! -d "${DIST_DIR}" ]]; then
    fail "dist/ not found at ${DIST_DIR} — run without --skip-ui first"
  fi
else
  log "Building admin UI  (${ADMIN_DIR})"

  if [[ ! -f "${ADMIN_DIR}/package.json" ]]; then
    fail "package.json not found at ${ADMIN_DIR}"
  fi

  # Install deps only when node_modules is missing or package-lock changed
  if [[ ! -d "${ADMIN_DIR}/node_modules" ]]; then
    log "  npm install"
    npm --prefix "${ADMIN_DIR}" install --silent
  fi

  log "  npm run build"
  npm --prefix "${ADMIN_DIR}" run build

  if [[ ! -d "${DIST_DIR}" ]]; then
    fail "Vite build did not produce dist/ at ${DIST_DIR}"
  fi

  ok "Admin UI built → ${DIST_DIR}"
fi

# ── Step 1b: Stage dist/ into the Go embed directory ─────────────────────────
EMBED_DIR="${ROOT_DIR}/internal/admin/ui"

log "Staging dist/ → ${EMBED_DIR}"
rm -rf "${EMBED_DIR}"
cp -R "${DIST_DIR}" "${EMBED_DIR}"
ok "Staged admin UI → ${EMBED_DIR}"

# ── Step 2: Go binary ─────────────────────────────────────────────────────────
log "Compiling Go binary → ${OUTPUT}"

mkdir -p "$(dirname "${OUTPUT}")"

# Capture build metadata
VERSION="${VERSION:-dev}"
COMMIT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

cd "${ROOT_DIR}"

go build \
  -ldflags "-s -w \
    -X 'github.com/zebaqui/notx-engine/internal/buildinfo.Version=${VERSION}' \
    -X 'github.com/zebaqui/notx-engine/internal/buildinfo.Commit=${COMMIT}' \
    -X 'github.com/zebaqui/notx-engine/internal/buildinfo.BuildTime=${BUILD_TIME}'" \
  -o "${OUTPUT}" \
  ./cmd/notx

ok "Binary built → ${OUTPUT}"

# ── Done ──────────────────────────────────────────────────────────────────────
printf '\n\033[1;32m✓ Build complete\033[0m\n'
printf '  Binary:   %s\n' "${OUTPUT}"
printf '  Version:  %s (%s)\n' "${VERSION}" "${COMMIT}"
printf '  Built at: %s\n\n' "${BUILD_TIME}"
