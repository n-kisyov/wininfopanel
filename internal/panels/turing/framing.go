package turing

import "io"

// blockWriter frames a byte stream into the fixed-size blocks the panel reads.
//
// The panel consumes exactly blockSize bytes at a time and ignores the last
// byte of each, so callers append freely and this cuts the stream at
// payloadPerBlock and pads the remainder. Getting the block length wrong does
// not produce a corrupt frame -- it desynchronises the firmware, which counts
// blocks rather than bytes, and every later frame is wrong too.
//
// The pad byte is a parameter rather than a constant because the block that
// opens a frame is padded with the start byte instead of with zeroes.
type blockWriter struct {
	w io.Writer

	block  [blockSize]byte
	offset int
}

func newBlockWriter(w io.Writer) *blockWriter { return &blockWriter{w: w} }

// write appends bytes, emitting a block whenever one fills.
func (b *blockWriter) write(values []byte, pad byte) error {
	for b.offset+len(values) > payloadPerBlock {
		n := payloadPerBlock - b.offset
		copy(b.block[b.offset:], values[:n])
		b.offset += n

		if err := b.emit(pad); err != nil {
			return err
		}
		values = values[n:]
	}

	if len(values) > 0 {
		copy(b.block[b.offset:], values)
		b.offset += len(values)
	}
	return nil
}

// writeByte appends one byte.
func (b *blockWriter) writeByte(value, pad byte) error {
	if b.offset+1 > payloadPerBlock {
		if err := b.emit(pad); err != nil {
			return err
		}
	}
	b.block[b.offset] = value
	b.offset++
	return nil
}

// flush emits a partially filled block. A block that is already empty is not
// emitted: the panel would take the padding as a frame of its own.
func (b *blockWriter) flush(pad byte) error {
	if b.offset == 0 {
		return nil
	}
	return b.emit(pad)
}

// emit pads the staging buffer to full length and writes it.
func (b *blockWriter) emit(pad byte) error {
	for i := b.offset; i < blockSize; i++ {
		b.block[i] = pad
	}
	b.offset = 0

	_, err := b.w.Write(b.block[:])
	return err
}
