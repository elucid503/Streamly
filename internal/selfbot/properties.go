package selfbot

import (
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) discord/1.0.9210 Chrome/134.0.6998.205 Electron/35.3.0 Safari/537.36"

type Properties struct {

	authToken string`json:"-"` // Set only for REST; never marshaled to JSON.

	OS string`json:"os"`
	Browser string`json:"browser"`
	ReleaseChannel string`json:"release_channel"`
	ClientVersion string`json:"client_version"`
	OSVersion string`json:"os_version"`
	OSArch string`json:"os_arch"`
	AppArch string`json:"app_arch"`
	SystemLocale string`json:"system_locale"`

	HasClientMods bool`json:"has_client_mods"`
	ClientLaunchID string`json:"client_launch_id"`
	BrowserUserAgent string`json:"browser_user_agent"`
	BrowserVersion string`json:"browser_version"`
	OSSDKVersion string`json:"os_sdk_version"`
	ClientBuildNumber int`json:"client_build_number"`
	NativeBuildNumber int`json:"native_build_number"`
	ClientEventSource *string`json:"client_event_source"`

	LaunchSignature string`json:"launch_signature"`
	ClientHeartbeatSessionID string`json:"client_heartbeat_session_id"`
	ClientAppState string`json:"client_app_state"`
}

func newProperties() Properties {

	return Properties{

		OS: "Windows",
		Browser: "Discord Client",
		ReleaseChannel: "stable",
		ClientVersion: "1.0.9210",
		OSVersion: "10.0.19044",
		OSArch: "x64",
		AppArch: "x64",
		SystemLocale: "en-US",

		HasClientMods: false,
		ClientLaunchID: uuid.NewString(),
		BrowserUserAgent: userAgent,
		BrowserVersion: "35.3.0",
		OSSDKVersion: "19044",
		ClientBuildNumber: 455964,
		NativeBuildNumber: 69976,
		ClientEventSource: nil,

		LaunchSignature: uuid.NewString(),
		ClientHeartbeatSessionID: uuid.NewString(),
		ClientAppState: "focused",
	}

}

func (p Properties) superProperties() string {

	raw, _ := json.Marshal(p.public())

	return base64.StdEncoding.EncodeToString(raw)

}

func (p Properties) public() Properties {

	copy := p
	copy.authToken = ""

	return copy

}

func (p Properties) forIdentify(token string) identifyPayload {

	props := p.public()
	props.BrowserUserAgent = userAgent

	return newIdentifyPayload(token, props)

}

type identifyPayload struct {

	Token string`json:"token"`
	Capabilities int`json:"capabilities"`
	Properties Properties`json:"properties"`
	Compress bool`json:"compress"`

	Presence presenceUpdate`json:"presence"`
	ClientState clientState`json:"client_state"`

}

type presenceUpdate struct {

	Status string`json:"status"`
	Since int`json:"since"`
	Activities []interface{}`json:"activities"`
	AFK bool`json:"afk"`
}

type clientState struct {

	GuildVersions map[string]int`json:"guild_versions"`
}

func newIdentifyPayload(token string, props Properties) identifyPayload {

	return identifyPayload{

		Token: token,
		Capabilities: 0,
		Properties: props,
		Compress: false,
		Presence: presenceUpdate{

			Status: "online",
			Since: 0,
			Activities: []interface{}{},
			AFK: true,
		},
		ClientState: clientState{GuildVersions: map[string]int{}},
	}

}
