package streamer

import (
	"bytes"
	"testing"
)

// TestBitstreamRoundTrip validates the Exp-Golomb and fixed-bit codec, including the
// emulation-prevention 00 00 03 handling that the SPS rewriter relies on.
func TestBitstreamRoundTrip(t *testing.T) {

	w := &bitWriter{}

	w.writeBits(0x67, 8) // NAL header
	w.writeUE(0)         // small ue
	w.writeUE(5)         // multi-bit ue
	w.writeUE(100)       // larger ue
	w.writeSE(-3)        // negative se
	w.writeSE(7)         // positive se
	w.writeSE(0)         // zero se
	w.writeBits(0, 8)    // force an emulation sequence: 00 00 ...
	w.writeBits(0, 8)
	w.writeBits(2, 8) // ... 02 (<=3) triggers a 00 00 03 insertion
	w.writeBits(1, 1)
	w.flush()

	r := newBitReader(w.toBuffer())

	checks := []struct {
		name string
		got  int
		want int
	}{
		{"nal", int(r.readBits(8)), 0x67},
		{"ue0", r.readUE(), 0},
		{"ue5", r.readUE(), 5},
		{"ue100", r.readUE(), 100},
		{"se-3", r.readSE(), -3},
		{"se7", r.readSE(), 7},
		{"se0", r.readSE(), 0},
		{"byteA", int(r.readBits(8)), 0},
		{"byteB", int(r.readBits(8)), 0},
		{"byteC", int(r.readBits(8)), 2},
		{"bit", int(r.readBits(1)), 1},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Fatalf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}

}

// TestSplitNALUs checks Annex-B splitting across 3- and 4-byte start codes.
func TestSplitNALUs(t *testing.T) {

	frame := []byte{0, 0, 0, 1, 0x67, 0xAA, 0, 0, 1, 0x68, 0xBB, 0xCC}
	nalus := splitNALUs(frame)

	if len(nalus) != 2 {
		t.Fatalf("expected 2 NALUs, got %d", len(nalus))
	}

	if !bytes.Equal(nalus[0], []byte{0x67, 0xAA}) {
		t.Fatalf("nalu0 = %v", nalus[0])
	}

	if !bytes.Equal(nalus[1], []byte{0x68, 0xBB, 0xCC}) {
		t.Fatalf("nalu1 = %v", nalus[1])
	}

}

// TestRewriteSPSVUIInjectsRestriction rewrites a minimal baseline SPS and confirms it stays parseable
// with bitstream_restriction signalled (max_num_reorder_frames=0), which is what Discord requires.
func TestRewriteSPSVUIInjectsRestriction(t *testing.T) {

	// Build a minimal baseline SPS (no VUI) so the rewriter must append the restriction block.
	b := &bitWriter{}
	b.writeBits(0x67, 8) // NAL header: SPS
	b.writeBits(66, 8)   // profile_idc = baseline
	b.writeBits(0, 8)    // constraint flags
	b.writeBits(31, 8)   // level_idc
	b.writeUE(0)         // seq_parameter_set_id
	b.writeUE(0)         // log2_max_frame_num_minus4
	b.writeUE(0)         // pic_order_cnt_type
	b.writeUE(4)         // log2_max_pic_order_cnt_lsb_minus4
	b.writeUE(1)         // max_num_ref_frames
	b.writeBits(0, 1)    // gaps_in_frame_num_value_allowed_flag
	b.writeUE(79)        // pic_width_in_mbs_minus1 (1280)
	b.writeUE(44)        // pic_height_in_map_units_minus1 (720)
	b.writeBits(1, 1)    // frame_mbs_only_flag
	b.writeBits(1, 1)    // direct_8x8_inference_flag
	b.writeBits(0, 1)    // frame_cropping_flag
	b.writeBits(0, 1)    // vui_parameters_present_flag
	b.writeBits(1, 1)    // rbsp_stop_one_bit
	b.flush()

	sps := b.toBuffer()
	out := rewriteSPSVUI(sps)

	if out == nil {
		t.Fatal("rewriteSPSVUI returned nil for a valid SPS")
	}

	if out[0] != 0x67 {
		t.Fatalf("NAL header changed: %#x", out[0])
	}

	// Re-parse the rewritten SPS up to the VUI and confirm the restriction block is present with
	// max_num_reorder_frames = 0.
	r := newBitReader(out[1:])
	r.readBits(8)        // profile
	r.readBits(8)        // constraints
	r.readBits(8)        // level
	r.readUE()           // sps id
	r.readUE()           // log2_max_frame_num
	if r.readUE() != 0 { // pic_order_cnt_type
		t.Fatal("pic_order_cnt_type mismatch")
	}
	r.readUE()    // log2_max_pic_order_cnt_lsb
	r.readUE()    // max_num_ref_frames
	r.readBits(1) // gaps
	r.readUE()    // width
	r.readUE()    // height
	r.readBits(1) // frame_mbs_only
	r.readBits(1) // direct_8x8
	r.readBits(1) // frame_cropping

	if r.readBits(1) != 1 {
		t.Fatal("vui_parameters_present_flag should be 1 after rewrite")
	}

	r.readBits(2) // aspect/overscan present
	r.readBits(1) // video_signal_type present
	r.readBits(5) // chroma_loc/timing/nal_hrd/vcl_hrd/pic_struct present

	if r.readBits(1) != 1 {
		t.Fatal("bitstream_restriction_flag should be 1")
	}

	r.readBits(1) // motion_vectors_over_pic_boundaries
	r.readUE()    // max_bytes_per_pic_denom
	r.readUE()    // max_bits_per_mb_denom
	r.readUE()    // log2_max_mv_length_horizontal
	r.readUE()    // log2_max_mv_length_vertical

	if got := r.readUE(); got != 0 {
		t.Fatalf("max_num_reorder_frames = %d, want 0", got)
	}

}
