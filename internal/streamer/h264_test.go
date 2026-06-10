package streamer

import (
	"bytes"
	"testing"
)

func TestRewriteH264SPSIntoReusesScratch(t *testing.T) {

	frame := append(bytes.Clone(startCode3), []byte{
		0x67, 0x42, 0xc0, 0x1e, 0xda, 0x01, 0x40, 0x16, 0xe8, 0x06, 0xd0, 0xa1, 0x35, 0x80,
	}...)
	frame = append(frame, startCode3...)
	frame = append(frame, 0x68, 0xce, 0x06, 0xe2)

	var scratch []byte

	first := rewriteH264SPSInto(&scratch, frame)
	second := rewriteH264SPSInto(&scratch, frame)

	if len(first) == 0 || len(second) == 0 {
		t.Fatal("expected non-empty rewrite output")
	}

	if &scratch[0] != &first[0] && &scratch[0] != &second[0] {
		t.Fatal("expected scratch backing store to be reused")
	}

	if !bytes.Equal(first, second) {
		t.Fatal("expected deterministic rewrite output")
	}

}

func TestRewriteH264SPSPassthrough(t *testing.T) {

	frame := []byte{0, 0, 1, 0x65, 0x88, 0x84, 0x00, 0x10}

	out := rewriteH264SPSInto(nil, frame)

	if !bytes.Equal(out, frame) {
		t.Fatal("expected passthrough when no SPS NAL is present")
	}

}