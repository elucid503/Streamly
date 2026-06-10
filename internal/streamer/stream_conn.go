package streamer

// StreamConnection is the Go Live voice-gateway session layered on a VoiceConnection.
type StreamConnection struct {
	voice       *VoiceConnection
	rtcServerID string
	streamKey   string
	gateway     *mediaGateway
}

func newStreamConnection(voice *VoiceConnection, ready chan *MediaPeer) *StreamConnection {

	return &StreamConnection{voice: voice, gateway: newMediaGateway("stream", "", voice.botID, 0, ready, false)}

}

func (s *StreamConnection) setSession(sessionID string) {

	if s.rtcServerID == "" {
		return
	}

	s.gateway.serverID = s.rtcServerID
	s.gateway.setDaveChannelID(parseStreamDaveChannelID(s.rtcServerID))
	s.gateway.setSession(sessionID)

}

func (s *StreamConnection) setTokens(server, token string) {

	s.gateway.setTokens(server, token)

}

func (s *StreamConnection) peer() *MediaPeer {

	return s.gateway.peer

}

func (s *StreamConnection) setSpeaking(speaking bool) {

	s.gateway.setSpeaking(speaking, true)

}

func (s *StreamConnection) setVideoAttributes(enabled bool, width, height, fps int) {

	s.gateway.setVideoAttributes(enabled, width, height, fps)

}

func (s *StreamConnection) stop() {

	s.gateway.stop()

}
