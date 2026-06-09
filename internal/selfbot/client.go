package selfbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

// RawEvent is a gateway dispatch forwarded to the streamer.
type RawEvent struct {
	Type string
	Data json.RawMessage
}

// Client is a minimal user-account gateway connection for voice streaming only.
type Client struct {
	token  string
	props  Properties
	userID string

	mu     sync.RWMutex
	events chan RawEvent

	gateway *gateway
}

// NewClient validates the token and prepares a client.
func NewClient(token string) (*Client, error) {

	clean, err := sanitizeToken(token)

	if err != nil {
		return nil, err
	}

	return &Client{
		token:  clean,
		props:  newProperties(),
		events: make(chan RawEvent, 64),
	}, nil

}

// Login validates the token against REST, then opens the gateway and blocks until READY.
func (c *Client) Login(ctx context.Context) error {

	if err := validateToken(ctx, c.token, c.props); err != nil {
		return err
	}

	c.gateway = newGateway(c)

	return c.gateway.connect(ctx)

}

// UserID returns the logged-in account id after READY.
func (c *Client) UserID() string {

	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.userID

}

func (c *Client) setUserID(id string) {

	c.mu.Lock()
	defer c.mu.Unlock()

	c.userID = id

}

// Events exposes raw gateway dispatches the streamer cares about.
func (c *Client) Events() <-chan RawEvent {

	return c.events

}

// Send broadcasts a gateway opcode to Discord.
func (c *Client) Send(op int, data any) error {

	if c.gateway == nil {
		return fmt.Errorf("gateway not connected")
	}

	return c.gateway.send(op, data)

}

func (c *Client) emit(event RawEvent) {

	select {
	case c.events <- event:
	default:
		log.Printf("[selfbot] dropped gateway event: %s", event.Type)
	}

}