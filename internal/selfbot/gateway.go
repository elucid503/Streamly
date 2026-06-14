package selfbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func discordGatewayURL() string {

	return os.Getenv("DISCORD_GATEWAY_URL")

}

type gateway struct {

	client *Client
	conn *websocket.Conn

	mu sync.Mutex
	sessionID string
	sequence int
	hasSequence bool
	heartbeatCancel context.CancelFunc
	lastBeatAck bool

	identified chan error
	done chan struct{}

}

func newGateway(client *Client) *gateway {

	return &gateway{

		client: client,
		identified: make(chan error, 1),
		lastBeatAck: true,
		done: make(chan struct{}),

	}

}

func (g *gateway) connect(ctx context.Context) error {

	dialer := websocket.Dialer{

		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: chromeTLSConfig(),

	}

	conn, _, err := dialer.DialContext(ctx, discordGatewayURL(), gatewayHeaders())

	if err != nil {

		return fmt.Errorf("gateway dial: %w", err)

	}

	g.conn = conn

	go g.readLoop(ctx)

	select {

		case err := <-g.identified:

			return err

		case <-ctx.Done():

			g.close()
			return ctx.Err()

		case <-time.After(30 * time.Second):

			g.close()
			return fmt.Errorf("gateway READY timed out")

	}

}

func (g *gateway) send(op int, data any) error {

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.conn == nil {

		return fmt.Errorf("gateway closed")

	}

	payload, err := json.Marshal(map[string]any{"op": op, "d": data})

	if err != nil {

		return err

	}

	return g.conn.WriteMessage(websocket.TextMessage, payload)

}

func (g *gateway) heartbeatPayload() any {

	if !g.hasSequence {

		return nil

	}

	return g.sequence

}

func (g *gateway) readLoop(ctx context.Context) {

	defer func() {

		g.close()
		close(g.done)

	}()

	for {

		select {

			case <-ctx.Done():

				return

			default:

		}

		_, raw, err := g.conn.ReadMessage()

		if err != nil {

			log.Printf("[selfbot] gateway read: %v", err)
			g.signalReady(fmt.Errorf("gateway disconnected: %w", err))

			return

		}

		var packet struct {

			Op int `json:"op"`
			D json.RawMessage `json:"d"`
			S *int `json:"s"`
			T string `json:"t"`

		}

		if err := json.Unmarshal(raw, &packet); err != nil {

			continue

		}

		if packet.S != nil {

			g.sequence = *packet.S
			g.hasSequence = true

		}

		switch packet.Op {

			case opHello:

				g.onHello(packet.D)

			case opHeartbeatAck:

				g.lastBeatAck = true

			case opDispatch:

				g.onDispatch(packet.T, packet.D)

			case opInvalidSession:

				g.onInvalidSession(packet.D)

			case opReconnect:

				log.Printf("[selfbot] gateway requested reconnect")
				g.close()

				return

			default:

				if packet.Op >= 4000 {

					log.Printf("[selfbot] gateway error opcode %d: %s", packet.Op, string(packet.D))

				}

		}

	}

}

func (g *gateway) onHello(data json.RawMessage) {

	var hello struct {

		HeartbeatInterval int `json:"heartbeat_interval"`

	}

	_ = json.Unmarshal(data, &hello)

	if g.heartbeatCancel != nil {

		g.heartbeatCancel()

	}

	ctx, cancel := context.WithCancel(context.Background())
	g.heartbeatCancel = cancel

	go func() {

		ticker := time.NewTicker(time.Duration(hello.HeartbeatInterval) * time.Millisecond)
		defer ticker.Stop()

		for {

			select {

				case <-ctx.Done():

					return

				case <-ticker.C:

					if !g.lastBeatAck {

						log.Printf("[selfbot] heartbeat not acknowledged; closing zombie connection")
						g.close()

						return

					}

					g.lastBeatAck = false
					_ = g.send(opHeartbeat, g.heartbeatPayload())

			}

		}

	}()

	if g.sessionID != "" {

		_ = g.send(opResume, map[string]any{"token": g.client.token, "session_id": g.sessionID, "seq": g.heartbeatPayload()})
		return

	}

	_ = g.send(opIdentify, g.client.props.forIdentify(g.client.token))

}

func (g *gateway) onInvalidSession(data json.RawMessage) {

	var resumable bool
	_ = json.Unmarshal(data, &resumable)

	if !resumable {

		g.sessionID = ""
		g.hasSequence = false

	}

	time.Sleep(time.Second)
	_ = g.send(opIdentify, g.client.props.forIdentify(g.client.token))

}

func (g *gateway) onDispatch(eventType string, data json.RawMessage) {

	switch eventType {

		case "READY":

			var ready struct {

				User struct {

					ID string `json:"id"`

				} `json:"user"`

				SessionID string `json:"session_id"`

			}

			if err := json.Unmarshal(data, &ready); err != nil {

				g.signalReady(fmt.Errorf("ready decode: %w", err))
				return

			}

			g.sessionID = ready.SessionID
			g.client.setUserID(ready.User.ID)

			log.Printf("[selfbot] ready as user %s", ready.User.ID)
			g.signalReady(nil)

		case "RESUMED":

			log.Printf("[selfbot] session resumed")
			g.signalReady(nil)

		case "VOICE_STATE_UPDATE", "VOICE_SERVER_UPDATE", "STREAM_CREATE", "STREAM_SERVER_UPDATE":

			g.client.emit(RawEvent{Type: eventType, Data: data})

	}

}

func (g *gateway) signalReady(err error) {

	select {

		case g.identified <- err: // signal ready or failure

		default: // already signaled

	}

}

func (g *gateway) resumeState() (sessionID string, sequence int, hasSequence bool) {

	g.mu.Lock()
	defer g.mu.Unlock()

	return g.sessionID, g.sequence, g.hasSequence

}

func (g *gateway) close() {

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.heartbeatCancel != nil {

		g.heartbeatCancel()
		g.heartbeatCancel = nil

	}

	if g.conn != nil {

		_ = g.conn.Close()
		g.conn = nil

	}

}
