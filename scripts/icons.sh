#!/bin/sh
# SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
# SPDX-License-Identifier: MIT
# icons.sh: render app/build/icon.svg into the icons the builds use:
# icon.png (window and Linux), icon.icns (macOS bundle), icon.ico (Windows).
# Needs rsvg-convert and ImageMagick; icon.icns also needs macOS iconutil.
# Run from the module root after changing the SVG; commit the results.
set -eu
cd app/build
rsvg-convert -w 1024 -h 1024 icon.svg -o icon.png
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for s in 16 32 48 64 128 256; do rsvg-convert -w $s -h $s icon.svg -o "$tmp/$s.png"; done
magick "$tmp/16.png" "$tmp/32.png" "$tmp/48.png" "$tmp/64.png" "$tmp/128.png" "$tmp/256.png" icon.ico
if command -v iconutil >/dev/null; then
  set_=$tmp/icon.iconset
  mkdir "$set_"
  for s in 16 32 128 256 512; do
    rsvg-convert -w $s -h $s icon.svg -o "$set_/icon_${s}x${s}.png"
    rsvg-convert -w $((s * 2)) -h $((s * 2)) icon.svg -o "$set_/icon_${s}x${s}@2x.png"
  done
  iconutil -c icns "$set_" -o icon.icns
fi
