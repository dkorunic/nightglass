// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package fake

import (
	"bytes"
	"image/jpeg"
	"testing"
)

// JPEG encodes an 8-bit copy of the frame; the bytes are what encoding the
// 16-bit frame itself makes.
func TestSkyJPEGMatchesReference(t *testing.T) {
	for _, s := range []Sky{
		{RA: 83.8, Dec: -5.4, Width: OriginField, Rotation: OriginOrientation, W: 320, H: 214, Depth: 1, NoiseSeed: 3},
		{RA: 10, Dec: 89.5, Width: 2, Rotation: 33, W: 97, H: 61, Depth: 9, Blur: 2},
		{RA: 359.9, Dec: 0, Width: 0.5, W: 1, H: 1},
	} {
		var want bytes.Buffer
		if err := jpeg.Encode(&want, s.Image(), &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(s.JPEG(), want.Bytes()) {
			t.Errorf("%+v: JPEG differs from encoding Image", s)
		}
	}
}
