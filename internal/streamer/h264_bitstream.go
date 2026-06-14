package streamer

import "math/bits"

type bitReader struct {

	buf []byte

	byteOffset int
	bitOffset int

}

func newBitReader(buf []byte) *bitReader {

	return &bitReader{buf: buf}

}

func (r *bitReader) readBits(count int) uint32 {

	if count == 0 {

		return 0
	}

	var result uint32

	for count > 0 {

		if r.byteOffset >= len(r.buf) {

			panic("bitReader: bad byte offset")
		}

		if r.bitOffset == 0 && r.byteOffset >= 2 &&
			r.buf[r.byteOffset-2] == 0 && r.buf[r.byteOffset-1] == 0 && r.buf[r.byteOffset] == 3 {

			r.byteOffset++ // Skip the emulation-prevention byte.
		}

		if r.bitOffset == 0 && count >= 8 {

			result = (result << 8) | uint32(r.buf[r.byteOffset])
			r.byteOffset++
			count -= 8

		} else {

			numBits := count

			if room := 8 - r.bitOffset; room < numBits {

				numBits = room
			}

			mask := (1 << numBits) - 1
			newBits := (int(r.buf[r.byteOffset]) >> (8 - r.bitOffset - numBits)) & mask
			result = (result << numBits) | uint32(newBits)
			count -= numBits
			r.bitOffset += numBits

			if r.bitOffset == 8 {

				r.bitOffset = 0
				r.byteOffset++
			}

		}

	}

	return result

}

func (r *bitReader) readUE() int {

	leading := 0

	for r.readBits(1) == 0 {

		leading++
	}

	return (1 << leading) + int(r.readBits(leading)) - 1

}

func (r *bitReader) readSE() int {

	unsigned := r.readUE()

	if unsigned%2 == 0 {

		return -unsigned / 2
	}

	return (unsigned + 1) / 2

}

type bitWriter struct {

	arr []byte
	pendingByte byte
	bitOffset int

}

func (w *bitWriter) toBuffer() []byte {

	return w.arr

}

func (w *bitWriter) flush() {

	if w.pendingByte <= 3 && len(w.arr) >= 2 && w.arr[len(w.arr)-1] == 0 && w.arr[len(w.arr)-2] == 0 {

		w.arr = append(w.arr, 3)
	}

	w.arr = append(w.arr, w.pendingByte)
	w.pendingByte = 0
	w.bitOffset = 0

}

func (w *bitWriter) writeBits(value uint32, count int) {

	for count > 0 {

		if w.bitOffset == 0 {

			if count >= 8 {

				w.pendingByte = byte((value >> (count - 8)) & 0xff)
				count -= 8
				w.flush()

			} else {

				mask := uint32((1 << count) - 1)
				w.pendingByte |= byte((value & mask) << (8 - count))
				w.bitOffset = count
				count = 0

			}

		} else {

			numBits := 8 - w.bitOffset

			if count < numBits {

				numBits = count
			}

			toWrite := (value >> (count - numBits)) & ((1 << numBits) - 1)
			w.pendingByte |= byte(toWrite << (8 - w.bitOffset - numBits))
			count -= numBits
			w.bitOffset += numBits

			if w.bitOffset == 8 {

				w.bitOffset = 0
				w.flush()
			}

		}

	}

}

func (w *bitWriter) writeUE(value int) {

	value++
	bitCount := bits.Len32(uint32(value))
	w.writeBits(0, bitCount-1)
	w.writeBits(uint32(value), bitCount)

}

func (w *bitWriter) writeSE(value int) {

	if value <= 0 {

		w.writeUE(-2 * value)
	} else {

		w.writeUE(2*value - 1)
	}

}
