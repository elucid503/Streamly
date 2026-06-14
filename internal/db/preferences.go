package db

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const preferencesCollection = "preferences"

type CaptionPreference struct {

	GuildID string `bson:"guildId"`
	Enabled bool `bson:"enabled"`

	UpdatedAt time.Time `bson:"updatedAt"`

}

func (c *Client) ensurePreferences(ctx context.Context) error {

	if c.preferences != nil {

		return nil

	}

	c.preferences = c.client.Database(databaseName).Collection(preferencesCollection)

	_, err := c.preferences.Indexes().CreateOne(ctx, mongo.IndexModel{

		Keys: bson.D{{Key: "guildId", Value: 1}},

	})

	return err

}

func (c *Client) CaptionsEnabled(ctx context.Context, guildID string) (bool, error) {

	guildID = trim(guildID)

	if guildID == "" {

		return false, nil

	}

	if err := c.ensurePreferences(ctx); err != nil {

		return false, err

	}

	var pref CaptionPreference

	err := c.preferences.FindOne(ctx, bson.M{"guildId": guildID}).Decode(&pref)

	if err != nil {

		return false, nil

	}

	return pref.Enabled, nil

}

func (c *Client) SetCaptionsEnabled(ctx context.Context, guildID string, enabled bool) error {

	guildID = trim(guildID)

	if guildID == "" {

		return nil
	}

	if err := c.ensurePreferences(ctx); err != nil {

		return err
	}

	_, err := c.preferences.UpdateOne(ctx,

		bson.M{"guildId": guildID},

		bson.M{"$set": CaptionPreference{

			GuildID: guildID,

			Enabled: enabled,
			UpdatedAt: time.Now().UTC(),

		}},

		options.Update().SetUpsert(true),
	)

	return err

}
