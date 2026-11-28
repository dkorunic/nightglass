#!/bin/sh
# SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
# SPDX-License-Identifier: MIT
# smoke.sh: build the GUI in Wails server mode (no GTK/WebView needed), run it
# against origin-fake and drive the bound methods the way the frontend's
# Call.ByName does. Checks the Wails wiring Go tests cannot: binding names,
# the /frame/ route and the embedded assets. Run from the module root.
set -eu
FAKE=127.0.0.1:18080
APP=127.0.0.1:18081
mkdir -p dist
CGO_ENABLED=0 go build -tags server -o dist/app-server ./app
go build -o dist/origin-fake ./cmd/origin-fake
home=$(mktemp -d)
dist/origin-fake -listen "$FAKE" -live 1s >"$home/fake.log" 2>&1 &
fake=$!
HOME="$home" XDG_CONFIG_HOME="$home/.config" WAILS_SERVER_HOST=127.0.0.1 WAILS_SERVER_PORT=18081 \
  dist/app-server >"$home/app.log" 2>&1 &
app=$!
trap 'kill $fake $app 2>/dev/null; rm -rf "$home"' EXIT
sleep 2

fail() { echo "FAIL: $*"; echo "--- app log"; cat "$home/app.log"; exit 1; }

# call METHOD [JSON-ARGS...] prints the JSON result.
call() {
  m=$1; shift
  args=$(printf '%s,' "$@"); args="[${args%,}]"
  curl -s -X POST "http://$APP/wails/runtime" -H 'Content-Type: application/json' -d \
    "{\"object\":0,\"method\":0,\"args\":{\"call-id\":\"smoke\",\"methodName\":\"github.com/dkorunic/nightglass/app/control.App.$m\",\"args\":$args}}"
}
expect() { # expect DESCRIPTION OUTPUT ERE-PATTERN (grep -E: GNU and BSD grep agree)
  printf "%s\n" "$2" | grep -Eq -- "$3" || fail "$1: got $2" # not echo: dash expands \n
  echo "ok   $1"
}
status() { curl -s -o /dev/null -w '%{http_code} %{content_type}' "http://$APP$1"; }

for f in / /app.js /ui.js /targets.js /files.js /origin.js /mosaics.js /quickstart.js /gallery.js /style.css /wails/runtime.js; do
  expect "asset $f" "$(status "$f")" '^200'
done
expect "catalogs" "$(call Catalogs)" '"Name":"Solar System","Count":8.*"Name":"Messier","Count":110'
expect "save settings" "$(call SaveSettings '{"LatDeg":45.815,"LonDeg":15.982,"TimeZone":"Europe/Zagreb","Catalog":"Messier","ExposureSec":10,"ISO":200,"SaveDir":"'"$home"'/out"}')" '^\{\}$|^null$'
expect "invalid settings refused" "$(call SaveSettings '{"LatDeg":95}')" 'outside ±90'
expect "positions" "$(call Positions '"Messier"')" '^\[-?[0-9]'
expect "connect" "$(call Connect '" http://'"$FAKE"'/ "')" '^\{\}$|^null$'
sleep 2.5
expect "status" "$(call Status)" '"Key":"Mount/GetStatus"'
expect "live frame" "$(status /frame/live)" '^200 image/jpeg'
expect "bad slew rate refused" "$(call Slew 5 0)" 'want 0 or'
expect "slew" "$(call Slew 6 0)" '^\{\}$|^null$'
expect "stop slew" "$(call StopSlew)" '^\{\}$|^null$'
expect "focus" "$(call FocusTo 30500)" '^\{\}$|^null$'
expect "focus limits" "$(call FocusLimits)" '"Min":1000,"Max":35000'
expect "stretch" "$(call SetStretch '"Strength"' 0.7)" '^\{\}$|^null$'
expect "stretch read back" "$(call Stretch)" '"Strength":0.7'
expect "origin settings" "$(call OriginSettings)" '"Known":true,"Idle":true'
expect "LED ring" "$(call SetLedRing 0.3)" '^\{\}$|^null$'
expect "version info" "$(call OriginSettings)" '"Model":"Origin \(simulated\)","Version":"1.1.4248","Serial":"SIM-0001","Channel":"stable"'
expect "dark frame defaults" "$(call DarkDefaults)" '"ISO":200,"ExposureSec":10,"Count":10'
expect "image folders (none yet)" "$(call ImageDirectories)" '^\[\]$|^null$'
expect "below slew limit refused" "$(call Goto 0 -89)" 'Command Failure: .*below the horizon'
expect "supported ISOs" "$(call SupportedISOs)" '^\[100,200,500,1000,2000,5000,10000,20000\]$'
expect "solar system" "$(call Targets '"Solar System"')" '"Name":"Moon","Description":"","Type":"Moon"'
# Images where the telescope points (the fake reports 0°, within 0–85°),
# named after what is there: M 42 is selected but far away.
expect "image" "$(call Image '{"Catalog":"Messier","Name":"M 42"}')" '^\{\}$|^null$'
sleep 3.5
expect "stacked frame" "$(status /frame/stacked)" '^200 image/jpeg'
act=$(call Activity)
expect "status line shows the stack" "$act" '"Text":"Total Stacked Images: [0-9]*\\nTotal Integration Time: [0-9]* s\."'
expect "status line image" "$act" '"Image":"stacked"'
expect "finish imaging" "$(call FinishImaging)" '^\{\}$|^null$'
sleep 1
# Once saved, the masters are dropped and live view returns (as the app).
expect "live view after the save" "$(call Activity)" '"Image":"(live)?"'
expect "final saved as it arrived" "$(ls "$home"/out)" '^[^M].*-[0-9]{8}-[0-9]{6}\.png$'
expect "save final again" "$(call SaveFinal)" '\.png"?$'
expect "image folder listed" "$(call ImageDirectories)" '^\["'
saved=$(ls "$home"/out | head -1)
gallery=$(call Gallery)
expect "gallery" "$gallery" '"Name":"'"$saved"'"'
expect "gallery final" "$gallery" '"Final":true'
expect "gallery record" "$gallery" '"Title":"'
expect "saved image served" "$(status "/frame/saved/$saved")" '^200 image/png'
expect "edit overwrite" "$(call OverwriteImage '"'"$saved"'"' '{"Brightness":0.1,"Contrast":1.2,"Saturation":1,"Red":0,"Green":0,"Blue":0,"Rotate":1,"Crop":{"X":0,"Y":0,"W":0,"H":0}}')" '^\{\}$|^null$'
expect "export copy" "$(call ExportImage '"'"$saved"'"' '{"Brightness":0,"Contrast":1,"Saturation":1,"Red":0,"Green":0,"Blue":0,"Rotate":0,"Crop":{"X":0,"Y":0,"W":0,"H":0}}')" '^"?Nightglass_[0-9]+\.jpg"?$'
expect "favorite" "$(call SetFavorite '"'"$saved"'"' true)" '^\{\}$|^null$'
expect "quick start due" "$(call QuickStartDue)" '^true$'
expect "typed address checked" "$(call CheckHost '"127.0.0.1:1"')" 'Manual IP Address Invalid'
expect "second image" "$(call Image '{}')" '^\{\}$|^null$'
sleep 0.5
expect "cancel discards" "$(call CancelImaging)" '^\{\}$|^null$'
expect "mosaic plan" "$(call MosaicPlanFor '{"Catalog":"Messier","Name":"M 31"}')" '"DurationSec":600'
expect "mosaic grid" "$(call GridFor 120 60)" '"Cols":3,"Rows":2,"Tiles":6'
expect "mosaic start" "$(call StartMosaic '{"Catalog":"Messier","Name":"M 31","RA":10.68,"Dec":41.27,"WidthArcmin":60,"HeightArcmin":30,"RotationDeg":0,"DurationSec":60,"ExposureSec":10,"ISO":200,"StartAt":"0001-01-01T00:00:00Z"}')" '^\{\}$|^null$'
expect "mosaics listed" "$(call Mosaics)" '"Title":"M 31 Mosaic"'
expect "updates" "$(call Updates)" '"Channels":\["stable"'
expect "mosaic cancelled" "$(call CancelImaging)" '^\{\}$|^null$'
sleep 0.5
expect "logs" "$(call DownloadLogs)" '^\{\}$|^null$'
sleep 2
expect "logs saved" "$(ls "$home"/out)" 'origin-logs\.tar\.gz'
expect "power off" "$(call PowerOff)" '^\{\}$|^null$'
expect "disconnected after power off" "$(call Activity)" '"Text":"No Connection"'
expect "unknown method rejected" "$(call Bogus)" 'unknown bound method'
echo "smoke: all ok"
