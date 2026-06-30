package db

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const hintsCollection = "hints"

func (c *Client) ensureHints(ctx context.Context) error {

	if c.hints != nil {

		return nil

	}

	c.hints = c.client.Database(databaseName).Collection(hintsCollection)

	_, err := c.hints.Indexes().CreateOne(ctx, mongo.IndexModel{

		Keys: bson.D{
			{Key: "userId", Value: 1},
			{Key: "hintKey", Value: 1},
		},

		Options: options.Index().SetUnique(true),

	})

	return err

}

func (c *Client) HasSeenHint(ctx context.Context, userID, hintKey string) bool {

	userID = trim(userID)
	hintKey = trim(hintKey)

	if userID == "" || hintKey == "" {

		return false

	}

	if err := c.ensureHints(ctx); err != nil {

		return false

	}

	var row struct{}

	err := c.hints.FindOne(ctx, bson.M{"userId": userID, "hintKey": hintKey}).Decode(&row)

	return err == nil

}

func (c *Client) MarkHintSeen(ctx context.Context, userID, hintKey string) error {

	userID = trim(userID)
	hintKey = trim(hintKey)

	if userID == "" || hintKey == "" {

		return nil

	}

	if err := c.ensureHints(ctx); err != nil {

		return err

	}

	_, err := c.hints.UpdateOne(
		ctx,
		bson.M{"userId": userID, "hintKey": hintKey},
		bson.M{"$setOnInsert": bson.M{"userId": userID, "hintKey": hintKey}},
		options.Update().SetUpsert(true),
	)

	return err

}
