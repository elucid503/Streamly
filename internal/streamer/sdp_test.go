package streamer

import (
	"strings"
	"testing"
)

func TestPrepareRemoteSDPRewritesBrokenAnswer(t *testing.T) {

	raw := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\na=ice-ufrag:abc\r\na=ice-pwd:def\r\na=fingerprint:sha-256 AA\r\na=candidate:1 1 udp 1 1.1.1.1 9 typ host\r\nc=IN IP4 1.1.1.1\r\na=rtcp:9\r\n"

	prepared := prepareRemoteSDP(raw)

	if !strings.HasPrefix(prepared, "v=0") {
		t.Fatalf("expected rewritten sdp to start with v=0, got %q", prepared)
	}

	if err := validateSDP(prepared); err != nil {
		t.Fatalf("rewritten sdp should parse: %v", err)
	}

}

func TestExtractAckSDPPrefersSDPField(t *testing.T) {

	raw := []byte(`{"sdp":"v=0\na=ice-ufrag:abc\n","data":"ignored","dave_protocol_version":1}`)

	if got := extractAckSDP(raw); !strings.Contains(got, "ice-ufrag:abc") {
		t.Fatalf("expected sdp field, got %q", got)
	}

}
