package streamer

import "bytes"

var startCode3 = []byte{0, 0, 1}

const (
	h264NalTypeSPS = 7
	h264NalTypeIDR = 5
)

func h264ContainsIDR(frame []byte) bool {

	for _, nalu := range splitNALUs(frame) {

		if len(nalu) > 0 && nalu[0]&0x1f == h264NalTypeIDR {

			return true
		}

	}

	return false

}

func rewriteH264SPS(frame []byte) []byte {

	return rewriteH264SPSInto(nil, frame)
}

func rewriteH264SPSInto(scratch *[]byte, frame []byte) []byte {

	nalus := splitNALUs(frame)
	rewritten := false

	for i, nalu := range nalus {

		if len(nalu) > 0 && nalu[0]&0x1f == h264NalTypeSPS {

			if out := rewriteSPSVUI(nalu); out != nil {

				nalus[i] = out
				rewritten = true
			}

		}

	}

	if !rewritten {

		return frame
	}

	need := len(frame)

	for _, nalu := range nalus {

		need += len(startCode3) + len(nalu)
	}

	buf := growByteScratch(scratch, need)[:0]

	for _, nalu := range nalus {

		buf = append(buf, startCode3...)
		buf = append(buf, nalu...)
	}

	if scratch != nil {

		*scratch = buf
	}

	return buf

}

func growByteScratch(scratch *[]byte, need int) []byte {

	if scratch == nil {

		buf := make([]byte, 0, need)
		return buf
	}

	if cap(*scratch) >= need {

		return *scratch
	}

	*scratch = make([]byte, 0, need)

	return *scratch

}

func splitNALUs(buf []byte) [][]byte {

	var nalus [][]byte
	temp := buf

	for len(temp) > 0 {

		pos := bytes.Index(temp, startCode3)
		length := 3

		if pos > 0 && temp[pos-1] == 0 {

			pos--
			length++
		}

		var nalu []byte

		if pos == -1 {

			nalu = temp
			temp = nil
		} else {

			nalu = temp[:pos]
			temp = temp[pos+length:]
		}

		if len(nalu) > 0 {

			nalus = append(nalus, nalu)
		}

	}

	return nalus

}

func rewriteSPSVUI(sps []byte) (out []byte) {

	defer func() {

		if recover() != nil {

			out = nil
		}

	}()

	reader := newBitReader(sps[1:])
	writer := &bitWriter{}

	writer.writeBits(uint32(sps[0]), 8)

	profileIDC := reader.readBits(8)
	writer.writeBits(profileIDC, 8)

	writer.writeBits(reader.readBits(8), 8)
	writer.writeBits(reader.readBits(8), 8)

	writer.writeUE(reader.readUE())

	switch profileIDC {

	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 144:

		chromaFormatIDC := reader.readUE()
		writer.writeUE(chromaFormatIDC)

		if chromaFormatIDC == 3 {

			writer.writeBits(reader.readBits(1), 1)
		}

		writer.writeUE(reader.readUE())
		writer.writeUE(reader.readUE())
		writer.writeBits(reader.readBits(1), 1)

		seqScalingMatrixPresent := reader.readBits(1)
		writer.writeBits(seqScalingMatrixPresent, 1)

		if seqScalingMatrixPresent != 0 {

			count := 8

			if chromaFormatIDC == 3 {

				count = 12
			}

			for i := 0; i < count; i++ {

				present := reader.readBits(1)
				writer.writeBits(present, 1)

				if present != 0 {

					size := 64

					if i < 6 {

						size = 16
					}

					lastScale := 8

					for j := 0; j < size; j++ {

						delta := reader.readSE()
						writer.writeSE(delta)
						nextScale := (lastScale + delta + 256) % 256

						if nextScale != 0 {

							lastScale = nextScale
						}

					}

				}

			}

		}

	}

	writer.writeUE(reader.readUE())

	picOrderCntType := reader.readUE()
	writer.writeUE(picOrderCntType)

	if picOrderCntType == 0 {

		writer.writeUE(reader.readUE())

	} else if picOrderCntType == 1 {

		writer.writeBits(reader.readBits(1), 1)
		writer.writeSE(reader.readSE())
		writer.writeSE(reader.readSE())

		num := reader.readUE()
		writer.writeUE(num)

		for i := 0; i < num; i++ {

			writer.writeSE(reader.readSE())
		}

	}

	maxNumRefFrames := reader.readUE()
	writer.writeUE(maxNumRefFrames)

	writer.writeBits(reader.readBits(1), 1)
	writer.writeUE(reader.readUE())
	writer.writeUE(reader.readUE())

	frameMbsOnly := reader.readBits(1)
	writer.writeBits(frameMbsOnly, 1)

	if frameMbsOnly == 0 {

		writer.writeBits(reader.readBits(1), 1)
	}

	writer.writeBits(reader.readBits(1), 1)

	frameCropping := reader.readBits(1)
	writer.writeBits(frameCropping, 1)

	if frameCropping != 0 {

		writer.writeUE(reader.readUE())
		writer.writeUE(reader.readUE())
		writer.writeUE(reader.readUE())
		writer.writeUE(reader.readUE())
	}

	// Forces max_num_reorder_frames to 0 so Discord's decoder accepts the stream.
	addBitstreamRestriction := func() {

		writer.writeBits(1, 1)
		writer.writeUE(2)
		writer.writeUE(1)
		writer.writeUE(16)
		writer.writeUE(16)
		writer.writeUE(0)
		writer.writeUE(maxNumRefFrames)
	}

	vuiPresent := reader.readBits(1)
	writer.writeBits(1, 1)

	if vuiPresent == 0 {

		writer.writeBits(0, 2)
		writer.writeBits(0, 1)
		writer.writeBits(0, 5)
		writer.writeBits(1, 1)
		addBitstreamRestriction()

	} else {

		aspectRatioPresent := reader.readBits(1)
		writer.writeBits(aspectRatioPresent, 1)

		if aspectRatioPresent != 0 {

			aspectRatioIDC := reader.readBits(8)
			writer.writeBits(aspectRatioIDC, 8)

			if aspectRatioIDC == 255 {

				writer.writeBits(reader.readBits(16), 16)
				writer.writeBits(reader.readBits(16), 16)
			}

		}

		overscanPresent := reader.readBits(1)
		writer.writeBits(overscanPresent, 1)

		if overscanPresent != 0 {

			writer.writeBits(reader.readBits(1), 1)
		}

		// Read but drop the video signal type.
		videoSignalTypePresent := reader.readBits(1)
		writer.writeBits(0, 1)

		if videoSignalTypePresent != 0 {

			reader.readBits(3)
			reader.readBits(1)
			colourDescriptionPresent := reader.readBits(1)

			if colourDescriptionPresent != 0 {

				reader.readBits(8)
				reader.readBits(8)
				reader.readBits(8)
			}

		}

		chromaLocPresent := reader.readBits(1)
		writer.writeBits(chromaLocPresent, 1)

		if chromaLocPresent != 0 {

			writer.writeUE(reader.readUE())
			writer.writeUE(reader.readUE())
		}

		timingInfoPresent := reader.readBits(1)
		writer.writeBits(timingInfoPresent, 1)

		if timingInfoPresent != 0 {

			writer.writeBits(reader.readBits(32), 32)
			writer.writeBits(reader.readBits(32), 32)
			writer.writeBits(reader.readBits(1), 1)
		}

		nalHRDPresent := reader.readBits(1)
		writer.writeBits(nalHRDPresent, 1)

		if nalHRDPresent != 0 {

			copyHRDParameters(reader, writer)
		}

		vclHRDPresent := reader.readBits(1)
		writer.writeBits(vclHRDPresent, 1)

		if vclHRDPresent != 0 {

			copyHRDParameters(reader, writer)
		}

		if nalHRDPresent != 0 || vclHRDPresent != 0 {

			writer.writeBits(reader.readBits(1), 1)
		}

		writer.writeBits(reader.readBits(1), 1)

		bitstreamRestriction := reader.readBits(1)
		writer.writeBits(1, 1)

		if bitstreamRestriction == 0 {

			addBitstreamRestriction()

		} else {

			writer.writeBits(reader.readBits(1), 1)
			writer.writeUE(reader.readUE())
			writer.writeUE(reader.readUE())
			writer.writeUE(reader.readUE())
			writer.writeUE(reader.readUE())
			reader.readUE()
			writer.writeUE(0)
			reader.readUE()
			writer.writeUE(maxNumRefFrames)

		}

	}

	writer.writeBits(1, 1)
	writer.flush()

	return writer.toBuffer()

}

func copyHRDParameters(reader *bitReader, writer *bitWriter) {

	cpbCntMinus1 := reader.readUE()
	writer.writeUE(cpbCntMinus1)
	writer.writeBits(reader.readBits(4), 4)
	writer.writeBits(reader.readBits(4), 4)

	for i := 0; i <= cpbCntMinus1; i++ {

		writer.writeUE(reader.readUE())
		writer.writeUE(reader.readUE())
		writer.writeBits(reader.readBits(1), 1)
	}

	writer.writeBits(reader.readBits(5), 5)
	writer.writeBits(reader.readBits(5), 5)
	writer.writeBits(reader.readBits(5), 5)
	writer.writeBits(reader.readBits(5), 5)

}
