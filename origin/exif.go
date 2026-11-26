// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package origin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"slices"
	"time"
)

// Metadata is what the app writes into a saved final image
// (SkySafariActivityImaging.getEXIFMetadataFromPayload and BitmapWithMetadata).
type Metadata struct {
	Taken          time.Time // zero when the payload has no valid Date and Time
	Uuid           string
	ExposureSec    int // the app keeps whole seconds
	ISO            int
	StackDepth     int
	LatDeg, LonDeg float64
	HasLocation    bool
}

// FinalMetadata is a final image's metadata. Its position is the payload's
// (radians) when not 0, 0, else the observing site (degrees), as the app
// falls back to the device's location; a 0, 0 site is no location.
func FinalMetadata(info ImageReady, siteLatDeg, siteLonDeg float64) Metadata {
	m := Metadata{Uuid: info.Uuid, ExposureSec: int(info.ExposureTime), ISO: info.ISO, StackDepth: info.StackDepth}
	// Origin.ORIGIN_DATE_FORMAT and ORIGIN_TIME_FORMAT.
	if t, err := time.Parse("02 01 2006 15:04:05", info.Date+" "+info.Time); err == nil {
		m.Taken = t
	}
	switch {
	case info.Latitude != 0 || info.Longitude != 0:
		m.LatDeg, m.LonDeg, m.HasLocation = info.Latitude*180/math.Pi, info.Longitude*180/math.Pi, true
	case siteLatDeg != 0 || siteLonDeg != 0:
		m.LatDeg, m.LonDeg, m.HasLocation = siteLatDeg, siteLonDeg, true
	}

	return m
}

// xmp is the app's XMP packet naming the image's Uuid.
func xmp(uuid string) string {
	return "<x:xmpmeta xmlns:x=\"adobe:ns:meta/\" >\n   <rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\">\n" +
		"      <rdf:Description rdf:about=\"\"\n            xmlns:xapMM=\"http://ns.adobe.com/xap/1.0/mm/\">\n" +
		"         <xapMM:DocumentID>" + uuid + "</xapMM:DocumentID>\n      </rdf:Description>\n   </rdf:RDF>\n</x:xmpmeta>"
}

// PNGWithExif returns png with m in an eXIf chunk right after IHDR, where
// Android's ExifInterface puts it.
func PNGWithExif(png []byte, m Metadata) ([]byte, error) {
	const head = 8 + 4 + 4 + 13 + 4 // signature and the IHDR chunk
	if len(png) < head || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || string(png[12:16]) != "IHDR" {
		return nil, errors.New("origin: not a PNG")
	}
	data := exifTIFF(m)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, exifChunk...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))

	return append(append(append([]byte(nil), png[:head]...), chunk...), png[head:]...), nil
}

// CopyExif returns dst with src's eXIf chunk (if any) after its IHDR, so
// an edited image keeps the original's metadata.
func CopyExif(src, dst []byte) ([]byte, error) {
	const head = 8 + 4 + 4 + 13 + 4
	if len(dst) < head || !bytes.HasPrefix(dst, []byte("\x89PNG\r\n\x1a\n")) || string(dst[12:16]) != "IHDR" {
		return nil, errors.New("origin: not a PNG")
	}
	for i := 8; i+12 <= len(src); {
		n := int(binary.BigEndian.Uint32(src[i:]))
		end := i + 12 + n
		if n < 0 || end > len(src) {
			break
		}
		if string(src[i+4:i+8]) == exifChunk {
			return append(append(append([]byte(nil), dst[:head]...), src[i:end]...), dst[head:]...), nil
		}
		i = end
	}

	return dst, nil
}

// exifChunk is the PNG chunk type that carries EXIF.
const exifChunk = "eXIf"

// TIFF field types.
const (
	tByte     = 1
	tASCII    = 2
	tShort    = 3
	tLong     = 4
	tRational = 5
)

type field struct {
	data     []byte
	count    uint32
	tag, typ uint16
}

var be = binary.BigEndian

func ascii(tag uint16, s string) field {
	return field{tag: tag, typ: tASCII, count: uint32(len(s) + 1), data: append([]byte(s), 0)}
}

func shorts(tag uint16, v ...int) field {
	var b []byte
	for _, x := range v {
		b = be.AppendUint16(b, uint16(x))
	}

	return field{tag: tag, typ: tShort, count: uint32(len(v)), data: b}
}

func rational(tag uint16, num ...uint32) field { // num/den pairs
	var b []byte
	for _, x := range num {
		b = be.AppendUint32(b, x)
	}

	return field{tag: tag, typ: tRational, count: uint32(len(num) / 2), data: b}
}

// dms is ExifInterface.convertDecimalDegree: d/1, m/1, s·10⁷/10⁷.
func dms(tag uint16, deg float64) field {
	deg = math.Abs(deg)
	d := math.Floor(deg)
	mi := math.Floor((deg - d) * 60)
	s := math.Round((deg - d - mi/60) * 3600 * 1e7)

	return rational(tag, uint32(d), 1, uint32(mi), 1, uint32(s), 1e7)
}

// exifTIFF lays out IFD0 (XMP and the two pointers), the Exif IFD and the
// GPS IFD, big-endian like ExifInterface. Tags are in ascending order.
func exifTIFF(m Metadata) []byte {
	var exif, gps, ifd0 []field
	if m.Uuid != "" { // a final image; a snapshot has only its location
		exif = []field{
			rational(33434, uint32(max(m.ExposureSec, 0)), 1), // ExposureTime
			shorts(34855, m.ISO),                              // PhotographicSensitivity (ISOSpeedRatings)
		}
		if !m.Taken.IsZero() {
			exif = append(exif, ascii(36867, m.Taken.Format("2006:01:02 15:04:05"))) // DateTimeOriginal
		}
		exif = append(exif,
			ascii(42016, m.Uuid),                      // ImageUniqueID
			shorts(42080, 3),                          // CompositeImage: composite captured in one go
			shorts(42081, m.StackDepth, m.StackDepth), // SourceImageNumberOfCompositeImage
		)
		x := []byte(xmp(m.Uuid))
		ifd0 = []field{{tag: 700, typ: tByte, count: uint32(len(x)), data: x}, {tag: 34665, typ: tLong, count: 1}}
	}
	if m.HasLocation {
		ns, ew := "N", "E"
		if m.LatDeg < 0 {
			ns = "S"
		}
		if m.LonDeg < 0 {
			ew = "W"
		}
		gps = []field{ascii(1, ns), dms(2, m.LatDeg), ascii(3, ew), dms(4, m.LonDeg)}
		ifd0 = append(ifd0, field{tag: 34853, typ: tLong, count: 1})
	}

	return layout(ifd0, exif, gps)
}

// layout writes IFD0, the Exif IFD and the GPS IFD (nil: absent) as a
// big-endian TIFF, pointing IFD0's 34665 and 34853 entries at them.
func layout(ifd0, exif, gps []field) []byte {
	size := func(fs []field) uint32 {
		if fs == nil {
			return 0
		}
		n := uint32(2 + 12*len(fs) + 4)
		for _, f := range fs {
			if len(f.data) > 4 {
				n += uint32(len(f.data) + len(f.data)%2)
			}
		}

		return n
	}
	offExif := 8 + size(ifd0)
	offGPS := offExif + size(exif)
	for i := range ifd0 {
		switch ifd0[i].tag {
		case 34665:
			ifd0[i].data = be.AppendUint32(nil, offExif)
		case 34853:
			ifd0[i].data = be.AppendUint32(nil, offGPS)
		}
	}
	b := []byte("MM\x00\x2a\x00\x00\x00\x08")
	for _, fs := range [][]field{ifd0, exif, gps} {
		if fs != nil {
			b = writeIFD(b, fs)
		}
	}

	return b
}

// writeIFD appends one IFD at len(b): entries, a zero next-IFD offset, then
// the values too long to fit in an entry, each padded to an even length.
func writeIFD(b []byte, fs []field) []byte {
	extra := uint32(len(b) + 2 + 12*len(fs) + 4)
	b = be.AppendUint16(b, uint16(len(fs)))
	var tail []byte
	for _, f := range fs {
		b = be.AppendUint16(b, f.tag)
		b = be.AppendUint16(b, f.typ)
		b = be.AppendUint32(b, f.count)
		if len(f.data) <= 4 {
			b = append(b, f.data...)
			b = append(b, make([]byte, 4-len(f.data))...)

			continue
		}
		b = be.AppendUint32(b, extra+uint32(len(tail)))
		tail = append(tail, f.data...)
		if len(f.data)%2 == 1 {
			tail = append(tail, 0)
		}
	}
	b = be.AppendUint32(b, 0)

	return append(b, tail...)
}

// tagUserComment is EXIF UserComment, where the app's editor records its
// adjustments (BitmapWithMetadata, ExifInterface.TAG_USER_COMMENT).
const tagUserComment = 37510

// SetUserComment returns png with its EXIF UserComment set to comment (ASCII
// character code, as ExifInterface writes it), keeping every other tag; a
// PNG without EXIF gets an eXIf chunk with only the comment.
func SetUserComment(png []byte, comment string) ([]byte, error) {
	const head = 8 + 4 + 4 + 13 + 4
	if len(png) < head || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || string(png[12:16]) != "IHDR" {
		return nil, errors.New("origin: not a PNG")
	}
	var old []byte
	rest := png[head:]
	for i := head; i+12 <= len(png); {
		n := int(binary.BigEndian.Uint32(png[i:]))
		end := i + 12 + n
		if end > len(png) {
			break
		}
		if string(png[i+4:i+8]) == exifChunk {
			old = png[i+8 : i+8+n]
			rest = append(append([]byte(nil), png[head:i]...), png[end:]...)

			break
		}
		i = end
	}
	data, err := WithUserComment(old, comment)
	if err != nil {
		return nil, err
	}
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, exifChunk...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))

	return append(append(append([]byte(nil), png[:head]...), chunk...), rest...), nil
}

// WithUserComment returns the EXIF TIFF t (nil for none) with its
// UserComment set to comment, keeping every other tag.
func WithUserComment(t []byte, comment string) ([]byte, error) {
	var ifd0, exif, gps []field
	if t != nil {
		var err error
		if ifd0, exif, gps, err = readTIFF(t); err != nil {
			return nil, err
		}
	}
	uc := field{tag: tagUserComment, typ: 7, count: uint32(8 + len(comment)), data: append([]byte("ASCII\x00\x00\x00"), comment...)}
	exif = slices.DeleteFunc(exif, func(f field) bool { return f.tag == tagUserComment })
	exif = append(exif, uc)
	slices.SortFunc(exif, func(a, b field) int { return int(a.tag) - int(b.tag) })
	if !slices.ContainsFunc(ifd0, func(f field) bool { return f.tag == 34665 }) {
		ifd0 = append(ifd0, field{tag: 34665, typ: tLong, count: 1})
		slices.SortFunc(ifd0, func(a, b field) int { return int(a.tag) - int(b.tag) })
	}

	return layout(ifd0, exif, gps), nil
}

// ExifOf is the EXIF TIFF of a PNG (its eXIf chunk) or a JPEG (its APP1
// Exif segment), nil when it has none.
func ExifOf(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		for i := 8; i+12 <= len(b); {
			n := int(binary.BigEndian.Uint32(b[i:]))
			end := i + 12 + n
			if n < 0 || end > len(b) {
				return nil
			}
			if string(b[i+4:i+8]) == exifChunk {
				return b[i+8 : i+8+n]
			}
			i = end
		}
	case bytes.HasPrefix(b, []byte{0xff, 0xd8}):
		if start, end := jpegExif(b); start >= 0 {
			return b[start+10 : end]
		}
	}

	return nil
}

// jpegExif is where a JPEG's APP1 Exif segment is (marker to end), or -1.
func jpegExif(b []byte) (int, int) {
	for i := 2; i+4 <= len(b) && b[i] == 0xff; {
		marker, n := b[i+1], int(binary.BigEndian.Uint16(b[i+2:]))
		if marker == 0xda || n < 2 || i+2+n > len(b) { // the image data: no more headers
			break
		}
		if marker == 0xe1 && bytes.HasPrefix(b[i+4:i+2+n], []byte("Exif\x00\x00")) {
			return i, i + 2 + n
		}
		i += 2 + n
	}

	return -1, -1
}

// JPEGWithExif returns jpg with the EXIF TIFF t in an APP1 segment right
// after its start marker, replacing any it had.
func JPEGWithExif(jpg, t []byte) ([]byte, error) {
	if !bytes.HasPrefix(jpg, []byte{0xff, 0xd8}) {
		return nil, errors.New("origin: not a JPEG")
	}
	if len(t)+8 > 0xffff {
		return nil, errors.New("origin: EXIF too large for a JPEG segment")
	}
	rest := jpg[2:]
	if start, end := jpegExif(jpg); start >= 0 {
		rest = append(append([]byte(nil), jpg[2:start]...), jpg[end:]...)
	}
	seg := []byte{0xff, 0xe1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(2+6+len(t)))
	seg = append(append(seg, "Exif\x00\x00"...), t...)

	return append(append([]byte{0xff, 0xd8}, seg...), rest...), nil
}

// readTIFF reads IFD0 and the Exif and GPS IFDs of an EXIF TIFF, in either
// byte order, as big-endian fields (multi-byte values swapped as needed).
func readTIFF(t []byte) (ifd0, exif, gps []field, err error) {
	if len(t) < 8 {
		return nil, nil, nil, errors.New("origin: short EXIF")
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "MM":
		bo = binary.BigEndian
	case "II":
		bo = binary.LittleEndian
	default:
		return nil, nil, nil, errors.New("origin: not EXIF")
	}
	unit := map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 4, 7: 1, 9: 4, 10: 4}
	read := func(off uint32) ([]field, error) {
		if int(off)+2 > len(t) {
			return nil, errors.New("origin: bad IFD offset")
		}
		var fs []field
		n := int(bo.Uint16(t[off:]))
		for k := range n {
			e := int(off) + 2 + 12*k
			if e+12 > len(t) {
				return nil, errors.New("origin: truncated IFD")
			}
			tag, typ, count := bo.Uint16(t[e:]), bo.Uint16(t[e+2:]), bo.Uint32(t[e+4:])
			u, ok := unit[typ]
			if !ok {
				continue // a type this writer does not lay out: dropped
			}
			if typ == 5 || typ == 10 {
				count *= 2 // rationals are two 4-byte numbers
			}
			l := u * int(count)
			v := t[e+8 : e+12]
			if l > 4 {
				p := int(bo.Uint32(t[e+8:]))
				if p+l > len(t) {
					return nil, errors.New("origin: bad EXIF value offset")
				}
				v = t[p : p+l]
			}
			d := make([]byte, 0, l)
			for i := 0; i < l; i += u { // to big-endian
				switch u {
				case 2:
					d = be.AppendUint16(d, bo.Uint16(v[i:]))
				case 4:
					d = be.AppendUint32(d, bo.Uint32(v[i:]))
				default:
					d = append(d, v[i])
				}
			}
			if typ == 5 || typ == 10 {
				count /= 2
			}
			if tag == 34665 || tag == 34853 { // pointers are laid out again
				d = nil
			}
			fs = append(fs, field{tag: tag, typ: typ, count: count, data: d})
		}

		return fs, nil
	}
	if ifd0, err = read(bo.Uint32(t[4:])); err != nil {
		return nil, nil, nil, err
	}
	for _, f := range slices.Clone(ifd0) {
		if f.tag != 34665 && f.tag != 34853 {
			continue
		}
		// The pointer's value was dropped above; read it again.
		for k := range int(bo.Uint16(t[bo.Uint32(t[4:]):])) {
			e := int(bo.Uint32(t[4:])) + 2 + 12*k
			if bo.Uint16(t[e:]) != f.tag {
				continue
			}
			sub, err := read(bo.Uint32(t[e+8:]))
			if err != nil {
				return nil, nil, nil, err
			}
			if f.tag == 34665 {
				exif = sub
			} else {
				gps = sub
			}
		}
	}

	return ifd0, exif, gps, nil
}
