package streamer

import (
	"encoding/json"
	"fmt"
	"strings"

	pionsdp "github.com/pion/sdp/v3"
)

// extractAckSDP pulls the answer SDP out of a SELECT_PROTOCOL_ACK payload.
func extractAckSDP(data json.RawMessage) string {

	var ack struct {
		SDP  string `json:"sdp"`
		Data string `json:"data"`
	}

	_ = json.Unmarshal(data, &ack)

	if sdp := strings.TrimSpace(ack.SDP); sdp != "" {
		return sdp
	}

	return strings.TrimSpace(ack.Data)

}

// prepareRemoteSDP returns an SDP answer pion can parse.
func prepareRemoteSDP(sdp string) string {

	sdp = strings.TrimSpace(normalizeSDPLines(sdp))

	if sdp == "" {
		return ""
	}

	return finalizeSDP(rewriteDiscordSDP(sdp))

}

func validateSDP(raw string) error {

	var session pionsdp.SessionDescription

	return session.UnmarshalString(raw)

}

func finalizeSDP(sdp string) string {

	sdp = strings.TrimSpace(sdp)

	if sdp == "" {
		return ""
	}

	if !strings.HasSuffix(sdp, "\r\n") {
		sdp += "\r\n"
	}

	return sdp

}

func normalizeSDPLines(sdp string) string {

	sdp = strings.ReplaceAll(sdp, "\r\n", "\n")
	sdp = strings.ReplaceAll(sdp, "\r", "\n")

	return sdp

}

// rewriteDiscordSDP rebuilds Discord's answer into the shape expected by pion.
func rewriteDiscordSDP(sdp string) string {

	var ip, port, iceUsername, icePassword, fingerprint, candidate string

	for _, line := range strings.Split(normalizeSDPLines(sdp), "\n") {

		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "c="):
			ip = line
		case strings.HasPrefix(line, "m=audio"):
			fields := strings.Fields(line)

			if len(fields) >= 2 && port == "" {
				port = fields[1]
			}
		case strings.HasPrefix(line, "a=rtcp"):
			parts := strings.SplitN(line, ":", 2)

			if len(parts) == 2 && port == "" {
				port = strings.TrimSpace(parts[1])
			}
		case strings.HasPrefix(line, "a=ice-ufrag"):
			iceUsername = line
		case strings.HasPrefix(line, "a=ice-pwd"):
			icePassword = line
		case strings.HasPrefix(line, "a=fingerprint"):
			fingerprint = line
		case strings.HasPrefix(line, "a=candidate"):
			if candidate == "" {
				candidate = line
			}
		}

	}

	if port == "" {
		port = "9"
	}

	if ip == "" {
		ip = "c=IN IP4 127.0.0.1"
	}

	audioPayloadType := CodecOpus.PayloadType

	audioSection := fmt.Sprintf(`m=audio %s UDP/TLS/RTP/SAVPF %d
%s
a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level
a=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01
a=extmap:5 http://www.webrtc.org/experiments/rtp-hdrext/playout-delay
a=setup:passive
a=mid:0
a=maxptime:60
a=inactive
%s
%s
%s
%s
a=rtcp-mux
a=rtpmap:%d opus/48000/2
a=fmtp:%d minptime=10;useinbandfec=1;usedtx=1
a=rtcp-fb:%d transport-cc
a=rtcp-fb:%d nack
a=ice-lite`, port, audioPayloadType, ip, iceUsername, icePassword, fingerprint, candidate, audioPayloadType, audioPayloadType, audioPayloadType, audioPayloadType)

	videoPayloadTypes := fmt.Sprintf("%d %d", CodecH264.PayloadType, CodecH264.RtxPayloadType)

	videoSection := fmt.Sprintf(`m=video %s UDP/TLS/RTP/SAVPF %s
%s
a=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time
a=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01
a=extmap:14 urn:ietf:params:rtp-hdrext:toffset
a=extmap:13 urn:3gpp:video-orientation
a=extmap:5 http://www.webrtc.org/experiments/rtp-hdrext/playout-delay
a=setup:passive
a=mid:1
a=inactive
%s
%s
%s
%s
a=rtcp-mux
a=ice-lite`, port, videoPayloadTypes, ip, iceUsername, icePassword, fingerprint, candidate)

	videoRtpMap := fmt.Sprintf(`a=rtpmap:%d %s/90000
a=fmtp:%d level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f
a=rtpmap:%d rtx/90000
a=fmtp:%d apt=%d
a=rtcp-fb:%d ccm fir
a=rtcp-fb:%d nack
a=rtcp-fb:%d nack pli
a=rtcp-fb:%d goog-remb
a=rtcp-fb:%d transport-cc`,
		CodecH264.PayloadType, CodecH264.Name,
		CodecH264.PayloadType,
		CodecH264.RtxPayloadType,
		CodecH264.RtxPayloadType, CodecH264.PayloadType,
		CodecH264.PayloadType, CodecH264.PayloadType, CodecH264.PayloadType, CodecH264.PayloadType, CodecH264.PayloadType)

	return strings.Join([]string{
		"v=0",
		"o=- 0 0 IN IP4 127.0.0.1",
		"s=-",
		"t=0 0",
		"a=group:BUNDLE 0 1",
		audioSection,
		videoSection,
		videoRtpMap,
	}, "\n")

}