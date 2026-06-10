package streamer

// CodecPayload mirrors discord-video-stream's negotiated payload types.
type CodecPayload struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	ClockRate      int    `json:"clockRate"`
	Priority       int    `json:"priority,omitempty"`
	PayloadType    int    `json:"payload_type"`
	RtxPayloadType int    `json:"rtx_payload_type,omitempty"`
	Encode         bool   `json:"encode,omitempty"`
	Decode         bool   `json:"decode,omitempty"`
}

var (
	CodecOpus = CodecPayload{Name: "opus", Type: "audio", ClockRate: 48000, Priority: 1000, PayloadType: 120}
	CodecH264 = CodecPayload{Name: "H264", Type: "video", ClockRate: 90000, Priority: 1000, PayloadType: 101, RtxPayloadType: 102, Encode: true, Decode: true}
	CodecH265 = CodecPayload{Name: "H265", Type: "video", ClockRate: 90000, Priority: 1000, PayloadType: 103, RtxPayloadType: 104, Encode: true, Decode: true}
	CodecVP8  = CodecPayload{Name: "VP8", Type: "video", ClockRate: 90000, Priority: 1000, PayloadType: 105, RtxPayloadType: 106, Encode: true, Decode: true}
	CodecVP9  = CodecPayload{Name: "VP9", Type: "video", ClockRate: 90000, Priority: 1000, PayloadType: 107, RtxPayloadType: 108, Encode: true, Decode: true}
	CodecAV1  = CodecPayload{Name: "AV1", Type: "video", ClockRate: 90000, Priority: 1000, PayloadType: 109, RtxPayloadType: 110, Encode: true, Decode: true}
)

// SelectProtocolCodecs is the full codec list Discord expects on SELECT_PROTOCOL.
var SelectProtocolCodecs = []CodecPayload{CodecOpus, CodecH264, CodecH265, CodecVP8, CodecVP9, CodecAV1}

// StreamsSimulcast is the single-quality screen share stream Discord expects on IDENTIFY.
var StreamsSimulcast = []map[string]any{{"type": "screen", "rid": "100", "quality": 100}}
