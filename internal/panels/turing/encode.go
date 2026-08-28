package turing

import "image"

// EncodeRGBA converts a rendered frame into the panel's wire format.
//
// The panel takes blue, green, red, and an alpha byte per pixel. Alpha is
// written as opaque regardless of the source: the panel composites nothing, and
// a frame carrying the overlay's transparency would come out darkened wherever
// the layout left the canvas bare.
//
// dst must be model.FrameSize() bytes and img exactly the panel's size;
// DisplayImage checks both before calling.
func EncodeRGBA(dst []byte, img *image.RGBA, model Model) {
	bounds := img.Bounds()

	for y := 0; y < model.Height; y++ {
		src := img.Pix[(y+bounds.Min.Y-img.Rect.Min.Y)*img.Stride:]
		row := dst[y*model.Width*bytesPerPixel:]

		for x := 0; x < model.Width; x++ {
			s := x * 4
			d := x * bytesPerPixel

			row[d+0] = src[s+2] // B
			row[d+1] = src[s+1] // G
			row[d+2] = src[s+0] // R
			row[d+3] = 0xff
		}
	}
}
