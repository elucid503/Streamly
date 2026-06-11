package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/config"
	"streamly/internal/febapi"
	"streamly/internal/media"
	"streamly/internal/pool"
	"streamly/internal/tvapi"
)

var (
	selectionValueRE = regexp.MustCompile(`^([12]):(\d+)$`)
	seasonNumberRE   = regexp.MustCompile(`(\d+)`)
	episodeNumberREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)s\d{1,2}[ ._-]?e(\d{1,4})`),
		regexp.MustCompile(`(?i)\b\d{1,2}x(\d{1,4})\b`),
		regexp.MustCompile(`(?i)\bepisode[ ._-]?(\d{1,4})\b`),
		regexp.MustCompile(`(?i)\be(\d{1,4})\b`),
	}
)

// streamMedia tracks per-session quality picks for URL re-resolution.
var streamMedia = make(map[string]streamTarget)

type streamTarget struct {
	ShareKey  string
	FID       int
	VideoName string
	Target    int
	Label     string
	Live      bool
	DaddyID   string
	Details   media.TitleDetails
	Episode   *episodeRef
	TVChannel *tvapi.Channel
}

func (b *Bot) onAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {

	if i.ApplicationCommandData().Name == "seek" {
		b.onSeekAutocomplete(s, i)
		return
	}

	query := ""

	for _, option := range i.ApplicationCommandData().Options {
		if option.Name == "title" {
			query = strings.TrimSpace(option.StringValue())
		}
	}

	var choices []*discordgo.ApplicationCommandOptionChoice

	choices = append(choices, b.recentSearchChoices(i, query)...)

	tvLimit := 5

	if query != "" {
		tvLimit = maxOptions
	}

	if remaining := maxOptions - len(choices); remaining < tvLimit {
		tvLimit = remaining
	}

	if tvLimit <= 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionApplicationCommandAutocompleteResult, Data: &discordgo.InteractionResponseData{Choices: choices}})
		return
	}

	tvResults, tvErr := b.Resolver.SearchTV(query, tvLimit)

	if tvErr == nil {

		for _, channel := range tvResults {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  truncate(media.TVAutocompleteLabel(channel), 100),
				Value: media.TVSelectionValue(channel.DaddyID),
			})
		}

	}

	remaining := maxOptions - len(choices)

	if query != "" && remaining > 0 {

		results, err := b.Resolver.Search(query)

		if err == nil {

			for _, result := range results[:minInt(len(results), remaining)] {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
					Name:  autocompleteLabel(result),
					Value: fmt.Sprintf("%d:%d", result.BoxType, result.ID),
				})
			}

		}

	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionApplicationCommandAutocompleteResult, Data: &discordgo.InteractionResponseData{Choices: choices}})

}

func (b *Bot) handleStream(s *discordgo.Session, i *discordgo.InteractionCreate) {

	_ = deferReply(s, i)

	if err := b.Pool.RequireAvailable(i.GuildID); err != nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(err.Error())})
		return
	}

	title := optionString(i, "title")

	if live, err := b.resolveLiveTV(title); live != nil {

		if err != nil {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't resolve that live TV channel.")})
			return
		}

		b.startLiveStream(s, i, *live, live.Name, media.TVSelectionValue(live.DaddyID))
		return

	}

	selection, err := b.Resolver.ResolveSelection(title)

	if err != nil || selection == nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No results found for that title.")})
		return
	}

	shareKey, err := b.Resolver.ShareKey(*selection)

	if err != nil || shareKey == "" {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't find a streamable source for that title.")})
		return
	}

	details, err := b.Resolver.Details(*selection)

	if err != nil {
		details = media.TitleDetails{Title: "Your Selection"}
	}

	if b.Resolver.IsMovie(*selection) {

		file, err := b.Resolver.MovieFile(shareKey)

		if err != nil || file == nil {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No playable file was found for that movie.")})
			return
		}

		b.startStream(s, i, details, shareKey, file.FID, file.FileName, nil, title)
		return

	}

	root, err := b.Resolver.ListChildren(shareKey, 0)

	if err != nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't list files for that show.")})
		return
	}

	seasons := b.Resolver.Seasons(root)

	if len(seasons) > 0 {
		embed := baseEmbed(details, "Select a Season")
		components := []discordgo.MessageComponent{seasonRow(selection.ID, shareKey, seasons)}
		editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})
		return
	}

	episodes := toEpisodes(b.Resolver.Files(root))

	if len(episodes) == 0 {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No episodes were found for that show.")})
		return
	}

	embed := baseEmbed(details, "Select an Episode")
	components := []discordgo.MessageComponent{episodeRow(selection.ID, shareKey, 1, episodes)}
	editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})

}

func (b *Bot) handleSelect(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

	if len(parts) < 4 {
		return
	}

	kind := parts[1]
	rawID := parts[2]
	shareKey := parts[3]

	id, _ := strconv.Atoi(rawID)
	values := i.MessageComponentData().Values

	if len(values) == 0 {
		return
	}

	valueParts := strings.Split(values[0], ":")
	fid, _ := strconv.Atoi(valueParts[0])

	switch kind {
	case "season":

		rawSeason := valueParts[1]
		season, _ := strconv.Atoi(rawSeason)

		details, err := b.Resolver.Details(media.Selection{ID: id, BoxType: febapi.BoxSeries})

		if err != nil {
			details = media.TitleDetails{Title: "Your Selection"}
		}

		children, err := b.Resolver.ListChildren(shareKey, fid)

		if err != nil {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No episodes were found in that season.")})
			return
		}

		episodes := toEpisodes(b.Resolver.Files(children))

		if len(episodes) == 0 {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No episodes were found in that season.")})
			return
		}

		embed := baseEmbed(details, "Select an Episode")
		components := []discordgo.MessageComponent{episodeRow(id, shareKey, season, episodes)}
		editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})

	case "episode":

		if len(parts) < 5 {
			return
		}

		if err := b.Pool.RequireAvailable(i.GuildID); err != nil {
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(err.Error())})
			return
		}

		season, _ := strconv.Atoi(parts[4])
		episode, _ := strconv.Atoi(valueParts[1])

		details, err := b.Resolver.Details(media.Selection{ID: id, BoxType: febapi.BoxSeries})

		if err != nil {
			details = media.TitleDetails{Title: "Your Selection"}
		}

		videoName := b.Resolver.FileName(shareKey, fid)
		b.startStream(s, i, details, shareKey, fid, videoName, &episodeRef{Season: season, Episode: episode}, fmt.Sprintf("%d:%d", febapi.BoxSeries, id))

	}

}

type episodeRef struct {
	Season  int
	Episode int
}

func (b *Bot) startStream(s *discordgo.Session, i *discordgo.InteractionCreate, details media.TitleDetails, shareKey string, fid int, videoName string, episode *episodeRef, historyValue string) {

	channel := memberVoiceChannel(s, i)

	if channel == nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Join a voice channel first, then try again.")})
		return
	}

	session, err := b.Pool.Acquire(channel.GuildID)

	if err != nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(workerErrorMessage(err))})
		return
	}

	qualities, _ := b.Resolver.Qualities(shareKey, fid)

	target := config.Stream.Height
	label := fmt.Sprintf("%dp", config.Stream.Height)

	selected := media.PickQuality(qualities, target)

	if selected != nil {
		target = media.QualityHeight(*selected)
		label = qualityLabel(*selected)
	}

	ranked := media.RankedQualityURLs(qualities, target)
	url := ""

	if len(ranked) > 0 {
		url = ranked[0]
	}

	if url == "" {

		resolved, err := b.Resolver.StreamURL(shareKey, fid, target)

		if err != nil || resolved == "" {
			b.Pool.Release(session)
			editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No playable source was available for that title.")})
			return
		}

		url = resolved

	}

	caption := overlayCaption(details.Title, episode)

	streamMedia[session.ID] = streamTarget{
		ShareKey:  shareKey,
		FID:       fid,
		VideoName: videoName,
		Target:    target,
		Label:     label,
		Details:   details,
		Episode:   episode,
	}

	targetCopy := streamMedia[session.ID]
	embed := streamingEmbed(details, channel.ID, episode)

	err = b.Pool.Play(context.Background(), session, pool.Request{
		GuildID:      channel.GuildID,
		ChannelID:    channel.ID,
		Caption:      caption,
		InitialURL:   url,
		QualityLabel: label,
		ResolveURL: func() (string, error) {
			return b.Resolver.StreamURL(targetCopy.ShareKey, targetCopy.FID, streamMedia[session.ID].Target)
		},
		QualityURL: func(attempt int) (string, error) {
			target := streamMedia[session.ID]
			qualities, err := b.Resolver.Qualities(target.ShareKey, target.FID)

			if err != nil {
				return "", err
			}

			urls := media.RankedQualityURLs(qualities, target.Target)

			if attempt >= len(urls) {
				return "", fmt.Errorf("no more quality fallbacks")
			}

			return urls[attempt], nil
		},
		OnClose: func(reason pool.CloseReason) {
			delete(streamMedia, session.ID)

			if reason == pool.CloseStopped {
				return
			}

			closeStreamMessage(s, i, embed, closeLabel(reason))
		},
	})

	if err != nil {
		log.Printf("failed to start the stream: %v", err)
		delete(streamMedia, session.ID)
		b.Pool.Release(session)
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't join your voice channel to start streaming.")})
		return
	}

	components := controlRow(session.ID, false, false)
	editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})
	b.recordHistory(i, details.Title, historyValue)

}

func (b *Bot) resolveLiveTV(title string) (*tvapi.Channel, error) {

	title = strings.TrimSpace(title)

	if title == "" {
		return nil, nil
	}

	if selectionValueRE.MatchString(title) {
		return nil, nil
	}

	selection, err := b.Resolver.ResolveTVSelection(title)

	if err != nil {
		return nil, err
	}

	if selection == nil {
		return nil, nil
	}

	return &selection.Channel, nil

}

func (b *Bot) startLiveStream(s *discordgo.Session, i *discordgo.InteractionCreate, channel tvapi.Channel, historyTitle, historyValue string) {

	voice := memberVoiceChannel(s, i)

	if voice == nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Join a voice channel first, then try again.")})
		return
	}

	session, err := b.Pool.Acquire(voice.GuildID)

	if err != nil {
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr(workerErrorMessage(err))})
		return
	}

	endpoint, err := b.Resolver.TVStreamEndpoint(channel.DaddyID)

	if err != nil || endpoint.URL == "" {
		b.Pool.Release(session)
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("No live source was available for that channel.")})
		return
	}

	details := media.TVDetails(channel)
	caption := truncate(channel.Name, 53)
	daddyID := channel.DaddyID

	tvChannel := channel
	streamMedia[session.ID] = streamTarget{
		Live:      true,
		DaddyID:   daddyID,
		Label:     "Live",
		Details:   details,
		TVChannel: &tvChannel,
	}

	embed := liveStreamingEmbed(details, channel, voice.ID)

	resolveLive := func() (tvapi.ResolvedStream, error) {
		return b.Resolver.TVStreamEndpoint(streamMedia[session.ID].DaddyID)
	}

	err = b.Pool.Play(context.Background(), session, pool.Request{
		GuildID:      voice.GuildID,
		ChannelID:    voice.ID,
		Caption:      caption,
		InitialURL:   endpoint.URL,
		QualityLabel: "Live",
		Headers:      config.TVStreamHeadersForReferer(endpoint.Referer),
		Live:         true,
		ResolveURL: func() (string, error) {
			stream, err := resolveLive()
			if err != nil {
				return "", err
			}
			return stream.URL, nil
		},
		ResolveHeaders: func() map[string]string {
			stream, err := resolveLive()
			if err != nil || stream.Referer == "" {
				return nil
			}
			return config.TVStreamHeadersForReferer(stream.Referer)
		},
		OnClose: func(reason pool.CloseReason) {
			delete(streamMedia, session.ID)

			if reason == pool.CloseStopped {
				return
			}

			closeStreamMessage(s, i, embed, closeLabel(reason))
		},
	})

	if err != nil {
		log.Printf("failed to start the live stream: %v", err)
		delete(streamMedia, session.ID)
		b.Pool.Release(session)
		editMessage(s, i, &discordgo.WebhookEdit{Content: strPtr("Couldn't join your voice channel to start streaming.")})
		return
	}

	components := controlRow(session.ID, false, true)
	editMessage(s, i, &discordgo.WebhookEdit{Embeds: ptrEmbeds([]*discordgo.MessageEmbed{embed}), Components: ptrComponents(components)})
	b.recordHistory(i, historyTitle, historyValue)

}

func liveStreamingEmbed(details media.TitleDetails, channel tvapi.Channel, voiceChannelID string) *discordgo.MessageEmbed {

	embed := baseEmbed(details, "Now Streaming")

	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name: "Category", Value: channel.Category, Inline: true,
	})

	region := channel.Country.Name
	if region != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name: "Region", Value: region, Inline: true,
		})
	}

	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name: "Channel", Value: fmt.Sprintf("<#%s>", voiceChannelID), Inline: true,
	})

	return embed

}

func (b *Bot) handleControl(s *discordgo.Session, i *discordgo.InteractionCreate, kind string) {

	session := activeSession(s, i, b.Pool)

	if session == nil || !session.Busy {
		respondEmbed(s, i, simpleEmbed("Stream Control", "No Active Stream", "No active stream was found for this server."))
		return
	}

	switch kind {
	case "stop":
		embed := controlEmbed(b.Pool, session, "Stream Stopped", "Playback has been stopped.")
		b.Pool.Stop(session)
		respondEmbed(s, i, embed)
	case "pause":
		if session.Live() {
			respondEmbed(s, i, simpleEmbed("Stream Control", "Live TV", "Live streams cannot be paused."))
			return
		}
		b.Pool.Pause(session)
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Stream Paused", "Playback has been paused."))
	case "resume":
		if session.Live() {
			respondEmbed(s, i, simpleEmbed("Stream Control", "Live TV", "Live streams cannot be paused."))
			return
		}
		b.Pool.Resume(session)
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Stream Resumed", "Playback has resumed."))
	}

}

func (b *Bot) handleStopButton(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {

	if len(parts) < 3 {
		return
	}

	session := b.Pool.Get(parts[2])

	if session != nil {
		b.Pool.Stop(session)
	}

	embeds, components := endedCard(i.Message.Embeds, "Stream Ended")
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embeds, Components: components}})

}

func (b *Bot) handleToggleButton(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {

	if len(parts) < 3 {
		return
	}

	session := b.Pool.Get(parts[2])

	if session == nil || !session.Busy {
		embeds, components := endedCard(i.Message.Embeds, "Stream Ended")
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embeds, Components: components}})
		return
	}

	paused := parts[1] == "pause"

	if session.Live() {
		return
	}

	if paused {
		b.Pool.Pause(session)
	} else {
		b.Pool.Resume(session)
	}

	header := "Now Streaming"

	if paused {
		header = "Paused"
	}

	var embeds []*discordgo.MessageEmbed

	if len(i.Message.Embeds) > 0 {
		card := *i.Message.Embeds[0]
		card.Author = &discordgo.MessageEmbedAuthor{Name: header}
		embeds = []*discordgo.MessageEmbed{&card}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embeds, Components: controlRow(parts[2], paused, session.Live())}})

}

func optionString(i *discordgo.InteractionCreate, name string) string {

	for _, option := range i.ApplicationCommandData().Options {
		if option.Name == name {
			return option.StringValue()
		}
	}

	return ""

}

func autocompleteLabel(result febapi.SearchResult) string {

	kind := "Movie"

	if result.BoxType == febapi.BoxSeries {
		kind = "TV Show"
	}

	year := ""

	if result.Year > 0 {
		year = fmt.Sprintf(" (%d)", result.Year)
	}

	return truncate(fmt.Sprintf("%s • %s%s", kind, result.Title, year), 100)

}

func overlayCaption(title string, episode *episodeRef) string {

	name := truncate(title, 53)

	if episode == nil {
		return name
	}

	return fmt.Sprintf("%s • S%dE%d", name, episode.Season, episode.Episode)

}

func streamingEmbed(details media.TitleDetails, channelID string, episode *episodeRef) *discordgo.MessageEmbed {

	embed := baseEmbed(details, "Now Streaming")

	if episode != nil {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Now Playing", Value: fmt.Sprintf("Season %d · Episode %d", episode.Season, episode.Episode), Inline: true})
	}

	if details.IMDBRating != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "IMDb", Value: details.IMDBRating + " / 10", Inline: true})
	}

	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Channel", Value: fmt.Sprintf("<#%s>", channelID), Inline: true})

	return embed

}

func workerErrorMessage(err error) string {

	if errors.Is(err, pool.ErrNoWorker) || errors.Is(err, pool.ErrWorkerBusy) {
		return err.Error()
	}

	return "Could not start streaming right now. Try again shortly."

}

func closeLabel(reason pool.CloseReason) string {

	if reason == pool.CloseError {
		return "Streaming Failed"
	}

	return "Stream Ended"

}

func strPtr(value string) *string {

	return &value

}

func minInt(a, b int) int {

	if a < b {
		return a
	}

	return b

}

type episode struct {
	FID      int
	Number   int
	FileName string
}

func seasonRow(id int, shareKey string, seasons []febapi.FebboxFile) discordgo.ActionsRow {

	options := make([]discordgo.SelectMenuOption, 0, minInt(len(seasons), maxOptions))

	for index, season := range seasons[:minInt(len(seasons), maxOptions)] {

		info := seasonInfo(season.FileName, index+1)

		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(info.Label, 100),
			Value: fmt.Sprintf("%d:%d", season.FID, info.Number),
		})

	}

	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		CustomID:    fmt.Sprintf("stream:season:%d:%s", id, shareKey),
		Placeholder: "Choose a season",
		Options:     options,
	}}}

}

func episodeRow(id int, shareKey string, season int, episodes []episode) discordgo.ActionsRow {

	options := make([]discordgo.SelectMenuOption, 0, len(episodes))

	for _, ep := range episodes {

		options = append(options, discordgo.SelectMenuOption{
			Label: fmt.Sprintf("Episode %d", ep.Number),
			Value: fmt.Sprintf("%d:%d", ep.FID, ep.Number),
		})

	}

	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		CustomID:    fmt.Sprintf("stream:episode:%d:%s:%d", id, shareKey, season),
		Placeholder: "Choose an episode",
		Options:     options,
	}}}

}

func seasonInfo(name string, ordinal int) struct {
	Number int
	Label  string
} {

	if match := seasonNumberRE.FindStringSubmatch(name); len(match) > 1 {
		number, _ := strconv.Atoi(match[1])
		return struct {
			Number int
			Label  string
		}{Number: number, Label: fmt.Sprintf("Season %d", number)}
	}

	return struct {
		Number int
		Label  string
	}{Number: ordinal, Label: titleCase(name)}

}

func titleCase(text string) string {

	return strings.Title(strings.ToLower(text))

}

func toEpisodes(files []febapi.FebboxFile) []episode {

	byNumber := make(map[int]episode)
	fallback := 0

	for _, file := range files {

		number := episodeNumber(file.FileName)

		if number == 0 {
			fallback++
			number = fallback
		}

		candidate := episode{FID: file.FID, Number: number, FileName: file.FileName}

		if existing, exists := byNumber[number]; !exists {
			byNumber[number] = candidate
		} else if media.StreamFilePreference(candidate.FileName) > media.StreamFilePreference(existing.FileName) {
			byNumber[number] = candidate
		}

	}

	result := make([]episode, 0, len(byNumber))

	for _, ep := range byNumber {
		result = append(result, ep)
	}

	sortEpisodes(result)

	if len(result) > maxOptions {
		result = result[:maxOptions]
	}

	return result

}

func sortEpisodes(episodes []episode) {

	sort.Slice(episodes, func(i, j int) bool {
		return episodes[i].Number < episodes[j].Number
	})

}

func episodeNumber(name string) int {

	for _, pattern := range episodeNumberREs {
		if match := pattern.FindStringSubmatch(name); len(match) > 1 {
			number, _ := strconv.Atoi(match[1])
			return number
		}
	}

	return 0

}
