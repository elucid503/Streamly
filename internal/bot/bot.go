package bot

import (
	"log"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/config"
	"streamly/internal/db"
	"streamly/internal/media"
	"streamly/internal/pool"
)

// Bot is the real Discord application that registers slash commands.
type Bot struct {
	Session  *discordgo.Session
	Resolver *media.Resolver
	Pool     *pool.Pool
	DB       *db.Client
}

func New(resolver *media.Resolver, p *pool.Pool, database *db.Client) (*Bot, error) {

	session, err := discordgo.New("Bot " + config.App.DiscordToken)

	if err != nil {
		return nil, err
	}

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates

	bot := &Bot{Session: session, Resolver: resolver, Pool: p, DB: database}

	session.AddHandler(bot.onInteraction)

	return bot, nil

}

func (b *Bot) Start() error {

	if err := b.Session.Open(); err != nil {
		return err
	}

	return b.registerCommands()

}

func (b *Bot) registerCommands() error {

	commands := []*discordgo.ApplicationCommand{
		{Name: "stream", Description: "Stream a movie, TV show, or live TV channel in your call.", Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Search by movie name, show name, or TV channel.", Required: true, Autocomplete: true},
		}},
		{Name: "pause", Description: "Pause the active stream in your call."},
		{Name: "resume", Description: "Resume the paused stream in your call."},
		{Name: "stop", Description: "Stop the active stream in your call."},
		{Name: "stats", Description: "Show stats for the active stream in your call."},
		{Name: "channels", Description: "Browse live TV channels and pick one to watch."},
		{Name: "top", Description: "See trending movies and TV shows to watch."},
		{Name: "now", Description: "See what is streaming in this server."},
	}

	for _, command := range commands {

		if config.App.GuildID != "" {
			_, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, config.App.GuildID, command)
			if err != nil {
				return err
			}
		} else {
			_, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, "", command)
			if err != nil {
				return err
			}
		}

	}

	scope := "globally"

	if config.App.GuildID != "" {
		scope = "to guild " + config.App.GuildID
	}

	log.Printf("[startup] logged in as %s; %d commands registered %s.", b.Session.State.User.Username, len(commands), scope)

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
