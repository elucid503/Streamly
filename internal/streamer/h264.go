package streamer

import "bytes"

// startCode3 is the 3-byte Annex-B NAL start prefix.
var startCode3 = []byte{0, 0, 1}

const h264NalTypeSPS = 7 // SPS NAL unit type.

// rewriteH264SPS rewrites SPS VUI so Discord's decoder accepts the Annex-B frame.
func rewriteH264SPS(frame []byte) []byte {
	return rewriteH264SPSInto(nil, frame)
}

// rewriteH264SPSInto rewrites SPS VUI, reusing scratch when a new buffer is required.
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

// splitNALUs splits an Annex-B buffer into its NAL units, stripping the 3- and 4-byte start codes.
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

// rewriteSPSVUI rewrites one SPS NAL, returning nil if it cannot be parsed.
func rewriteSPSVUI(sps []byte) (out []byte) {

	defer func() {
		if recover() != nil {
			out = nil
		}
	}()

	reader := newBitReader(sps[1:])
	writer := &bitWriter{}

	// NAL header byte.
	writer.writeBits(uint32(sps[0]), 8)

	profileIDC := reader.readBits(8)
	writer.writeBits(profileIDC, 8)

	writer.writeBits(reader.readBits(8), 8) // constraint flags
	writer.writeBits(reader.readBits(8), 8) // level_idc

	writer.writeUE(reader.readUE()) // seq_parameter_set_id

	switch profileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 144:

		chromaFormatIDC := reader.readUE()
		writer.writeUE(chromaFormatIDC)

		if chromaFormatIDC == 3 {
			writer.writeBits(reader.readBits(1), 1) // separate_colour_plane_flag
		}

		writer.writeUE(reader.readUE())         // bit_depth_luma_minus8
		writer.writeUE(reader.readUE())         // bit_depth_chroma_minus8
		writer.writeBits(reader.readBits(1), 1) // qpprime_y_zero_transform_bypass_flag

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

	writer.writeUE(reader.readUE()) // log2_max_frame_num_minus4

	picOrderCntType := reader.readUE()
	writer.writeUE(picOrderCntType)

	if picOrderCntType == 0 {

		writer.writeUE(reader.readUE()) // log2_max_pic_order_cnt_lsb_minus4

	} else if picOrderCntType == 1 {

		writer.writeBits(reader.readBits(1), 1) // delta_pic_order_always_zero_flag
		writer.writeSE(reader.readSE())         // offset_for_non_ref_pic
		writer.writeSE(reader.readSE())         // offset_for_top_to_bottom_field

		num := reader.readUE()
		writer.writeUE(num)

		for i := 0; i < num; i++ {
			writer.writeSE(reader.readSE()) // offset_for_ref_frame
		}

	}

	maxNumRefFrames := reader.readUE()
	writer.writeUE(maxNumRefFrames)

	writer.writeBits(reader.readBits(1), 1) // gaps_in_frame_num_value_allowed_flag
	writer.writeUE(reader.readUE())         // pic_width_in_mbs_minus1
	writer.writeUE(reader.readUE())         // pic_height_in_map_units_minus1

	frameMbsOnly := reader.readBits(1)
	writer.writeBits(frameMbsOnly, 1)

	if frameMbsOnly == 0 {
		writer.writeBits(reader.readBits(1), 1) // mb_adaptive_frame_field_flag
	}

	writer.writeBits(reader.readBits(1), 1) // direct_8x8_inference_flag

	frameCropping := reader.readBits(1)
	writer.writeBits(frameCropping, 1)

	if frameCropping != 0 {
		writer.writeUE(reader.readUE()) // frame_crop_left_offset
		writer.writeUE(reader.readUE()) // frame_crop_right_offset
		writer.writeUE(reader.readUE()) // frame_crop_top_offset
		writer.writeUE(reader.readUE()) // frame_crop_bottom_offset
	}

	// addBitstreamRestriction writes the restriction defaults, forcing max_num_reorder_frames to 0.
	addBitstreamRestriction := func() {
		writer.writeBits(1, 1)          // motion_vectors_over_pic_boundaries_flag
		writer.writeUE(2)               // max_bytes_per_pic_denom
		writer.writeUE(1)               // max_bits_per_mb_denom
		writer.writeUE(16)              // log2_max_mv_length_horizontal
		writer.writeUE(16)              // log2_max_mv_length_vertical
		writer.writeUE(0)               // max_num_reorder_frames
		writer.writeUE(maxNumRefFrames) // max_dec_frame_buffering
	}

	vuiPresent := reader.readBits(1)
	writer.writeBits(1, 1)

	if vuiPresent == 0 {

		writer.writeBits(0, 2) // aspect_ratio_info_present_flag, overscan_info_present_flag
		writer.writeBits(0, 1) // video_signal_type_present_flag
		writer.writeBits(0, 5) // chroma_loc/timing/nal_hrd/vcl_hrd/pic_struct present flags
		writer.writeBits(1, 1) // bitstream_restriction_flag
		addBitstreamRestriction()

	} else {

		aspectRatioPresent := reader.readBits(1)
		writer.writeBits(aspectRatioPresent, 1)

		if aspectRatioPresent != 0 {

			aspectRatioIDC := reader.readBits(8)
			writer.writeBits(aspectRatioIDC, 8)

			if aspectRatioIDC == 255 {
				writer.writeBits(reader.readBits(16), 16) // sar_width
				writer.writeBits(reader.readBits(16), 16) // sar_height
			}

		}

		overscanPresent := reader.readBits(1)
		writer.writeBits(overscanPresent, 1)

		if overscanPresent != 0 {
			writer.writeBits(reader.readBits(1), 1) // overscan_appropriate_flag
		}

		// Read but drop the video signal type.
		videoSignalTypePresent := reader.readBits(1)
		writer.writeBits(0, 1)

		if videoSignalTypePresent != 0 {

			reader.readBits(3) // video_format
			reader.readBits(1) // video_full_range_flag
			colourDescriptionPresent := reader.readBits(1)

			if colourDescriptionPresent != 0 {
				reader.readBits(8) // colour_primaries
				reader.readBits(8) // transfer_characteristics
				reader.readBits(8) // matrix_coeffs
			}

		}

		chromaLocPresent := reader.readBits(1)
		writer.writeBits(chromaLocPresent, 1)

		if chromaLocPresent != 0 {
			writer.writeUE(reader.readUE()) // chroma_sample_loc_type_top_field
			writer.writeUE(reader.readUE()) // chroma_sample_loc_type_bottom_field
		}

		timingInfoPresent := reader.readBits(1)
		writer.writeBits(timingInfoPresent, 1)

		if timingInfoPresent != 0 {
			writer.writeBits(reader.readBits(32), 32) // num_units_in_tick
			writer.writeBits(reader.readBits(32), 32) // time_scale
			writer.writeBits(reader.readBits(1), 1)   // fixed_frame_rate_flag
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
			writer.writeBits(reader.readBits(1), 1) // low_delay_hrd_flag
		}

		writer.writeBits(reader.readBits(1), 1) // pic_struct_present_flag

		bitstreamRestriction := reader.readBits(1)
		writer.writeBits(1, 1)

		if bitstreamRestriction == 0 {

			addBitstreamRestriction()

		} else {

			writer.writeBits(reader.readBits(1), 1) // motion_vectors_over_pic_boundaries_flag
			writer.writeUE(reader.readUE())         // max_bytes_per_pic_denom
			writer.writeUE(reader.readUE())         // max_bits_per_mb_denom
			writer.writeUE(reader.readUE())         // log2_max_mv_length_horizontal
			writer.writeUE(reader.readUE())         // log2_max_mv_length_vertical
			reader.readUE()                         // num_reorder_frames (dropped)
			writer.writeUE(0)
			reader.readUE() // max_dec_frame_buffering (dropped)
			writer.writeUE(maxNumRefFrames)

		}

	}

	writer.writeBits(1, 1) // rbsp_stop_one_bit
	writer.flush()

	return writer.toBuffer()

}

func copyHRDParameters(reader *bitReader, writer *bitWriter) {

	cpbCntMinus1 := reader.readUE()
	writer.writeUE(cpbCntMinus1)
	writer.writeBits(reader.readBits(4), 4) // bit_rate_scale
	writer.writeBits(reader.readBits(4), 4) // cpb_size_scale

	for i := 0; i <= cpbCntMinus1; i++ {
		writer.writeUE(reader.readUE())         // bit_rate_value_minus1
		writer.writeUE(reader.readUE())         // cpb_size_value_minus1
		writer.writeBits(reader.readBits(1), 1) // cbr_flag
	}

	writer.writeBits(reader.readBits(5), 5) // initial_cpb_removal_delay_length_minus1
	writer.writeBits(reader.readBits(5), 5) // cpb_removal_delay_length_minus1
	writer.writeBits(reader.readBits(5), 5) // dpb_output_delay_length_minus1
	writer.writeBits(reader.readBits(5), 5) // time_offset_length

}
