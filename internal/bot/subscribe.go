package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"streamly/internal/config"
	"streamly/internal/db"
	"streamly/internal/media"
	"streamly/internal/pool"
	"streamly/internal/tvapi"

	"github.com/bwmarrin/discordgo"
)

const subscriptionPollInterval = 2 * time.Minute

func (b *Bot) handleSubscribe(s *discordgo.Session, i *discordgo.InteractionCreate) {

	if i.GuildID == "" {

		respondEphemeral(s, i, "This command can only be used in a server.")
		return

	}

	_ = deferReply(s, i)

	if err := b.Pool.RequireWorker(i.GuildID); err != nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(err.Error())})
		return

	}

	team := strings.TrimSpace(optionString(i, "team"))
	voice := optionChannel(s, i, "voice_channel")
	text := optionChannel(s, i, "text_channel")

	if team == "" {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Pick a team from the list, then try again.")})
		return

	}

	if voice == nil || voice.Type != discordgo.ChannelTypeGuildVoice {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Pick a voice channel for the bot to join.")})
		return

	}

	if text == nil || (text.Type != discordgo.ChannelTypeGuildText && text.Type != discordgo.ChannelTypeGuildNews) {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Pick a text channel for the now-playing message.")})
		return

	}

	if b.DB == nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Subscriptions are unavailable right now.")})
		return

	}

	sub, err := b.DB.CreateSubscription(context.Background(), i.GuildID, team, voice.ID, text.ID)

	if err != nil || sub == nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't save that subscription. Try again in a moment.")})
		return

	}

	msg := fmt.Sprintf(
		"Subscribed to **%s**. When a game starts, Streamly will join **%s** and announce in <#%s>.",
		team,
		voiceChannelDisplay(voice),
		text.ID,
	)

	editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(msg)})

}

func (b *Bot) handleSubscriptions(s *discordgo.Session, i *discordgo.InteractionCreate) {

	if i.GuildID == "" {

		respondEphemeral(s, i, "This command can only be used in a server.")
		return

	}

	_ = deferReply(s, i)

	if b.DB == nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Subscriptions are unavailable right now.")})
		return

	}

	action := strings.TrimSpace(optionString(i, "action"))
	subID := strings.TrimSpace(optionString(i, "subscription"))

	if !strings.EqualFold(action, "delete") {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Pick an action for that subscription.")})
		return

	}

	if subID == "" {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Pick a subscription from the list.")})
		return

	}

	sub, err := b.DB.GetSubscription(context.Background(), i.GuildID, subID)

	if err != nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't load that subscription.")})
		return

	}

	if sub == nil {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("That subscription was not found. Run /subscriptions again.")})
		return

	}

	ok, err := b.DB.DeleteSubscription(context.Background(), i.GuildID, subID)

	if err != nil || !ok {

		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't remove that subscription. Try again in a moment.")})
		return

	}

	editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(fmt.Sprintf("Removed the **%s** subscription.", sub.Team))})

}

func (b *Bot) subscribeTeamChoices(query string) []*discordgo.ApplicationCommandOptionChoice {

	teams, err := b.Resolver.SearchTeams(query, maxOptions)

	if err != nil {

		return nil

	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(teams))

	for _, team := range teams {

		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{

			Name:  truncate(team, 100),
			Value: truncate(team, 100),
		})

	}

	return choices

}

func (b *Bot) subscriptionChoices(guildID, query string) []*discordgo.ApplicationCommandOptionChoice {

	if b.DB == nil || guildID == "" {

		return nil

	}

	subs, err := b.DB.ListSubscriptions(context.Background(), guildID)

	if err != nil {

		return nil

	}

	query = strings.ToLower(strings.TrimSpace(query))
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(subs))

	for _, sub := range subs {

		label := sub.Team

		if query != "" && !strings.Contains(strings.ToLower(label), query) {

			continue

		}

		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{

			Name:  truncate(label, 100),
			Value: sub.ID.Hex(),
		})

		if len(choices) >= maxOptions {

			break

		}

	}

	return choices

}

func (b *Bot) StartSubscriptionLoop() {

	if b.DB == nil {

		return

	}

	go func() {

		// Brief delay so catalog/sports warmup can finish first.
		time.Sleep(15 * time.Second)

		b.pollSubscriptions()

		ticker := time.NewTicker(subscriptionPollInterval)
		defer ticker.Stop()

		for range ticker.C {

			b.pollSubscriptions()

		}

	}()

}

func (b *Bot) pollSubscriptions() {

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	subs, err := b.DB.ListAllSubscriptions(ctx)

	if err != nil {

		log.Printf("[subscribe] list failed: %v", err)
		return

	}

	if len(subs) == 0 {

		return

	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)

	for _, sub := range subs {

		sub := sub

		wg.Add(1)

		go func() {

			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			b.maybeTriggerSubscription(sub)

		}()

	}

	wg.Wait()

}

func (b *Bot) maybeTriggerSubscription(sub db.Subscription) {

	event, ok := b.Resolver.FindTeamEvent(sub.Team)

	if !ok {

		return

	}

	if event.ID == "" || event.ID == sub.LastMatchID {

		return

	}

	channel, ok := b.Resolver.ResolveMatchChannelForTeam(event, sub.Team)

	if !ok || channel.ID == "" {

		log.Printf("[subscribe] no channel for %s match %s", sub.Team, event.ID)
		return

	}

	if err := b.Pool.RequireWorker(sub.GuildID); err != nil {

		return

	}

	display := channel
	display.Name = event.Title

	if event.League != "" {

		display.Category = event.League

	}

	if err := b.startSubscribedLiveStream(sub, display, event); err != nil {

		log.Printf("[subscribe] stream failed for %s in guild %s: %v", sub.Team, sub.GuildID, err)
		return

	}

	_ = b.DB.MarkSubscriptionMatch(context.Background(), sub.ID, event.ID)

}

func (b *Bot) startSubscribedLiveStream(sub db.Subscription, channel tvapi.Channel, event tvapi.SportsEvent) error {

	b.cancelPendingAutoNext(sub.GuildID)

	endpoint, err := b.Resolver.TVStreamEndpoint(channel.ID)

	if err != nil || endpoint.URL == "" {

		return fmt.Errorf("resolve live source: %w", err)

	}

	session, play, err := b.acquireForPlayback(sub.GuildID)

	if err != nil {

		return err

	}

	details := media.TVDetails(channel)
	caption := truncate(channel.Name, 53)

	tvChannel := channel
	metadata := &pool.StreamMetadata{

		Live:      true,
		ChannelID: channel.ID,
		Label:     "Live",

		Details:   details,
		TVChannel: &tvChannel,

		TextChannelID: sub.TextChannelID,
	}

	embed := liveStreamingEmbed(details, channel, sub.VoiceChannelID)

	resolveLive := func() (tvapi.ResolvedStream, error) {

		return b.Resolver.TVStreamEndpoint(metadata.ChannelID)

	}

	err = play(context.Background(), session, pool.Request{

		GuildID:   sub.GuildID,
		ChannelID: sub.VoiceChannelID,

		Caption:    caption,
		InitialURL: endpoint.URL,

		QualityLabel: "Live",

		Headers: config.TVStreamHeadersForReferer(""),
		Live:    true,

		Metadata:  metadata,
		OnPrepare: b.prepareStream,

		ResolveURL: func() (string, error) {

			stream, err := resolveLive()

			if err != nil {

				return "", err

			}

			return stream.URL, nil

		},

		ResolveHeaders: func() map[string]string {

			return config.TVStreamHeadersForReferer("")

		},

		OnClose: func(reason pool.CloseReason) {

			if reason == pool.CloseStopped || sub.TextChannelID == "" {

				return

			}

			embeds, components := endedCard([]*discordgo.MessageEmbed{embed}, closeLabel(reason), nil)
			_, _ = b.Session.ChannelMessageSendComplex(sub.TextChannelID, &discordgo.MessageSend{

				Embeds:     embeds,
				Components: components,
			})

		},
	})

	if err != nil {

		b.Pool.Release(session)
		return err

	}

	voiceName := b.channelName(sub.VoiceChannelID)
	announce := fmt.Sprintf(
		"**%s** vs **%s** is now playing in **%s**!",
		sub.Team,
		event.Opponent(sub.Team),
		voiceName,
	)

	components := controlRow(session.ID, false, true)

	message := &discordgo.MessageSend{

		Content:    announce,
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	}

	if thumb, err := b.Resolver.TVChannelThumb(channel.Logo); err == nil && len(thumb) > 0 {

		streamEmbed := *embed
		streamEmbed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: "attachment://channelthumb.png"}

		message.Embeds = []*discordgo.MessageEmbed{&streamEmbed}
		message.Files = []*discordgo.File{{

			Name:        "channelthumb.png",
			ContentType: "image/png",
			Reader:      bytes.NewReader(thumb),
		}}

	}

	_, err = b.Session.ChannelMessageSendComplex(sub.TextChannelID, message)

	return err

}

func (b *Bot) channelName(channelID string) string {

	if channelID == "" || b.Session == nil {

		return "the voice channel"

	}

	channel, err := b.Session.Channel(channelID)

	if err != nil || channel == nil || channel.Name == "" {

		return "the voice channel"

	}

	return channel.Name

}

func voiceChannelDisplay(channel *discordgo.Channel) string {

	if channel == nil {

		return "the voice channel"

	}

	if channel.Name != "" {

		return channel.Name

	}

	return "the voice channel"

}

func optionChannel(s *discordgo.Session, i *discordgo.InteractionCreate, name string) *discordgo.Channel {

	data := i.ApplicationCommandData()

	for _, option := range data.Options {

		if option.Name != name {

			continue

		}

		if channel := option.ChannelValue(s); channel != nil {

			return channel

		}

		id := option.StringValue()

		if id == "" {

			if value, ok := option.Value.(string); ok {

				id = value

			}

		}

		if id == "" {

			return nil

		}

		channel, err := s.Channel(id)

		if err != nil {

			return &discordgo.Channel{ID: id}

		}

		return channel

	}

	return nil

}
