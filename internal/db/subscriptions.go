package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const subscriptionsCollection = "subscriptions"

type Subscription struct {
	ID primitive.ObjectID `bson:"_id,omitempty"`

	GuildID string `bson:"guildId"`
	Team    string `bson:"team"`

	VoiceChannelID string `bson:"voiceChannelId"`
	TextChannelID  string `bson:"textChannelId"`

	// LastMatchID avoids re-triggering the same fixture on every poll.
	LastMatchID string `bson:"lastMatchId,omitempty"`

	CreatedAt time.Time `bson:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

func (c *Client) ensureSubscriptions(ctx context.Context) error {

	if c.subscriptions != nil {

		return nil

	}

	c.subscriptions = c.client.Database(databaseName).Collection(subscriptionsCollection)

	_, err := c.subscriptions.Indexes().CreateOne(ctx, mongo.IndexModel{

		Keys: bson.D{

			{Key: "guildId", Value: 1},
			{Key: "team", Value: 1},
		},

		Options: options.Index().SetUnique(true),
	})

	return err

}

func (c *Client) CreateSubscription(ctx context.Context, guildID, team, voiceChannelID, textChannelID string) (*Subscription, error) {

	guildID = trim(guildID)
	team = trim(team)
	voiceChannelID = trim(voiceChannelID)
	textChannelID = trim(textChannelID)

	if guildID == "" || team == "" || voiceChannelID == "" || textChannelID == "" {

		return nil, fmt.Errorf("subscription fields are required")

	}

	if err := c.ensureSubscriptions(ctx); err != nil {

		return nil, err

	}

	now := time.Now().UTC()

	sub := Subscription{

		GuildID: guildID,
		Team:    team,

		VoiceChannelID: voiceChannelID,
		TextChannelID:  textChannelID,

		CreatedAt: now,
		UpdatedAt: now,
	}

	result, err := c.subscriptions.UpdateOne(
		ctx,
		bson.M{"guildId": guildID, "team": team},
		bson.M{

			"$set": bson.M{

				"voiceChannelId": voiceChannelID,
				"textChannelId":  textChannelID,
				"updatedAt":      now,
			},

			"$setOnInsert": bson.M{

				"guildId":   guildID,
				"team":      team,
				"createdAt": now,
			},
		},
		options.Update().SetUpsert(true),
	)

	if err != nil {

		return nil, err

	}

	if result.UpsertedID != nil {

		if id, ok := result.UpsertedID.(primitive.ObjectID); ok {

			sub.ID = id

		}

	} else {

		var existing Subscription

		if err := c.subscriptions.FindOne(ctx, bson.M{"guildId": guildID, "team": team}).Decode(&existing); err == nil {

			sub = existing
			sub.VoiceChannelID = voiceChannelID
			sub.TextChannelID = textChannelID
			sub.UpdatedAt = now

		}

	}

	return &sub, nil

}

func (c *Client) ListSubscriptions(ctx context.Context, guildID string) ([]Subscription, error) {

	guildID = trim(guildID)

	if guildID == "" {

		return nil, nil

	}

	if err := c.ensureSubscriptions(ctx); err != nil {

		return nil, err

	}

	cursor, err := c.subscriptions.Find(ctx, bson.M{"guildId": guildID}, options.Find().SetSort(bson.D{{Key: "team", Value: 1}}))

	if err != nil {

		return nil, err

	}

	defer cursor.Close(ctx)

	var subs []Subscription

	if err := cursor.All(ctx, &subs); err != nil {

		return nil, err

	}

	return subs, nil

}

func (c *Client) ListAllSubscriptions(ctx context.Context) ([]Subscription, error) {

	if err := c.ensureSubscriptions(ctx); err != nil {

		return nil, err

	}

	cursor, err := c.subscriptions.Find(ctx, bson.M{})

	if err != nil {

		return nil, err

	}

	defer cursor.Close(ctx)

	var subs []Subscription

	if err := cursor.All(ctx, &subs); err != nil {

		return nil, err

	}

	return subs, nil

}

func (c *Client) GetSubscription(ctx context.Context, guildID, id string) (*Subscription, error) {

	guildID = trim(guildID)
	id = trim(id)

	if guildID == "" || id == "" {

		return nil, nil

	}

	objectID, err := primitive.ObjectIDFromHex(id)

	if err != nil {

		return nil, nil

	}

	if err := c.ensureSubscriptions(ctx); err != nil {

		return nil, err

	}

	var sub Subscription

	err = c.subscriptions.FindOne(ctx, bson.M{"_id": objectID, "guildId": guildID}).Decode(&sub)

	if err != nil {

		if err == mongo.ErrNoDocuments {

			return nil, nil

		}

		return nil, err

	}

	return &sub, nil

}

func (c *Client) DeleteSubscription(ctx context.Context, guildID, id string) (bool, error) {

	guildID = trim(guildID)
	id = trim(id)

	if guildID == "" || id == "" {

		return false, nil

	}

	objectID, err := primitive.ObjectIDFromHex(id)

	if err != nil {

		return false, nil

	}

	if err := c.ensureSubscriptions(ctx); err != nil {

		return false, err

	}

	result, err := c.subscriptions.DeleteOne(ctx, bson.M{"_id": objectID, "guildId": guildID})

	if err != nil {

		return false, err

	}

	return result.DeletedCount > 0, nil

}

func (c *Client) MarkSubscriptionMatch(ctx context.Context, id primitive.ObjectID, matchID string) error {

	if id.IsZero() || trim(matchID) == "" {

		return nil

	}

	if err := c.ensureSubscriptions(ctx); err != nil {

		return err

	}

	_, err := c.subscriptions.UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{

			"lastMatchId": matchID,
			"updatedAt":   time.Now().UTC(),
		}},
	)

	return err

}
