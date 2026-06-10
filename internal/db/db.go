package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	databaseName      = "Streamly"
	historyCollection = "history"
	historyKeep       = 20
)

// Client wraps Streamly's MongoDB collections.
type Client struct {
	client  *mongo.Client
	history *mongo.Collection
}

// HistoryEntry is a saved stream selection for a user.
type HistoryEntry struct {
	UserID     string    `bson:"userId"`
	Title      string    `bson:"title"`
	Value      string    `bson:"value"`
	StreamedAt time.Time `bson:"streamedAt"`
}

// Connect opens the Streamly database and ensures indexes.
func Connect(ctx context.Context, uri string) (*Client, error) {

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))

	if err != nil {
		return nil, fmt.Errorf("connect mongo: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("ping mongo: %w", err)
	}

	database := client.Database(databaseName)
	history := database.Collection(historyCollection)

	_, err = history.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "userId", Value: 1},
			{Key: "streamedAt", Value: -1},
		},
	})

	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("create history index: %w", err)
	}

	return &Client{client: client, history: history}, nil

}

// RecordStream saves a user's stream pick, deduping by value and trimming old rows.
func (c *Client) RecordStream(ctx context.Context, userID, title, value string) error {

	userID = trim(userID)
	title = trim(title)
	value = trim(value)

	if userID == "" || title == "" || value == "" {
		return nil
	}

	_, _ = c.history.DeleteOne(ctx, bson.M{"userId": userID, "value": value})

	_, err := c.history.InsertOne(ctx, HistoryEntry{
		UserID:     userID,
		Title:      title,
		Value:      value,
		StreamedAt: time.Now().UTC(),
	})

	if err != nil {
		return err
	}

	return c.trimHistory(ctx, userID)

}

// RecentSearches returns the newest distinct stream picks for a user.
func (c *Client) RecentSearches(ctx context.Context, userID string, limit int) ([]HistoryEntry, error) {

	userID = trim(userID)

	if userID == "" || limit <= 0 {
		return nil, nil
	}

	cursor, err := c.history.Find(ctx, bson.M{"userId": userID}, options.Find().
		SetSort(bson.D{{Key: "streamedAt", Value: -1}}).
		SetLimit(int64(limit)))

	if err != nil {
		return nil, err
	}

	defer cursor.Close(ctx)

	var entries []HistoryEntry

	if err := cursor.All(ctx, &entries); err != nil {
		return nil, err
	}

	return entries, nil

}

// Close disconnects the underlying client.
func (c *Client) Close(ctx context.Context) error {

	if c == nil || c.client == nil {
		return nil
	}

	return c.client.Disconnect(ctx)

}

func (c *Client) trimHistory(ctx context.Context, userID string) error {

	cursor, err := c.history.Find(ctx, bson.M{"userId": userID}, options.Find().
		SetSort(bson.D{{Key: "streamedAt", Value: -1}}).
		SetSkip(historyKeep).
		SetProjection(bson.M{"_id": 1}))

	if err != nil {
		return err
	}

	defer cursor.Close(ctx)

	var stale []any

	for cursor.Next(ctx) {

		var row struct {
			ID any `bson:"_id"`
		}

		if err := cursor.Decode(&row); err != nil {
			return err
		}

		stale = append(stale, row.ID)

	}

	if len(stale) == 0 {
		return nil
	}

	_, err = c.history.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": stale}})

	return err

}

func trim(value string) string {

	start := 0
	end := len(value)

	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n' || value[start] == '\r') {
		start++
	}

	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n' || value[end-1] == '\r') {
		end--
	}

	return value[start:end]

}