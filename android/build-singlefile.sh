#!/bin/bash
# Rebuilds the classic-script SingleFile bundle used by the in-app clip mode.
# Run from the repo root whenever extension/single-file changes:
#   ./android/build-singlefile.sh
set -euo pipefail
cd "$(dirname "$0")/.."
node_modules/.bin/esbuild extension/single-file/single-file.js \
  --bundle \
  --format=iife \
  --global-name=CFSingleFile \
  --target=chrome90 \
  --outfile=android/app/src/main/assets/single-file-bundle.js
