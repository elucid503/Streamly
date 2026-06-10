package bot

import "github.com/bwmarrin/discordgo"

func ptrEmbeds(embeds []*discordgo.MessageEmbed) *[]*discordgo.MessageEmbed {

	return &embeds

}

func ptrComponents(components []discordgo.MessageComponent) *[]discordgo.MessageComponent {

	return &components

}
