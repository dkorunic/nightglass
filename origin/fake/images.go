// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"

	"golang.org/x/image/tiff"
)

// JPEG returns a small synthetic frame; frame shifts its gradient so
// successive live frames differ visibly.
func JPEG(frame int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := range 240 {
		for x := range 320 {
			v := uint8((x + y + frame*16) % 256)
			img.Set(x, y, color.RGBA{v / 3, v / 3, v, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)

	return b.Bytes()
}

// TIFF16 returns a 16-bit grayscale TIFF like a final stacked master.
func TIFF16() []byte {
	img := image.NewGray16(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.SetGray16(x, y, color.Gray16{Y: uint16(x * 1000)})
		}
	}
	var b bytes.Buffer
	_ = tiff.Encode(&b, img, nil)

	return b.Bytes()
}
