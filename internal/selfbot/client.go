package selfbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

type RawEvent struct {

	Type string
	Data json.RawMessage
}

type Client struct {

	token string
	props Properties
	userID string

	mu sync.RWMutex
	events chan RawEvent
	gateway *gateway

}

func NewClient(token string) (*Client, error) {

	clean, err := sanitizeToken(token)

	if err != nil {

		return nil, err
	}

	return &Client{

		token: clean,
		props: newProperties(),
		events: make(chan RawEvent, 64),
	}, nil

}

func (c *Client) Login(ctx context.Context) error {

	if err := validateToken(ctx, c.token, c.props); err != nil {

		return err
	}

	c.gateway = newGateway(c)

	if err := c.gateway.connect(ctx); err != nil {

		return err
	}

	go c.maintainGateway(ctx)

	return nil

}

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

func (c *Client) Events() <-chan RawEvent {

	return c.events

}

func (c *Client) Send(op int, data any) error {

	c.mu.RLock()
	gateway := c.gateway
	c.mu.RUnlock()

	if gateway == nil {

		return fmt.Errorf("gateway not connected")
	}

	return gateway.send(op, data)

}

func (c *Client) maintainGateway(ctx context.Context) {

	backoff := time.Second
	const maxBackoff = 60 * time.Second

	for {

		c.mu.RLock()
		current := c.gateway
		c.mu.RUnlock()

		if current == nil {

			return
		}

		select {

		case <-ctx.Done():
			return
		case <-current.done:
		}

		sessionID, sequence, hasSequence := current.resumeState()

		for {

			log.Printf("[selfbot] gateway reconnecting")

			time.Sleep(backoff)

			if ctx.Err() != nil {

				return
			}

			next := newGateway(c)

			if sessionID != "" {

				next.sessionID = sessionID
				next.sequence = sequence
				next.hasSequence = hasSequence
			}

			c.mu.Lock()
			c.gateway = next
			c.mu.Unlock()

			if err := next.connect(ctx); err != nil {

				log.Printf("[selfbot] gateway reconnect failed: %v", err)
				backoff = min(backoff*2, maxBackoff)
				continue
			}

			backoff = time.Second

			break

		}

	}

}

func (c *Client) emit(event RawEvent) {

	select {

	case c.events <- event:
	default:
		log.Printf("[selfbot] dropped gateway event: %s", event.Type)
	}

}
