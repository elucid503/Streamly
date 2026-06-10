package bot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"streamly/internal/pool"
)

var seekPositionChoices = []*discordgo.ApplicationCommandOptionChoice{
	{Name: "-5 Min", Value: "-300"},
	{Name: "-1 Min", Value: "-60"},
	{Name: "-30s", Value: "-30"},
	{Name: "-15s", Value: "-15"},
	{Name: "+15s", Value: "+15"},
	{Name: "+30s", Value: "+30"},
	{Name: "+1 Min", Value: "+60"},
	{Name: "+5 Min", Value: "+300"},
}

func (b *Bot) handleSeek(s *discordgo.Session, i *discordgo.InteractionCreate) {

	session := activeSession(s, i, b.Pool)

	if session == nil || !session.Busy {
		respondEmbed(s, i, simpleEmbed("Stream Control", "No Active Stream", "No active stream was found for this server."))
		return
	}

	if session.Live() {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Live TV", "Live streams cannot be seeked."))
		return
	}

	input := optionString(i, "position")

	if input == skipIntroValue {
		b.executeSkipIntro(s, i, session)
		return
	}

	current := time.Duration(b.Pool.Stats(session).PositionMs) * time.Millisecond

	target, err := parseSeekTarget(input, current)

	if err != nil {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Invalid Position", "Use a time like `12:30`, `1:05:00`, `95`, or a jump like `+30` / `-90`."))
		return
	}

	actual, err := b.Pool.Seek(session, target)

	if errors.Is(err, pool.ErrUnseekable) {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Seek Unavailable", "This stream's source doesn't support seeking."))
		return
	}

	if err != nil {
		respondEmbed(s, i, controlEmbed(b.Pool, session, "Seek Failed", "Couldn't seek this stream right now."))
		return
	}

	respondEmbed(s, i, controlEmbed(b.Pool, session, "Seeking", fmt.Sprintf("Jumping to %s.", formatDuration(actual.Milliseconds()))))

}

// parseSeekTarget turns user input into an absolute position. A leading + or -
// jumps relative to the current position; otherwise the value is absolute.
// Values are plain seconds or clock times (mm:ss, hh:mm:ss).
func parseSeekTarget(input string, current time.Duration) (time.Duration, error) {

	input = strings.TrimSpace(input)

	if input == "" {
		return 0, errors.New("empty position")
	}

	sign := 0

	switch input[0] {
	case '+':
		sign = 1
		input = input[1:]
	case '-':
		sign = -1
		input = input[1:]
	}

	value, err := parseClock(input)

	if err != nil {
		return 0, err
	}

	if sign == 0 {
		return value, nil
	}

	return current + time.Duration(sign)*value, nil

}

func parseClock(text string) (time.Duration, error) {

	parts := strings.Split(strings.TrimSpace(text), ":")

	if len(parts) == 0 || len(parts) > 3 {
		return 0, errors.New("invalid time")
	}

	total := 0

	for _, part := range parts {

		number, err := strconv.Atoi(strings.TrimSpace(part))

		if err != nil || number < 0 {
			return 0, errors.New("invalid time")
		}

		total = total*60 + number

	}

	return time.Duration(total) * time.Second, nil

}
