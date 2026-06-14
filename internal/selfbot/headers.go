package selfbot

import (
	"net/http"
	"os"
	"strings"
	"time"
)

const apiVersion = "9"

func discordBaseURL() string {

	return strings.TrimRight(os.Getenv("DISCORD_BASE_URL"), "/")

}

func discordAPIBase() string {

	return discordBaseURL() + "/api/v" + apiVersion

}

func restHeaders(props Properties) http.Header {

	return http.Header{

		"Accept": []string{"*/*"},
		"Accept-Language": []string{"en-US"},
		"Authorization": []string{props.authToken},
		"Origin": []string{discordBaseURL()},
		"Priority": []string{"u=1, i"},
		"Referer": []string{discordBaseURL() + "/channels/@me"},

		"Sec-CH-UA": []string{`"Not:A-Brand";v="24", "Chromium";v="134"`},
		"Sec-CH-UA-Mobile": []string{"?0"},
		"Sec-CH-UA-Platform": []string{`"Windows"`},
		"Sec-Fetch-Dest": []string{"empty"},
		"Sec-Fetch-Mode": []string{"cors"},
		"Sec-Fetch-Site": []string{"same-origin"},

		"User-Agent": []string{userAgent},
		"X-Debug-Options": []string{"bugReporterEnabled"},
		"X-Discord-Locale": []string{"en-US"},
		"X-Discord-Timezone": []string{localTimezone()},
		"X-Super-Properties": []string{props.superProperties()},

	}

}

func localTimezone() string {

	zone, _ := time.Now().Zone()

	if zone == "" {

		return "America/New_York"

	}

	return zone

}

func gatewayHeaders() http.Header {

	return http.Header{

		"User-Agent": []string{userAgent},
		"Origin": []string{discordBaseURL()},

	}

}
