package streamer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"streamly/internal/selfbot"
)

// Gateway opcodes the streamer sends on the main gateway websocket.
const (
	gwVoiceStateUpdate = 4
	gwStreamCreate     = 18
	gwStreamDelete     = 19
	gwStreamSetPaused  = 22
)

const voiceJoinTimeout = 45 * time.Second

// Streamer wraps a minimal selfbot client and owns the active voice connection.
type Streamer struct {
	client *selfbot.Client

	mu              sync.Mutex
	voiceConnection *VoiceConnection
}

func New(client *selfbot.Client) *Streamer {

	s := &Streamer{client: client}

	go s.listen()

	return s

}

func (s *Streamer) listen() {

	for event := range s.client.Events() {

		switch event.Type {
		case "VOICE_STATE_UPDATE":
			s.onVoiceStateUpdate(event.Data)
		case "VOICE_SERVER_UPDATE":
			s.onVoiceServerUpdate(event.Data)
		case "STREAM_CREATE":
			s.onStreamCreate(event.Data)
		case "STREAM_SERVER_UPDATE":
			s.onStreamServerUpdate(event.Data)
		}

	}

}

func (s *Streamer) onVoiceStateUpdate(data json.RawMessage) {

	var payload struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
	}

	_ = json.Unmarshal(data, &payload)

	if payload.UserID != s.client.UserID() {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.voiceConnection != nil {
		s.voiceConnection.setSession(payload.SessionID)
	}

}

func (s *Streamer) onVoiceServerUpdate(data json.RawMessage) {

	var payload struct {
		Token     string  `json:"token"`
		Endpoint  string  `json:"endpoint"`
		GuildID   *string `json:"guild_id"`
		ChannelID *string `json:"channel_id"`
	}

	_ = json.Unmarshal(data, &payload)

	s.mu.Lock()
	conn := s.voiceConnection
	s.mu.Unlock()

	if conn == nil {
		return
	}

	if payload.GuildID != nil && conn.guildID != nil && *payload.GuildID != *conn.guildID {
		return
	}

	if payload.ChannelID != nil && *payload.ChannelID != conn.channelID {
		return
	}

	conn.setTokens(payload.Endpoint, payload.Token)

}

func (s *Streamer) onStreamCreate(data json.RawMessage) {

	var payload struct {
		StreamKey   string `json:"stream_key"`
		RTCServerID string `json:"rtc_server_id"`
	}

	_ = json.Unmarshal(data, &payload)

	s.mu.Lock()
	conn := s.voiceConnection
	s.mu.Unlock()

	if conn == nil || conn.streamConnection == nil {
		return
	}

	guildID, channelID, userID, _, err := parseStreamKey(payload.StreamKey)

	if err != nil {
		return
	}

	if conn.guildIDString() != guildID || conn.channelID != channelID || s.client.UserID() != userID {
		return
	}

	conn.streamConnection.rtcServerID = payload.RTCServerID
	conn.streamConnection.streamKey = payload.StreamKey
	conn.streamConnection.setSession(conn.sessionID())

}

func (s *Streamer) onStreamServerUpdate(data json.RawMessage) {

	var payload struct {
		StreamKey string `json:"stream_key"`
		Token     string `json:"token"`
		Endpoint  string `json:"endpoint"`
	}

	_ = json.Unmarshal(data, &payload)

	s.mu.Lock()
	conn := s.voiceConnection
	s.mu.Unlock()

	if conn == nil || conn.streamConnection == nil {
		return
	}

	guildID, channelID, userID, _, err := parseStreamKey(payload.StreamKey)

	if err != nil {
		return
	}

	if conn.guildIDString() != guildID || conn.channelID != channelID || s.client.UserID() != userID {
		return
	}

	conn.streamConnection.setTokens(payload.Endpoint, payload.Token)

}

// JoinVoice joins a guild or DM voice channel and waits for the WebRTC session.
func (s *Streamer) JoinVoice(ctx context.Context, guildID, channelID string) (*MediaPeer, error) {

	if s.client.UserID() == "" {
		return nil, fmt.Errorf("client not logged in")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, voiceJoinTimeout)
		defer cancel()
	}

	ready := make(chan *MediaPeer, 1)

	var guildPtr *string

	if guildID != "" {
		guildPtr = &guildID
	}

	conn := newVoiceConnection(s, guildPtr, channelID, s.client.UserID(), ready)

	s.mu.Lock()
	s.voiceConnection = conn
	s.mu.Unlock()

	_ = s.client.Send(gwVoiceStateUpdate, map[string]any{
		"guild_id":   guildPtr,
		"channel_id": channelID,
		"self_mute":  false,
		"self_deaf":  true,
		"self_video": false,
	})

	select {
	case <-ready:
		return nil, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("voice join timed out: %w", ctx.Err())
	}

}

// CreateStream starts a Go Live stream on the active voice connection.
func (s *Streamer) CreateStream(ctx context.Context) (*StreamConnection, error) {

	s.mu.Lock()
	conn := s.voiceConnection
	s.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected to a voice channel")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, voiceJoinTimeout)
		defer cancel()
	}

	ready := make(chan *MediaPeer, 1)
	streamConn := newStreamConnection(conn, ready)
	conn.streamConnection = streamConn

	s.signalStream(conn)

	select {
	case peer := <-ready:
		if peer == nil {
			return nil, fmt.Errorf("stream connection failed")
		}

		return streamConn, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("stream start timed out: %w", ctx.Err())
	}

}

func (s *Streamer) signalStream(conn *VoiceConnection) {

	_ = s.client.Send(gwStreamCreate, map[string]any{
		"type":             conn.streamType(),
		"guild_id":         conn.guildID,
		"channel_id":       conn.channelID,
		"preferred_region": nil,
	})

	streamKey := generateStreamKey(conn.guildID == nil, conn.guildIDString(), conn.channelID, conn.botID)

	_ = s.client.Send(gwStreamSetPaused, map[string]any{
		"stream_key": streamKey,
		"paused":     false,
	})

}

// StopStream tears down the active Go Live session.
func (s *Streamer) StopStream() {

	s.mu.Lock()
	conn := s.voiceConnection
	s.mu.Unlock()

	if conn == nil || conn.streamConnection == nil {
		return
	}

	streamKey := generateStreamKey(conn.guildID == nil, conn.guildIDString(), conn.channelID, conn.botID)

	_ = s.client.Send(gwStreamDelete, map[string]any{"stream_key": streamKey})

	conn.streamConnection.stop()
	conn.streamConnection = nil

}

// LeaveVoice disconnects from voice and clears local state.
func (s *Streamer) LeaveVoice() {

	s.mu.Lock()
	conn := s.voiceConnection
	s.voiceConnection = nil
	s.mu.Unlock()

	if conn != nil {
		conn.stop()
	}

	_ = s.client.Send(gwVoiceStateUpdate, map[string]any{
		"guild_id":   nil,
		"channel_id": nil,
		"self_mute":  true,
		"self_deaf":  false,
		"self_video": false,
	})

}

// VoiceConnection returns the active voice connection, if any.
func (s *Streamer) VoiceConnection() *VoiceConnection {

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.voiceConnection

}
