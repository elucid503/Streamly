package streamer

import "testing"

func TestH264ContainsIDR(t *testing.T) {

	idr := []byte{0, 0, 1, 0x67, 0xAA, 0, 0, 1, 0x68, 0xBB, 0, 0, 0, 1, 0x65, 0xCC}

	if !h264ContainsIDR(idr) {
		t.Fatal("expected IDR detection in SPS/PPS/IDR frame")
	}

	pFrame := []byte{0, 0, 0, 1, 0x41, 0xCC, 0xDD}

	if h264ContainsIDR(pFrame) {
		t.Fatal("P-frame must not register as IDR")
	}

	if h264ContainsIDR(nil) || h264ContainsIDR([]byte{0, 0}) {
		t.Fatal("degenerate buffers must not register as IDR")
	}

}
