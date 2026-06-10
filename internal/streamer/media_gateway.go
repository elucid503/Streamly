package streamer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/godave"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"streamly/internal/libdc"
)

// Voice opcodes on the voice gateway websocket.
const (
	voiceIdentify              = 0
	voiceSelectProtocol        = 1
	voiceReady                 = 2
	voiceHeartbeat             = 3
	voiceSelectProtocolAck     = 4
	voiceSpeaking              = 5
	voiceHello                 = 8
	voiceClientsConnect        = 11
	voiceVideo                 = 12
	voiceClientDisconnect      = 13
	voiceMediaSinkWants        = 15
	voiceDavePrepareTransition = 21
	voiceDaveExecuteTransition = 22
	voiceDaveTransitionReady   = 23
	voiceDavePrepareEpoch      = 24
	voiceMLSExternalSender     = 25
	voiceMLSKeyPackage         = 26
	voiceMLSProposals          = 27
	voiceMLSCommitWelcome      = 28
	voiceMLSAnnounceCommit     = 29
	voiceMLSWelcome            = 30
	voiceMLSInvalidCommit      = 31
)

// Discord voice-gateway close codes that should not trigger reconnect.
const voiceCloseDisconnected = 4014

type gatewayCallbacks struct {
	gateway *mediaGateway
}

func (c gatewayCallbacks) SendMLSKeyPackage(mlsKeyPackage []byte) error {

	return c.gateway.sendBinary(voiceMLSKeyPackage, mlsKeyPackage)

}

func (c gatewayCallbacks) SendMLSCommitWelcome(mlsCommitWelcome []byte) error {

	return c.gateway.sendBinary(voiceMLSCommitWelcome, mlsCommitWelcome)

}

func (c gatewayCallbacks) SendReadyForTransition(transitionID uint16) error {

	return c.gateway.send(voiceDaveTransitionReady, map[string]any{"transition_id": transitionID})

}

func (c gatewayCallbacks) SendInvalidCommitWelcome(transitionID uint16) error {

	return c.gateway.send(voiceMLSInvalidCommit, map[string]any{"transition_id": transitionID})

}

// mediaGateway owns one voice-gateway websocket and optional WebRTC media.
type mediaGateway struct {
	label         string
	serverID      string
	botID         string
	daveChannelID godave.ChannelID
	signalingOnly bool // Voice join uses the gateway for session setup only; Go Live uses full WebRTC.

	sessionID string
	server    string
	token     string

	hasSession bool
	hasToken   bool
	closed     bool

	dave     *daveSession
	peer     *MediaPeer
	ready    chan *MediaPeer
	readDone chan struct{} // Closed when readLoop exits, so teardown can stop reading before destroying the peer.

	mu              sync.Mutex
	conn            *websocket.Conn
	seq             int
	heartbeatCancel context.CancelFunc
	reconnecting    atomic.Bool
}

func newMediaGateway(label, serverID, botID string, daveChannelID godave.ChannelID, ready chan *MediaPeer, signalingOnly bool) *mediaGateway {

	gateway := &mediaGateway{label: label, serverID: serverID, botID: botID, daveChannelID: daveChannelID, ready: ready, signalingOnly: signalingOnly, readDone: make(chan struct{})}
	gateway.dave = newDaveSession(botID, gatewayCallbacks{gateway: gateway})
	gateway.dave.SetChannelID(daveChannelID)

	return gateway

}

func (g *mediaGateway) setDaveChannelID(channelID godave.ChannelID) {

	g.daveChannelID = channelID
	g.dave.SetChannelID(channelID)

}

func (g *mediaGateway) setSession(sessionID string) {

	g.mu.Lock()
	g.sessionID = sessionID
	g.hasSession = true
	g.mu.Unlock()

	g.tryConnect()

}

func (g *mediaGateway) setTokens(server, token string) {

	g.mu.Lock()
	g.server = server
	g.token = token
	g.hasToken = true
	g.mu.Unlock()

	g.tryConnect()

}

func (g *mediaGateway) tryConnect() {

	g.mu.Lock()

	if g.closed || !g.hasSession || !g.hasToken || g.conn != nil {
		g.mu.Unlock()
		return
	}

	g.mu.Unlock()

	if err := g.dial(); err != nil {
		log.Printf("[streamer] %s gateway dial: %v", g.label, err)
	}

}

func (g *mediaGateway) dial() error {

	endpoint := strings.TrimPrefix(g.server, "wss://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	u := url.URL{Scheme: "wss", Host: endpoint, Path: "/", RawQuery: "v=8"}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)

	if err != nil {
		return err
	}

	g.mu.Lock()

	if g.closed {
		g.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("%s gateway closed", g.label)
	}

	g.conn = conn
	g.readDone = make(chan struct{})
	g.mu.Unlock()

	go g.readLoop()

	return nil

}

func isIntentionalGatewayClose(err error) bool {

	var closeErr *websocket.CloseError

	if !errors.As(err, &closeErr) {
		return false
	}

	return closeErr.Code == voiceCloseDisconnected

}

func (g *mediaGateway) stopHeartbeat() {

	g.mu.Lock()
	cancel := g.heartbeatCancel
	g.heartbeatCancel = nil
	g.mu.Unlock()

	if cancel != nil {
		cancel()
	}

}

func (g *mediaGateway) resetPeer() {

	if g.peer == nil {
		return
	}

	g.peer.close()
	g.peer = nil

}

func (g *mediaGateway) handleDisconnect(err error) {

	g.stopHeartbeat()

	g.mu.Lock()
	conn := g.conn
	g.conn = nil
	g.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}

	if !g.signalingOnly {
		g.resetPeer()
	}

	if g.closed {
		return
	}

	if isIntentionalGatewayClose(err) {
		return
	}

	log.Printf("[streamer] %s gateway read: %v; reconnecting", g.label, err)

	if !g.reconnecting.CompareAndSwap(false, true) {
		return
	}

	go func() {

		defer g.reconnecting.Store(false)
		g.reconnectLoop()

	}()

}

func (g *mediaGateway) reconnectLoop() {

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for attempt := 1; ; attempt++ {

		if g.closed {
			return
		}

		g.mu.Lock()
		hasCreds := g.hasSession && g.hasToken
		g.mu.Unlock()

		if !hasCreds {
			log.Printf("[streamer] %s gateway reconnect skipped: missing session credentials", g.label)
			return
		}

		time.Sleep(backoff)

		if g.closed {
			return
		}

		if err := g.dial(); err != nil {
			log.Printf("[streamer] %s gateway reconnect attempt %d: %v", g.label, attempt, err)

			backoff = min(backoff*2, maxBackoff)

			continue
		}

		log.Printf("[streamer] %s gateway reconnected", g.label)

		return

	}

}

func (g *mediaGateway) readLoop() {

	defer close(g.readDone)

	for {

		messageType, raw, err := g.conn.ReadMessage()

		if err != nil {
			g.handleDisconnect(err)
			return
		}

		if messageType == websocket.BinaryMessage {
			g.onBinary(raw)
			continue
		}

		var packet struct {
			Op  int             `json:"op"`
			D   json.RawMessage `json:"d"`
			Seq int             `json:"seq"`
		}

		if err := json.Unmarshal(raw, &packet); err != nil {
			continue
		}

		if packet.Seq > 0 {
			g.seq = packet.Seq
		}

		switch packet.Op {
		case voiceHello:
			g.onHello(packet.D)
		case voiceReady:
			g.onReady(packet.D)
		case voiceSelectProtocolAck:
			g.onSelectProtocolAck(packet.D)
		case voiceClientsConnect:
			g.onClientsConnect(packet.D)
		case voiceClientDisconnect:
			g.onClientDisconnect(packet.D)
		case voiceMediaSinkWants:
		case voiceDavePrepareTransition:
			g.onDavePrepareTransition(packet.D)
		case voiceDaveExecuteTransition:
			g.onDaveExecuteTransition(packet.D)
		case voiceDavePrepareEpoch:
			g.onDavePrepareEpoch(packet.D)
		default:
			if packet.Op >= 4000 {
				log.Printf("[streamer] %s gateway error %d: %s", g.label, packet.Op, string(packet.D))
			}
		}

	}

}

func (g *mediaGateway) onBinary(raw []byte) {

	if len(raw) < 3 {
		return
	}

	g.seq = int(binary.BigEndian.Uint16(raw[0:2]))
	op := raw[2]
	payload := raw[3:]

	switch op {
	case voiceMLSExternalSender:
		g.dave.OnDaveMLSExternalSenderPackage(payload)
	case voiceMLSProposals:
		if len(payload) < 1 {
			return
		}

		g.dave.OnDaveMLSProposals(payload)
	case voiceMLSAnnounceCommit:
		if len(payload) < 2 {
			return
		}

		transitionID := binary.BigEndian.Uint16(payload[0:2])
		g.dave.OnDaveMLSPrepareCommitTransition(transitionID, payload[2:])
	case voiceMLSWelcome:
		if len(payload) < 2 {
			return
		}

		transitionID := binary.BigEndian.Uint16(payload[0:2])
		g.dave.OnDaveMLSWelcome(transitionID, payload[2:])
	}

}

func (g *mediaGateway) onHello(data json.RawMessage) {

	var hello struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}

	_ = json.Unmarshal(data, &hello)

	g.stopHeartbeat()

	ctx, cancel := context.WithCancel(context.Background())

	g.mu.Lock()
	g.heartbeatCancel = cancel
	g.mu.Unlock()

	go func() {

		ticker := time.NewTicker(time.Duration(hello.HeartbeatInterval) * time.Millisecond)
		defer ticker.Stop()

		for {

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = g.send(voiceHeartbeat, map[string]any{"t": time.Now().UnixMilli(), "seq_ack": g.seq})
			}

		}

	}()

	_ = g.send(voiceIdentify, map[string]any{
		"server_id":                 g.serverID,
		"user_id":                   g.botID,
		"session_id":                g.sessionID,
		"token":                     g.token,
		"video":                     true,
		"streams":                   StreamsSimulcast,
		"max_dave_protocol_version": g.maxDaveProtocolVersion(),
	})

}

func (g *mediaGateway) maxDaveProtocolVersion() int {

	if os.Getenv("STREAMLY_DISABLE_DAVE") == "1" {
		log.Printf("[streamer] %s dave disabled by STREAMLY_DISABLE_DAVE=1", g.label)
		return daveDisabledProtocolVersion
	}

	return g.dave.MaxSupportedProtocolVersion()

}

type voiceReadyData struct {
	SSRC    int    `json:"ssrc"`
	Port    int    `json:"port"`
	IP      string `json:"ip"`
	Streams []struct {
		SSRC    int `json:"ssrc"`
		RtxSSRC int `json:"rtx_ssrc"`
	} `json:"streams"`
}

func (g *mediaGateway) onReady(data json.RawMessage) {

	if g.signalingOnly {
		g.signalPeerReady(nil)

		return
	}

	var ready voiceReadyData

	_ = json.Unmarshal(data, &ready)

	if len(ready.Streams) == 0 {
		log.Printf("[streamer] %s gateway ready without streams", g.label)
		return
	}

	g.peer = newMediaPeer(g, ready.SSRC, ready.Streams[0].SSRC, ready.Streams[0].RtxSSRC)
	g.dave.AssignSsrcToCodec(uint32(ready.SSRC), godave.CodecOpus)
	g.dave.AssignVideoSsrc(uint32(ready.Streams[0].SSRC))
	g.setVideoAttributes(false, 0, 0, 0)
	g.peer.negotiate()

}

func (g *mediaGateway) signalPeerReady(peer *MediaPeer) {

	select {
	case g.ready <- peer:
	default:
	}

}

func (g *mediaGateway) onSelectProtocolAck(data json.RawMessage) {

	if g.peer == nil {
		return
	}

	var ack struct {
		DaveProtocolVersion int `json:"dave_protocol_version"`
	}

	_ = json.Unmarshal(data, &ack)

	g.dave.OnSelectProtocolAck(uint16(ack.DaveProtocolVersion))

	rawSDP := extractAckSDP(data)
	remoteSDP := prepareRemoteSDP(rawSDP)

	if remoteSDP == "" {
		log.Printf("[streamer] %s select protocol ack missing sdp: %s", g.label, string(data))
		return
	}

	if err := validateSDP(remoteSDP); err != nil {
		log.Printf("[streamer] %s prepared sdp invalid (%d bytes from %d raw): %v", g.label, len(remoteSDP), len(rawSDP), err)
		return
	}

	if err := g.peer.setRemoteDescription(remoteSDP); err != nil {
		log.Printf("[streamer] %s remote sdp: %v", g.label, err)
		return
	}

	g.signalPeerReady(g.peer)

}

func (g *mediaGateway) onClientsConnect(data json.RawMessage) {

	var payload struct {
		UserIDs []string `json:"user_ids"`
	}

	_ = json.Unmarshal(data, &payload)

	for _, userID := range payload.UserIDs {
		g.dave.AddUser(godave.UserID(userID))
	}

}

func (g *mediaGateway) onClientDisconnect(data json.RawMessage) {

	var payload struct {
		UserID string `json:"user_id"`
	}

	_ = json.Unmarshal(data, &payload)
	g.dave.RemoveUser(godave.UserID(payload.UserID))

}

func (g *mediaGateway) onDavePrepareTransition(data json.RawMessage) {

	var payload struct {
		TransitionID    uint16 `json:"transition_id"`
		ProtocolVersion uint16 `json:"protocol_version"`
	}

	_ = json.Unmarshal(data, &payload)
	g.dave.OnDavePrepareTransition(payload.TransitionID, payload.ProtocolVersion)

}

func (g *mediaGateway) onDaveExecuteTransition(data json.RawMessage) {

	var payload struct {
		TransitionID uint16 `json:"transition_id"`
	}

	_ = json.Unmarshal(data, &payload)
	g.dave.OnDaveExecuteTransition(payload.TransitionID)

}

func (g *mediaGateway) onDavePrepareEpoch(data json.RawMessage) {

	var payload struct {
		Epoch           int    `json:"epoch"`
		ProtocolVersion uint16 `json:"protocol_version"`
	}

	_ = json.Unmarshal(data, &payload)
	g.dave.OnDavePrepareEpoch(payload.Epoch, payload.ProtocolVersion)

}

func (g *mediaGateway) send(op int, data any) error {

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.conn == nil {
		return fmt.Errorf("%s gateway closed", g.label)
	}

	payload, err := json.Marshal(map[string]any{"op": op, "d": data})

	if err != nil {
		return err
	}

	return g.conn.WriteMessage(websocket.TextMessage, payload)

}

func (g *mediaGateway) sendBinary(op int, data []byte) error {

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.conn == nil {
		return fmt.Errorf("%s gateway closed", g.label)
	}

	buf := make([]byte, 1+len(data))
	buf[0] = byte(op)
	copy(buf[1:], data)

	return g.conn.WriteMessage(websocket.BinaryMessage, buf)

}

func (g *mediaGateway) setSpeaking(speaking bool, stream bool) {

	if g.closed || g.peer == nil {
		return
	}

	flag := 0

	if speaking {
		if stream {
			flag = 2
		} else {
			flag = 1
		}
	}

	payload := map[string]any{"speaking": flag, "delay": 0, "ssrc": g.peer.audioSSRC}
	_ = g.send(voiceSpeaking, payload)

}

func (g *mediaGateway) setVideoAttributes(enabled bool, width, height, fps int) {

	if g.closed || g.peer == nil {
		return
	}

	if !enabled {
		payload := map[string]any{"audio_ssrc": g.peer.audioSSRC, "video_ssrc": 0, "rtx_ssrc": 0, "streams": []any{}}
		_ = g.send(voiceVideo, payload)
		return
	}

	payload := map[string]any{
		"audio_ssrc": g.peer.audioSSRC,
		"video_ssrc": g.peer.videoSSRC,
		"rtx_ssrc":   g.peer.rtxSSRC,
		"streams": []any{map[string]any{
			"type": "video", "rid": "100", "ssrc": g.peer.videoSSRC, "active": true, "quality": 100,
			"rtx_ssrc": g.peer.rtxSSRC, "max_bitrate": 10_000_000, "max_framerate": fps,
			"max_resolution": map[string]any{"type": "fixed", "width": width, "height": height},
		}},
	}
	_ = g.send(voiceVideo, payload)

}

func (g *mediaGateway) stop() {

	g.closed = true
	g.stopHeartbeat()

	// Close the socket and let readLoop exit first, so no handler touches the peer or DAVE while we tear them down.
	g.mu.Lock()
	conn := g.conn
	g.conn = nil
	readDone := g.readDone
	g.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}

	if readDone != nil {
		select {
		case <-readDone:
		case <-time.After(2 * time.Second):
		}
	}

	if g.peer != nil {
		g.peer.close()
		g.peer = nil
	}

}

// MediaPeer wraps the libdatachannel peer used to ship encoded frames.
type MediaPeer struct {
	gateway   *mediaGateway
	audioSSRC int
	videoSSRC int
	rtxSSRC   int

	peer             atomic.Pointer[libdc.Peer]
	closed           atomic.Bool
	packetizersReady atomic.Bool
	closeMu          sync.Mutex
	videoScratch     []byte // Reused by the video pump for SPS rewrite output.
}

func newMediaPeer(gateway *mediaGateway, audioSSRC, videoSSRC, rtxSSRC int) *MediaPeer {

	return &MediaPeer{gateway: gateway, audioSSRC: audioSSRC, videoSSRC: videoSSRC, rtxSSRC: rtxSSRC}

}

func (m *MediaPeer) negotiate() {

	peer, err := libdc.NewPeer("stun:stun.l.google.com:19302")

	if err != nil {
		log.Printf("[streamer] libdatachannel peer: %v", err)
		return
	}

	m.peer.Store(peer)

	peer.OnLocalDescription(func(sdp string, offer bool) {

		if !offer {
			return
		}

		_ = m.gateway.send(voiceSelectProtocol, map[string]any{
			"protocol":          "webrtc",
			"codecs":            SelectProtocolCodecs,
			"data":              sdp,
			"sdp":               sdp,
			"rtc_connection_id": uuid.NewString(),
		})

	})

	if err := peer.AddAudioTrack(uint32(m.audioSSRC), CodecOpus.PayloadType); err != nil {
		log.Printf("[streamer] audio track: %v", err)
		m.peer.Store(nil)
		peer.Destroy()
		return
	}

	if err := peer.AddVideoTrack(uint32(m.videoSSRC), uint32(m.rtxSSRC), CodecH264.PayloadType, CodecH264.RtxPayloadType); err != nil {
		log.Printf("[streamer] video track: %v", err)
		m.peer.Store(nil)
		peer.Destroy()
		return
	}

	peer.CreateOffer()

}

func (m *MediaPeer) setRemoteDescription(sdp string) error {

	peer := m.peer.Load()

	if peer == nil {
		return fmt.Errorf("peer connection not initialized")
	}

	return peer.SetRemoteAnswer(sdp)

}

func (m *MediaPeer) setupPacketizers() error {

	peer := m.peer.Load()

	if peer == nil {
		return fmt.Errorf("peer connection not initialized")
	}

	return peer.SetupPacketizers(uint32(m.audioSSRC), CodecOpus.PayloadType, uint32(m.videoSSRC), CodecH264.PayloadType)

}

func (m *MediaPeer) sendReady() bool {

	peer := m.peer.Load()

	if m.closed.Load() || peer == nil {
		return false
	}

	return peer.Connected() && peer.MediaReady() && m.mediaAllowed()

}

func (m *MediaPeer) waitSendReady(ctx context.Context) error {

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {

		if m.sendReady() {

			if !m.packetizersReady.Load() {

				if err := m.setupPacketizers(); err != nil {
					return err
				}

				m.packetizersReady.Store(true)

			}

			return nil
		}

		select {
		case <-ctx.Done():
			peer := m.peer.Load()
			return fmt.Errorf("send path not ready: connected=%v tracks=%v dave=%v: %w", peer != nil && peer.Connected(), peer != nil && peer.MediaReady(), m.mediaAllowed(), ctx.Err())
		case <-ticker.C:
		}

	}

}

func (m *MediaPeer) mediaAllowed() bool {

	if m.gateway.dave == nil {
		return true
	}

	return m.gateway.dave.MediaReady()

}

func (m *MediaPeer) sendVideo(data []byte, duration time.Duration) {

	if m.closed.Load() {
		return
	}

	peer := m.peer.Load()

	if peer == nil {
		return
	}

	data = rewriteH264SPSInto(&m.videoScratch, data)

	encrypted, err := m.gateway.dave.EncryptVideo(uint32(m.videoSSRC), data)

	if err != nil {
		encrypted = data
	}

	peer.SendVideo(encrypted, duration.Seconds()*1000)

}

func (m *MediaPeer) sendAudio(data []byte, duration time.Duration) {

	if m.closed.Load() {
		return
	}

	peer := m.peer.Load()

	if peer == nil {
		return
	}

	encrypted, err := m.gateway.dave.EncryptAudio(uint32(m.audioSSRC), data)

	if err != nil {
		encrypted = data
	}

	peer.SendAudio(encrypted, duration.Seconds()*1000)

}

func (m *MediaPeer) advanceAudio(duration time.Duration) {

	if m.closed.Load() {
		return
	}

	peer := m.peer.Load()

	if peer == nil {
		return
	}

	peer.AdvanceAudio(duration.Seconds() * 1000)

}

func (m *MediaPeer) advanceVideo(duration time.Duration) {

	if m.closed.Load() {
		return
	}

	peer := m.peer.Load()

	if peer == nil {
		return
	}

	peer.AdvanceVideo(duration.Seconds() * 1000)

}

func (m *MediaPeer) close() {

	m.closeMu.Lock()
	defer m.closeMu.Unlock()

	if m.closed.Load() {
		return
	}

	m.closed.Store(true)

	if peer := m.peer.Swap(nil); peer != nil {
		peer.Destroy()
	}

}

func parseDaveChannelID(channelID string) godave.ChannelID {

	id, _ := strconv.ParseUint(channelID, 10, 64)

	return godave.ChannelID(id)

}

func parseStreamDaveChannelID(rtcServerID string) godave.ChannelID {

	id, _ := strconv.ParseUint(rtcServerID, 10, 64)

	if id == 0 {
		return 0
	}

	return godave.ChannelID(id - 1)

}
