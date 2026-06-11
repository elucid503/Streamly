package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/config"
	"streamly/internal/media"
	"streamly/internal/pool"
)

const (
	autoNextDelay        = 15 * time.Second
	autoNextSlotWaitMax  = 2 * time.Minute
	autoNextStopSettle   = 750 * time.Millisecond
)

type autoNextState struct {
	generation uint64
	cancel     context.CancelFunc
	guildID    string
	channelID  string
	messageID  string
	voiceID    string
	details    media.TitleDetails
	auto       pool.AutoNextContext
	next       nextEpisode
	invoker    *discordgo.InteractionCreate
	streamCard *discordgo.MessageEmbed
}

func (b *Bot) autoNextGuildState(guildID string) *autoNextState {

	b.autoNextMu.Lock()
	defer b.autoNextMu.Unlock()

	return b.autoNext[guildID]

}

func (b *Bot) isAutoNextCurrent(state *autoNextState) bool {

	if state == nil {
		return false
	}

	b.autoNextMu.Lock()
	defer b.autoNextMu.Unlock()

	current := b.autoNext[state.guildID]

	return current != nil && current.generation == state.generation

}

func (b *Bot) setAutoNextState(state *autoNextState) {

	b.autoNextMu.Lock()
	defer b.autoNextMu.Unlock()

	if existing := b.autoNext[state.guildID]; existing != nil && existing.cancel != nil {
		existing.cancel()
	}

	state.generation = b.autoNextGen + 1
	b.autoNextGen = state.generation
	b.autoNext[state.guildID] = state

}

func (b *Bot) clearAutoNextState(guildID string) {

	b.autoNextMu.Lock()
	defer b.autoNextMu.Unlock()

	if existing := b.autoNext[guildID]; existing != nil && existing.cancel != nil {
		existing.cancel()
	}

	delete(b.autoNext, guildID)

}

// cancelPendingAutoNext aborts a queued auto-next without starting playback.
func (b *Bot) cancelPendingAutoNext(guildID string) {

	b.clearAutoNextState(guildID)

}

func (b *Bot) handleNearEnd(s *discordgo.Session, i *discordgo.InteractionCreate, session *pool.Session, embed *discordgo.MessageEmbed) func() {

	return func() {

		if session.Metadata == nil || session.Metadata.AutoNext == nil {
			return
		}

		if b.autoNextGuildState(i.GuildID) != nil {
			return
		}

		if b.Pool.ActiveInGuild(i.GuildID) == nil {
			return
		}

		next, err := b.resolveNextEpisode(context.Background(), session.Metadata.AutoNext)

		if err != nil || next == nil {
			return
		}

		auto := *session.Metadata.AutoNext

		b.promptAutoNext(s, i, i.GuildID, session.Metadata, embed, auto, *next)

	}

}

func (b *Bot) promptAutoNext(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string, metadata *pool.StreamMetadata, streamCard *discordgo.MessageEmbed, auto pool.AutoNextContext, next nextEpisode) {

	channelID := auto.ChannelID

	if channelID == "" {
		channelID = i.ChannelID
	}

	details := media.TitleDetails{Title: "Your Show"}

	if metadata != nil {
		details = metadata.Details
	}

	nextEpisode := &episodeRef{Season: next.Season, Episode: next.Episode}

	embed := streamingEmbed(details, channelID, nextEpisode)
	embed.Author = &discordgo.MessageEmbedAuthor{Name: "Up Next"}
	embed.Description = fmt.Sprintf("Season %d · Episode %d starts in 15 seconds.", next.Season, next.Episode)

	components := autoNextRow(guildID)

	message, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})

	if err != nil {
		log.Printf("auto-next prompt failed: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	state := &autoNextState{
		cancel:     cancel,
		guildID:    guildID,
		channelID:  channelID,
		messageID:  message.ID,
		voiceID:    auto.VoiceChannelID,
		details:    details,
		auto:       auto,
		next:       next,
		invoker:    i,
		streamCard: streamCard,
	}

	b.setAutoNextState(state)

	go b.autoNextCountdown(ctx, s, state)

}

func (b *Bot) autoNextCountdown(ctx context.Context, s *discordgo.Session, state *autoNextState) {

	timer := time.NewTimer(autoNextDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	b.tryExecuteAutoNext(s, state)

}

func (b *Bot) tryExecuteAutoNext(s *discordgo.Session, state *autoNextState) {

	if !b.isAutoNextCurrent(state) {
		return
	}

	slotCtx, cancel := context.WithTimeout(context.Background(), autoNextSlotWaitMax)
	defer cancel()

	if !b.waitForStreamSlot(slotCtx, state.guildID) {
		if b.isAutoNextCurrent(state) {
			b.failAutoNext(s, state, "Auto-Next Cancelled", "The stream slot stayed busy.")
		}
		return
	}

	if !b.isAutoNextCurrent(state) {
		return
	}

	b.executeAutoNextPlay(s, state)

}

func (b *Bot) waitForStreamSlot(ctx context.Context, guildID string) bool {

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {

		if ctx.Err() != nil {
			return false
		}

		if err := b.Pool.RequireAvailable(guildID); err == nil {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}

	}

}

func (b *Bot) executeAutoNextPlay(s *discordgo.Session, state *autoNextState) {

	if !b.isAutoNextCurrent(state) {
		return
	}

	if err := b.Pool.RequireAvailable(state.guildID); err != nil {
		b.failAutoNext(s, state, "Auto-Next Cancelled", "Another stream is already active.")
		return
	}

	episode := &episodeRef{Season: state.next.Season, Episode: state.next.Episode}

	auto := state.auto
	auto.Season = state.next.Season
	auto.Episode = state.next.Episode

	metadata := pool.StreamMetadata{
		ShareKey:        auto.ShareKey,
		FID:             state.next.FID,
		VideoName:       state.next.FileName,
		Target:          config.Stream.Height,
		Details:         state.details,
		Episode:         &pool.EpisodeRef{Season: episode.Season, Episode: episode.Episode},
		AutoNext:        &auto,
		UserID:          auto.UserID,
		TextChannelID:   auto.ChannelID,
		TextChannelName: textChannelNameForID(s, auto.ChannelID),
	}

	if captions, _ := b.DB.CaptionsEnabled(context.Background(), state.guildID); captions {
		metadata.CaptionsPreferred = true
	}

	if b.startEpisodeFromAutoNext(s, state, metadata, episode) {
		b.deleteAutoNextMessage(s, state)
		b.clearAutoNextState(state.guildID)
	}

}

func (b *Bot) failAutoNext(s *discordgo.Session, state *autoNextState, header, description string) {

	b.disableAutoNextMessage(s, state, header, description)
	b.clearAutoNextState(state.guildID)

}

func (b *Bot) handleAutoNextButton(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {

	if len(parts) < 3 {
		return
	}

	guildID := parts[2]
	action := parts[1]

	state := b.autoNextGuildState(guildID)

	if state == nil {
		b.ackAutoNextInteraction(s, i)
		return
	}

	switch action {
	case "play":
		if state.cancel != nil {
			state.cancel()
		}

		if active := b.Pool.ActiveInGuild(guildID); active != nil {
			b.Pool.Stop(active)
		}

		prompt := autoNextPromptCopy(state)
		b.ackAutoNextInteraction(s, i)
		b.deleteAutoNextMessage(s, prompt)

		go func(st *autoNextState) {
			time.Sleep(autoNextStopSettle)
			b.tryExecuteAutoNext(s, st)
		}(state)

	case "stop":
		if state.cancel != nil {
			state.cancel()
		}

		prompt := autoNextPromptCopy(state)
		b.clearAutoNextState(guildID)
		b.ackAutoNextInteraction(s, i)
		b.deleteAutoNextMessage(s, prompt)
	}

}

func autoNextRow(guildID string) []discordgo.MessageComponent {

	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Play Now",
			CustomID: fmt.Sprintf("autonext:play:%s", guildID),
			Style:    discordgo.SuccessButton,
		},
		discordgo.Button{
			Label:    "Stop",
			CustomID: fmt.Sprintf("autonext:stop:%s", guildID),
			Style:    discordgo.DangerButton,
		},
	}}}

}

func autoNextPromptCopy(state *autoNextState) *autoNextState {

	if state == nil {
		return nil
	}

	copy := *state

	return &copy

}

func (b *Bot) ackAutoNextInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})

	if err != nil {
		log.Printf("auto-next interaction ack failed: %v", err)
	}

}

func (b *Bot) deleteAutoNextMessage(s *discordgo.Session, state *autoNextState) {

	if state == nil || state.channelID == "" || state.messageID == "" {
		return
	}

	if err := s.ChannelMessageDelete(state.channelID, state.messageID); err != nil {
		log.Printf("auto-next prompt delete failed: %v", err)
	}

}

func (b *Bot) disableAutoNextMessage(s *discordgo.Session, state *autoNextState, header, description string) {

	embed := simpleEmbed("Up Next", header, description)

	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    state.channelID,
		ID:         state.messageID,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &[]discordgo.MessageComponent{},
	})

}

// startEpisodeFromAutoNext returns true when playback started successfully.
func (b *Bot) startEpisodeFromAutoNext(s *discordgo.Session, state *autoNextState, metadata pool.StreamMetadata, episode *episodeRef) bool {

	if !b.isAutoNextCurrent(state) {
		return false
	}

	channel, err := s.Channel(state.voiceID)

	if err != nil || channel == nil {
		b.failAutoNext(s, state, "Auto-Next Failed", "Couldn't find the voice channel for the next episode.")
		return false
	}

	session, err := b.Pool.Acquire(state.guildID)

	if err != nil {
		b.failAutoNext(s, state, "Auto-Next Failed", workerErrorMessage(err))
		return false
	}

	qualities, _ := b.Resolver.Qualities(metadata.ShareKey, metadata.FID)
	target := metadata.Target

	if target == 0 {
		target = config.Stream.Height
	}

	selected := media.PickQuality(qualities, target)

	if selected != nil {
		target = media.QualityHeight(*selected)
		metadata.Target = target
		metadata.Label = qualityLabel(*selected)
	} else {
		metadata.Label = fmt.Sprintf("%dp", config.Stream.Height)
	}

	ranked := media.RankedQualityURLs(qualities, target)
	url := ""

	if len(ranked) > 0 {
		url = ranked[0]
	}

	if url == "" {

		resolved, err := b.Resolver.StreamURL(metadata.ShareKey, metadata.FID, target)

		if err != nil || resolved == "" {
			b.Pool.Release(session)
			b.failAutoNext(s, state, "Auto-Next Failed", "No playable source was available for the next episode.")
			return false
		}

		url = resolved

	}

	meta := metadata
	caption := overlayCaption(meta.Details.Title, episode)
	embed := streamingEmbed(meta.Details, channel.ID, episode)

	err = b.Pool.Play(context.Background(), session, pool.Request{
		GuildID:      channel.GuildID,
		ChannelID:    channel.ID,
		Caption:      caption,
		InitialURL:   url,
		QualityLabel: meta.Label,
		Metadata:     &meta,
		OnPrepare:     b.prepareStream,
		OnMediaProbed: b.armIntroOnProbe,
		OnNearEnd:     b.handleNearEnd(s, state.invoker, session, embed),
		ResolveURL: func() (string, error) {
			return b.Resolver.StreamURL(meta.ShareKey, meta.FID, meta.Target)
		},
		QualityURL: func(attempt int) (string, error) {
			qualities, err := b.Resolver.Qualities(meta.ShareKey, meta.FID)

			if err != nil {
				return "", err
			}

			urls := media.RankedQualityURLs(qualities, meta.Target)

			if attempt >= len(urls) {
				return "", fmt.Errorf("no more quality fallbacks")
			}

			return urls[attempt], nil
		},
		OnClose: func(reason pool.CloseReason) {
			if reason == pool.CloseStopped {
				return
			}

			if state.streamCard != nil && state.invoker != nil {
				closeStreamMessage(s, state.invoker, state.streamCard, closeLabel(reason))
			}
		},
	})

	if err != nil {
		b.Pool.Release(session)
		b.failAutoNext(s, state, "Auto-Next Failed", "Couldn't join the voice channel for the next episode.")
		return false
	}

	if state.invoker != nil {
		components := controlRow(session.ID, false, false)
		editMessage(s, state.invoker, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})
	} else if state.auto.ChannelID != "" {
		components := controlRow(session.ID, false, false)
		_, _ = s.ChannelMessageSendComplex(state.auto.ChannelID, &discordgo.MessageSend{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		})
	}

	return true

}