#include "native/streamly_dc.h"

#include <chrono>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <mutex>
#include <string>

#include "rtc/rtc.hpp"

namespace {

constexpr uint8_t kPlayoutDelayExtID = 5;
constexpr double kVideoPacingMbps = 25.0 * 1000.0 * 1000.0;
constexpr int kVideoPacingIntervalMs = 1;

bool disableVideoPacing() {

	const char *value = std::getenv("STREAMLY_DISABLE_VIDEO_PACING");

	return value != nullptr && (std::strcmp(value, "1") == 0 || std::strcmp(value, "true") == 0 || std::strcmp(value, "TRUE") == 0);

}

struct TrackState {
	std::shared_ptr<rtc::Track> track;
	std::shared_ptr<rtc::RtpPacketizationConfig> rtpConfig;
};

struct StreamlyPeerImpl {
	std::shared_ptr<rtc::PeerConnection> pc;
	TrackState audio;
	TrackState video;
	StreamlyDescriptionCallback onDescription = nullptr;
	uint64_t descriptionUser = 0;
	std::mutex mu;
	bool closed = false;
};

struct StreamlyPeerHandle {
	std::shared_ptr<StreamlyPeerImpl> impl;
};

StreamlyPeerImpl *asImpl(StreamlyPeer *peer) {

	auto handle = reinterpret_cast<StreamlyPeerHandle *>(peer);

	if (handle == nullptr || handle->impl == nullptr) {
		return nullptr;
	}

	return handle->impl.get();

}

void clearCallbacks(StreamlyPeerImpl *impl) {

	if (impl == nullptr) {
		return;
	}

	impl->onDescription = nullptr;
	impl->descriptionUser = 0;

}

void addDiscordAudioExtensions(rtc::Description::Audio &audio) {

	audio.addExtMap(rtc::Description::Entry::ExtMap(1, "urn:ietf:params:rtp-hdrext:ssrc-audio-level"));
	audio.addExtMap(rtc::Description::Entry::ExtMap(3, "http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01"));
	audio.addExtMap(rtc::Description::Entry::ExtMap(5, "http://www.webrtc.org/experiments/rtp-hdrext/playout-delay"));

}

void addDiscordVideoExtensions(rtc::Description::Video &video) {

	video.addExtMap(rtc::Description::Entry::ExtMap(2, "http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time"));
	video.addExtMap(rtc::Description::Entry::ExtMap(3, "http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01"));
	video.addExtMap(rtc::Description::Entry::ExtMap(14, "urn:ietf:params:rtp-hdrext:toffset"));
	video.addExtMap(rtc::Description::Entry::ExtMap(13, "urn:3gpp:video-orientation"));
	video.addExtMap(rtc::Description::Entry::ExtMap(5, "http://www.webrtc.org/experiments/rtp-hdrext/playout-delay"));
	video.addExtMap(rtc::Description::Entry::ExtMap(10, "urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id"));
	video.addExtMap(rtc::Description::Entry::ExtMap(11, "urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id"));

}

void setupAudioPacketizer(StreamlyPeerImpl *peer, uint32_t ssrc, int payloadType, uint16_t playoutMin, uint16_t playoutMax) {

	auto rtpConfig = std::make_shared<rtc::RtpPacketizationConfig>(ssrc, "streamly", static_cast<uint8_t>(payloadType), 48000);
	rtpConfig->playoutDelayId = kPlayoutDelayExtID;
	rtpConfig->playoutDelayMin = playoutMin;
	rtpConfig->playoutDelayMax = playoutMax;

	auto packetizer = std::make_shared<rtc::RtpPacketizer>(rtpConfig);
	packetizer->addToChain(std::make_shared<rtc::RtcpSrReporter>(rtpConfig));
	packetizer->addToChain(std::make_shared<rtc::RtcpNackResponder>());

	peer->audio.rtpConfig = rtpConfig;
	peer->audio.track->setMediaHandler(packetizer);

}

void setupVideoPacketizer(StreamlyPeerImpl *peer, uint32_t ssrc, int payloadType, uint16_t playoutMin, uint16_t playoutMax) {

	auto rtpConfig = std::make_shared<rtc::RtpPacketizationConfig>(ssrc, "streamly", static_cast<uint8_t>(payloadType), rtc::RtpPacketizer::VideoClockRate);
	rtpConfig->playoutDelayId = kPlayoutDelayExtID;
	rtpConfig->playoutDelayMin = playoutMin;
	rtpConfig->playoutDelayMax = playoutMax;

	auto packetizer = std::make_shared<rtc::H264RtpPacketizer>(rtc::NalUnit::Separator::StartSequence, rtpConfig);
	packetizer->addToChain(std::make_shared<rtc::RtcpSrReporter>(rtpConfig));
	packetizer->addToChain(std::make_shared<rtc::RtcpNackResponder>());

	if (!disableVideoPacing()) {
		packetizer->addToChain(std::make_shared<rtc::PacingHandler>(kVideoPacingMbps, std::chrono::milliseconds(kVideoPacingIntervalMs)));
	}

	peer->video.rtpConfig = rtpConfig;
	peer->video.track->setMediaHandler(packetizer);

}

// sendTrackFrame fires one frame at the track when the PeerConnection is connected, matching
// discord-video-stream: it does not gate on track->isOpen() (Discord answers with a=inactive) nor
// surface backpressure (the caller paces frames). Returns 1 when the send was attempted, 0 otherwise.
int sendTrackFrame(const std::shared_ptr<rtc::Track> &track, const std::shared_ptr<rtc::PeerConnection> &pc, const uint8_t *data, size_t len) {

	if (track == nullptr || pc == nullptr || data == nullptr || len == 0) {
		return 0;
	}

	if (pc->state() != rtc::PeerConnection::State::Connected) {
		return 0;
	}

	try {
		track->send(reinterpret_cast<const rtc::byte *>(data), len);
	} catch (const std::exception &) {
	}

	return 1;

}

} // namespace

extern "C" {

StreamlyPeer *streamly_peer_create(const char *stun_url) {

	try {

		rtc::Configuration config;

		if (stun_url != nullptr && stun_url[0] != '\0') {
			config.iceServers.emplace_back(stun_url);
		}

		auto impl = std::make_shared<StreamlyPeerImpl>();
		impl->pc = std::make_shared<rtc::PeerConnection>(config);

		impl->pc->onLocalDescription([impl](rtc::Description description) {

			if (impl->closed || impl->onDescription == nullptr) {
				return;
			}

			const std::string sdp = std::string(description);
			const int type = description.type() == rtc::Description::Type::Answer ? STREAMLY_DESC_ANSWER : STREAMLY_DESC_OFFER;
			impl->onDescription(impl->descriptionUser, sdp.c_str(), type);

		});

		auto handle = new StreamlyPeerHandle();
		handle->impl = std::move(impl);

		return reinterpret_cast<StreamlyPeer *>(handle);

	} catch (...) {
		return nullptr;
	}

}

void streamly_peer_destroy(StreamlyPeer *peer) {

	if (peer == nullptr) {
		return;
	}

	streamly_peer_close(peer);
	delete reinterpret_cast<StreamlyPeerHandle *>(peer);

}

void streamly_peer_close(StreamlyPeer *peer) {

	auto impl = asImpl(peer);

	if (impl == nullptr) {
		return;
	}

	std::lock_guard<std::mutex> lock(impl->mu);

	if (impl->closed) {
		return;
	}

	impl->closed = true;
	clearCallbacks(impl);

	// Match discord-video-stream's teardown: close the PeerConnection (which closes its tracks and
	// stops the media-handler threads) and let RAII drop the rest. Closing tracks separately as well
	// races those handler threads during destruction.
	if (impl->pc != nullptr) {
		impl->pc->close();
		impl->pc.reset();
	}

	impl->audio.track.reset();
	impl->audio.rtpConfig.reset();
	impl->video.track.reset();
	impl->video.rtpConfig.reset();

}

void streamly_peer_on_local_description(StreamlyPeer *peer, StreamlyDescriptionCallback cb, uint64_t user) {

	auto impl = asImpl(peer);

	if (impl == nullptr) {
		return;
	}

	impl->onDescription = cb;
	impl->descriptionUser = user;

}

int streamly_peer_add_audio(StreamlyPeer *peer, uint32_t ssrc, int payload_type) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->pc == nullptr) {
		return 0;
	}

	try {

		rtc::Description::Audio audio("0", rtc::Description::Direction::SendRecv);
		addDiscordAudioExtensions(audio);
		audio.addOpusCodec(payload_type);
		audio.addSSRC(ssrc, "streamly");

		impl->audio.track = impl->pc->addTrack(audio);

		return impl->audio.track != nullptr;

	} catch (...) {
		return 0;
	}

}

int streamly_peer_add_video(StreamlyPeer *peer, uint32_t ssrc, uint32_t rtx_ssrc, int payload_type, int rtx_payload_type) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->pc == nullptr) {
		return 0;
	}

	try {

		rtc::Description::Video video("1", rtc::Description::Direction::SendRecv);
		addDiscordVideoExtensions(video);
		video.addH264Codec(payload_type);
		video.addRtxCodec(rtx_payload_type, payload_type, rtc::RtpPacketizer::VideoClockRate);
		video.addSSRC(ssrc, "streamly");

		impl->video.track = impl->pc->addTrack(video);

		return impl->video.track != nullptr;

	} catch (...) {
		return 0;
	}

}

void streamly_peer_create_offer(StreamlyPeer *peer) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->pc == nullptr) {
		return;
	}

	impl->pc->setLocalDescription(rtc::Description::Type::Offer);

}

int streamly_peer_set_remote_answer(StreamlyPeer *peer, const char *sdp) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->pc == nullptr || sdp == nullptr) {
		return 0;
	}

	try {

		impl->pc->setRemoteDescription(rtc::Description(sdp, rtc::Description::Type::Answer));
		return 1;

	} catch (...) {
		return 0;
	}

}

int streamly_peer_connected(StreamlyPeer *peer) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->pc == nullptr || impl->closed) {
		return 0;
	}

	return impl->pc->state() == rtc::PeerConnection::State::Connected ? 1 : 0;

}

int streamly_peer_media_ready(StreamlyPeer *peer) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->closed || impl->pc == nullptr) {
		return 0;
	}

	if (impl->pc->state() != rtc::PeerConnection::State::Connected) {
		return 0;
	}

	if (impl->audio.track == nullptr || impl->video.track == nullptr) {
		return 0;
	}

	return impl->audio.track->isOpen() && impl->video.track->isOpen() ? 1 : 0;

}

int streamly_peer_setup_audio_packetizer(StreamlyPeer *peer, uint32_t ssrc, int payload_type, uint16_t playout_min, uint16_t playout_max) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->audio.track == nullptr) {
		return 0;
	}

	try {

		setupAudioPacketizer(impl, ssrc, payload_type, playout_min, playout_max);
		return 1;

	} catch (...) {
		return 0;
	}

}

int streamly_peer_setup_video_packetizer(StreamlyPeer *peer, uint32_t ssrc, int payload_type, uint16_t playout_min, uint16_t playout_max) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->video.track == nullptr) {
		return 0;
	}

	try {

		setupVideoPacketizer(impl, ssrc, payload_type, playout_min, playout_max);
		return 1;

	} catch (...) {
		return 0;
	}

}

int streamly_peer_send_audio(StreamlyPeer *peer, const uint8_t *data, size_t len) {

	auto impl = asImpl(peer);

	if (impl == nullptr || data == nullptr || len == 0) {
		return 0;
	}

	std::lock_guard<std::mutex> lock(impl->mu);

	if (impl->closed || impl->audio.track == nullptr || impl->audio.rtpConfig == nullptr) {
		return 0;
	}

	return sendTrackFrame(impl->audio.track, impl->pc, data, len);

}

int streamly_peer_send_video(StreamlyPeer *peer, const uint8_t *data, size_t len) {

	auto impl = asImpl(peer);

	if (impl == nullptr || data == nullptr || len == 0) {
		return 0;
	}

	std::lock_guard<std::mutex> lock(impl->mu);

	if (impl->closed || impl->video.track == nullptr || impl->video.rtpConfig == nullptr) {
		return 0;
	}

	return sendTrackFrame(impl->video.track, impl->pc, data, len);

}

void streamly_peer_advance_audio_timestamp(StreamlyPeer *peer, uint32_t clock_rate, double duration_ms) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->audio.rtpConfig == nullptr || clock_rate == 0) {
		return;
	}

	impl->audio.rtpConfig->timestamp += static_cast<uint32_t>((duration_ms * clock_rate / 1000.0) + 0.5);

}

void streamly_peer_advance_video_timestamp(StreamlyPeer *peer, uint32_t clock_rate, double duration_ms) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->video.rtpConfig == nullptr || clock_rate == 0) {
		return;
	}

	impl->video.rtpConfig->timestamp += static_cast<uint32_t>((duration_ms * clock_rate / 1000.0) + 0.5);

}

int streamly_peer_audio_open(StreamlyPeer *peer) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->audio.track == nullptr) {
		return 0;
	}

	return impl->audio.track->isOpen() ? 1 : 0;

}

int streamly_peer_video_open(StreamlyPeer *peer) {

	auto impl = asImpl(peer);

	if (impl == nullptr || impl->video.track == nullptr) {
		return 0;
	}

	return impl->video.track->isOpen() ? 1 : 0;

}

} // extern "C"
