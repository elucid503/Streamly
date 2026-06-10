package streamer

// VoiceConnection manages the voice-gateway session for joining a channel.
type VoiceConnection struct {
	streamer  *Streamer
	guildID   *string
	channelID string
	botID     string
	gateway   *mediaGateway

	streamConnection *StreamConnection
}

func newVoiceConnection(streamer *Streamer, guildID *string, channelID, botID string, ready chan *MediaPeer) *VoiceConnection {

	conn := &VoiceConnection{
		streamer:  streamer,
		guildID:   guildID,
		channelID: channelID,
		botID:     botID,
	}

	conn.gateway = newMediaGateway("voice", conn.serverID(), botID, parseDaveChannelID(channelID), ready, true)

	return conn

}

func (v *VoiceConnection) guildIDString() string {

	if v.guildID == nil {
		return ""
	}

	return *v.guildID

}

func (v *VoiceConnection) serverID() string {

	if v.guildID != nil {
		return *v.guildID
	}

	return v.channelID

}

func (v *VoiceConnection) streamType() string {

	if v.guildID == nil {
		return "call"
	}

	return "guild"

}

func (v *VoiceConnection) sessionID() string {

	return v.gateway.sessionID

}

func (v *VoiceConnection) setSession(sessionID string) {

	v.gateway.setSession(sessionID)

}

func (v *VoiceConnection) setTokens(server, token string) {

	v.gateway.setTokens(server, token)

}

func (v *VoiceConnection) stop() {

	if v.streamConnection != nil {
		v.streamConnection.stop()
		v.streamConnection = nil
	}

	v.gateway.stop()

}
