package bot

import (
	"log"
	"sync"

	"streamly/internal/captions"
	"streamly/internal/config"
	"streamly/internal/db"
	"streamly/internal/febapi"
	"streamly/internal/introdb"
	"streamly/internal/media"
	"streamly/internal/pool"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {

	Session *discordgo.Session
	Resolver *media.Resolver
	Pool *pool.Pool
	DB *db.Client
	IntroDB *introdb.CachedClient
	Captions *captions.Fetcher

	autoNext map[string]*autoNextState
	autoNextMu sync.Mutex
	autoNextGen uint64

}

func New(resolver *media.Resolver, p *pool.Pool, database *db.Client) (*Bot, error) {

	session, err := discordgo.New("Bot " + config.App.DiscordToken)

	if err != nil {

		return nil, err

	}

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates

	subtitleProviders := []captions.RemoteProvider{

		captions.NewSubDLClient(captions.SubDLOptions{APIKey: config.App.SubDLAPIKey}),
	}

	bot := &Bot{

		Session: session,
		Resolver: resolver,
		Pool: p,
		DB: database,
		IntroDB: introdb.NewCachedClient(introdb.NewClient(introdb.ClientOptions{APIKey: config.App.IntroDBAPIKey})),
		Captions: captions.NewFetcher(febapi.NewFebboxClient(febapi.FebboxOptions{Cookie: config.App.FebboxCookie}), subtitleProviders, config.FebboxStreamHeaders()),

		autoNext: make(map[string]*autoNextState),
	}

	session.AddHandler(bot.onInteraction)

	return bot, nil

}

func (b *Bot) Start() error {

	if err := b.Session.Open(); err != nil {

		return err

	}

	log.Printf("[startup] logged in as %s.", b.Session.State.User.Username)

	b.Resolver.Warmup()

	return b.registerCommands()

}

func (b *Bot) registerCommands() error {

	commands := []*discordgo.ApplicationCommand{

		{Name: "stream", Description: "Stream a movie, TV show, or live TV channel in your call.", Options: []*discordgo.ApplicationCommandOption{

			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Search by movie name, show name, or TV channel.", Required: true, Autocomplete: true},

		}},

		{Name: "pause", Description: "Pause the active stream in your call."},
		{Name: "resume", Description: "Resume the paused stream in your call."},

		{Name: "seek", Description: "Jump to a position in the active stream.", Options: []*discordgo.ApplicationCommandOption{

			{Type: discordgo.ApplicationCommandOptionString, Name: "position", Description: "Enter a time to jump to (eg: 4:20 or +30).", Required: true, Autocomplete: true},

		}},

		{Name: "skip-intro", Description: "Skip past the intro in the active stream."},

		{Name: "subtitles", Description: "Turn English subtitles on or off for the active stream.", Options: []*discordgo.ApplicationCommandOption{

			{Type: discordgo.ApplicationCommandOptionString, Name: "mode", Description: "Whether subtitles should be shown on stream.", Required: true, Choices: []*discordgo.ApplicationCommandOptionChoice{

				{Name: "Enabled", Value: subtitleModeEnabled},
				{Name: "Disabled", Value: subtitleModeDisabled},

			}},

		}},

		{Name: "stop", Description: "Stop the active stream in your call."},
		{Name: "stats", Description: "Show stats for the active stream in your call."},
		{Name: "channels", Description: "Browse live TV channels and pick one to watch."},
		{Name: "top", Description: "See trending movies and TV shows to watch."},
		{Name: "now", Description: "See what is streaming in this server."},

		{Name: "configure", Description: "Configure server streaming.", Options: []*discordgo.ApplicationCommandOption{

			{Type: discordgo.ApplicationCommandOptionString, Name: "key", Description: "The server key.", Required: true},

		}},

	}

	guildID := config.App.GuildID

	if _, err := b.Session.ApplicationCommandBulkOverwrite(b.Session.State.User.ID, guildID, commands); err != nil {

		return err

	}

	scope := "globally"

	if config.App.GuildID != "" {

		scope = "to guild " + config.App.GuildID

	}

	log.Printf("[startup] %d commands registered %s.", len(commands), scope)

	return nil

}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {

	switch i.Type {

	case discordgo.InteractionApplicationCommand:

		b.onCommand(s, i)

	case discordgo.InteractionApplicationCommandAutocomplete:

		b.onAutocomplete(s, i)

	case discordgo.InteractionMessageComponent:

		b.onComponent(s, i)

	}

}

func (b *Bot) onCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {

	switch i.ApplicationCommandData().Name {

	case "stream":

		b.handleStream(s, i)

	case "pause":

		b.handleControl(s, i, "pause")

	case "resume":

		b.handleControl(s, i, "resume")

	case "seek":

		b.handleSeek(s, i)

	case "skip-intro":

		b.handleSkipIntro(s, i)

	case "subtitles":

		b.handleSubtitles(s, i)

	case "stop":

		b.handleControl(s, i, "stop")

	case "stats":

		b.handleStats(s, i)

	case "channels":

		b.handleChannels(s, i)

	case "top":

		b.handleTop(s, i)

	case "now":

		b.handleNow(s, i)

	case "configure":

		b.handleConfigure(s, i)

	default:

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Unknown command."}})

	}

}

func (b *Bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {

	customID := i.MessageComponentData().CustomID
	parts := splitCustomID(customID)

	if len(parts) < 2 {

		return

	}

	switch parts[0] {

	case "stream":
		switch parts[1] {

		case "stop":

			b.handleStopButton(s, i, parts)

		case "pause", "resume":

			b.handleToggleButton(s, i, parts)

		case "season", "episode":

			b.handleSelect(s, i, parts)

		}

	case "channels":

		b.handleChannelsComponent(s, i, parts)

	case "autonext":

		b.handleAutoNextButton(s, i, parts)

	}

}

func splitCustomID(customID string) []string {

	var parts []string
	start := 0

	for index := 0; index < len(customID); index++ {

		if customID[index] == ':' {

			parts = append(parts, customID[start:index])
			start = index + 1

		}

	}

	parts = append(parts, customID[start:])

	return parts

}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{

		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral},

	})

}

func respondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{

		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},

	})

}

func editMessage(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.WebhookEdit) {

	_, _ = s.InteractionResponseEdit(i.Interaction, data)

}

func deferReply(s *discordgo.Session, i *discordgo.InteractionCreate) error {

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})

}
