package beada

import "image"

// BeadaPanels take BGR16 -- five bits of blue, six of green, five of red,
// packed little-endian into two bytes:
//
//	bit  15 14 13 12 11 | 10 9 8 7 6 5 | 4 3 2 1 0
//	      R  R  R  R  R |  G G G G G G |  B B B B B
//
// Green gets the extra bit because the eye resolves it best. Converting a
// full-resolution frame runs on every displayed frame, so this is one of the
// few places in the project written for speed over elegance.

// EncodeBGR16 converts an RGBA frame into the panel's pixel format.
//
// dst must hold two bytes per pixel. The image's own stride is honoured, so a
// sub-image of a larger surface encodes correctly.
func EncodeBGR16(dst []byte, src *image.RGBA) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	if len(dst) < width*height*2 {
		return
	}

	for y := 0; y < height; y++ {
		srcRow := src.Pix[y*src.Stride:]
		dstRow := dst[y*width*2:]

		for x := 0; x < width; x++ {
			i := x * 4

			// image.RGBA is premultiplied, and a panel has no alpha to
			// composite against, so the premultiplied values are exactly what
			// should be shown: a half-transparent pixel over nothing is dark.
			r := uint16(srcRow[i]) >> 3
			g := uint16(srcRow[i+1]) >> 2
			b := uint16(srcRow[i+2]) >> 3

			packed := r<<11 | g<<5 | b

			o := x * 2
			dstRow[o] = byte(packed)
			dstRow[o+1] = byte(packed >> 8)
		}
	}
}

// EncodeRGB16 converts an RGBA frame with the red and blue channels swapped,
// for panels configured in the buffered video mode rather than write-through.
func EncodeRGB16(dst []byte, src *image.RGBA) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	if len(dst) < width*height*2 {
		return
	}

	for y := 0; y < height; y++ {
		srcRow := src.Pix[y*src.Stride:]
		dstRow := dst[y*width*2:]

		for x := 0; x < width; x++ {
			i := x * 4

			b := uint16(srcRow[i+2]) >> 3
			g := uint16(srcRow[i+1]) >> 2
			r := uint16(srcRow[i]) >> 3

			packed := b<<11 | g<<5 | r

			o := x * 2
			dstRow[o] = byte(packed)
			dstRow[o+1] = byte(packed >> 8)
		}
	}
}
