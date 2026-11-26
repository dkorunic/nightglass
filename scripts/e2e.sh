#!/bin/sh
# SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
# SPDX-License-Identifier: MIT
# e2e.sh: GUI tests. Builds the GUI in Wails server mode and origin-fake,
# runs both, and drives the frontend in headless Chrome (scripts/e2e.mjs;
# set CHROME if Chrome is not where cdp.mjs looks). Run from the module root.
set -eu
FAKE=127.0.0.1:18090
APP=127.0.0.1:18091
mkdir -p dist
CGO_ENABLED=0 go build -tags server -o dist/app-server ./app
go build -o dist/origin-fake ./cmd/origin-fake
home=$(mktemp -d)
dist/origin-fake -listen "$FAKE" -live 1s >"$home/fake.log" 2>&1 &
fake=$!
HOME="$home" XDG_CONFIG_HOME="$home/.config" WAILS_SERVER_HOST=127.0.0.1 WAILS_SERVER_PORT=18091 \
  dist/app-server >"$home/app.log" 2>&1 &
app=$!
trap 'kill $fake $app 2>/dev/null; rm -rf "$home"' EXIT
sleep 2
APP=$APP FAKE=$FAKE node --test --test-concurrency=1 scripts/e2e.mjs || { echo "--- app log"; tail -40 "$home/app.log"; exit 1; }
