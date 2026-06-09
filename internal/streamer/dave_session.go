package streamer

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/disgoorg/godave"
	"github.com/disgoorg/godave/libdave"
)

const (
	daveInitTransitionID         = 0
	daveDisabledProtocolVersion  = 0
	daveMLSNewGroupExpectedEpoch = 1
)

// daveSession wraps libdave for the voice-gateway DAVE handshake and media frame encryption.
// This implementation mirrors golibdave (github.com/disgoorg/godave/golibdave) with the addition
// of MediaReady gating and per-codec encryption helpers.
type daveSession struct {
	mu                            sync.Mutex // Serializes libdave access between the sender and the gateway read loop.
	selfUserID                    godave.UserID
	channelID                     godave.ChannelID
	logger                        *slog.Logger
	callbacks                     godave.Callbacks
	session                       *libdave.Session
	encryptor                     *libdave.Encryptor
	decryptors                    map[godave.UserID]*libdave.Decryptor
	preparedTransitions           map[uint16]uint16
	lastPreparedTransitionVersion uint16
	protocolVersion               uint16
}

func newDaveSession(userID string, callbacks godave.Callbacks) *daveSession {

	encryptor := libdave.NewEncryptor()
	encryptor.SetPassthroughMode(true)

	return &daveSession{
		selfUserID:          godave.UserID(userID),
		callbacks:           callbacks,
		logger:              slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})),
		session:             libdave.NewSession("", ""),
		encryptor:           encryptor,
		decryptors:          make(map[godave.UserID]*libdave.Decryptor),
		preparedTransitions: make(map[uint16]uint16),
	}

}

func (s *daveSession) MaxSupportedProtocolVersion() int {

	return int(libdave.MaxSupportedProtocolVersion())

}

func (s *daveSession) SetChannelID(channelID godave.ChannelID) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.channelID = channelID

}

// active reports whether encryption is engaged; callers must hold s.mu.
func (s *daveSession) active() bool {

	return s.protocolVersion > daveDisabledProtocolVersion && s.encryptor.HasKeyRatchet()

}

func (s *daveSession) Active() bool {

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.active()

}

func (s *daveSession) MediaReady() bool {

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.protocolVersion == daveDisabledProtocolVersion {
		return true
	}

	return s.active()

}

func (s *daveSession) AssignSsrcToCodec(ssrc uint32, codec godave.Codec) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.encryptor.AssignSsrcToCodec(ssrc, libdave.Codec(codec))

}

func (s *daveSession) EncryptAudio(ssrc uint32, frame []byte) ([]byte, error) {

	return s.encrypt(libdave.MediaTypeAudio, ssrc, frame)

}

func (s *daveSession) EncryptVideo(ssrc uint32, frame []byte) ([]byte, error) {

	return s.encrypt(libdave.MediaTypeVideo, ssrc, frame)

}

func (s *daveSession) AssignVideoSsrc(ssrc uint32) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.encryptor.AssignSsrcToCodec(ssrc, libdave.CodecH264)

}

func (s *daveSession) encrypt(mediaType libdave.MediaType, ssrc uint32, frame []byte) ([]byte, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active() {
		return frame, nil
	}

	out := make([]byte, s.encryptor.GetMaxCiphertextByteSize(mediaType, len(frame)))
	n, err := s.encryptor.Encrypt(mediaType, ssrc, frame, out)

	if err != nil {
		return nil, err
	}

	return out[:n], nil

}

func (s *daveSession) EncryptorStats() string {

	if s == nil || s.encryptor == nil {
		return "unavailable"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stats := s.encryptor.GetStats(libdave.MediaTypeAudio)

	return fmt.Sprintf("attempts=%d success=%d failure=%d missing_key=%d passthrough=%d", stats.EncryptAttempts, stats.EncryptSuccessCount, stats.EncryptFailureCount, stats.EncryptMissingKeyCount, stats.PassthroughCount)

}

func (s *daveSession) AddUser(userID godave.UserID) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.decryptors[userID] = libdave.NewDecryptor()
	s.setupKeyRatchetForUser(userID, s.lastPreparedTransitionVersion)

}

func (s *daveSession) RemoveUser(userID godave.UserID) {

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.decryptors, userID)

}

func (s *daveSession) OnSelectProtocolAck(protocolVersion uint16) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.protocolVersion = protocolVersion
	s.protocolInit(protocolVersion)

}

func (s *daveSession) OnDavePrepareTransition(transitionID uint16, protocolVersion uint16) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.prepareTransition(transitionID, protocolVersion)

	if transitionID != daveInitTransitionID {
		s.sendReadyForTransition(transitionID)
	}

}

func (s *daveSession) OnDaveExecuteTransition(transitionID uint16) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.executeTransition(transitionID)

}

func (s *daveSession) OnDavePrepareEpoch(epoch int, protocolVersion uint16) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.prepareEpoch(epoch, protocolVersion)

	if epoch == daveMLSNewGroupExpectedEpoch {
		s.sendMLSKeyPackage()
	}

}

func (s *daveSession) OnDaveMLSExternalSenderPackage(externalSenderPackage []byte) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.session.SetExternalSender(externalSenderPackage)

}

func (s *daveSession) OnDaveMLSProposals(proposals []byte) {

	s.mu.Lock()
	defer s.mu.Unlock()

	commitWelcome := s.session.ProcessProposals(proposals, s.recognizedUserIDs())

	if commitWelcome != nil {
		s.sendMLSCommitWelcome(commitWelcome)
	}

}

func (s *daveSession) OnDaveMLSPrepareCommitTransition(transitionID uint16, commitMessage []byte) {

	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.session.ProcessCommit(commitMessage)

	if res.IsIgnored() {
		return
	}

	if res.IsFailed() {
		s.sendInvalidCommitWelcome(transitionID)
		s.protocolInit(s.session.GetProtocolVersion())
		return
	}

	s.prepareTransition(transitionID, s.session.GetProtocolVersion())

	if transitionID != daveInitTransitionID {
		s.sendReadyForTransition(transitionID)
	}

}

func (s *daveSession) OnDaveMLSWelcome(transitionID uint16, welcomeMessage []byte) {

	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.session.ProcessWelcome(welcomeMessage, s.recognizedUserIDs())

	if res == nil {
		s.sendInvalidCommitWelcome(transitionID)
		s.sendMLSKeyPackage()
		return
	}

	s.prepareTransition(transitionID, s.session.GetProtocolVersion())

	if transitionID != daveInitTransitionID {
		s.sendReadyForTransition(transitionID)
	}

}

func (s *daveSession) recognizedUserIDs() []string {

	userIDs := make([]string, 0, len(s.decryptors)+1)

	userIDs = append(userIDs, string(s.selfUserID))

	for userID := range s.decryptors {
		userIDs = append(userIDs, string(userID))
	}

	return userIDs

}

func (s *daveSession) protocolInit(protocolVersion uint16) {

	if protocolVersion > daveDisabledProtocolVersion {
		s.prepareEpoch(daveMLSNewGroupExpectedEpoch, protocolVersion)
		s.sendMLSKeyPackage()
	} else {
		s.prepareTransition(daveInitTransitionID, protocolVersion)
		s.executeTransition(daveInitTransitionID)
	}

}

func (s *daveSession) prepareEpoch(epoch int, protocolVersion uint16) {

	if epoch != daveMLSNewGroupExpectedEpoch {
		return
	}

	s.session.Init(protocolVersion, uint64(s.channelID), string(s.selfUserID))

}

func (s *daveSession) executeTransition(transitionID uint16) {

	protocolVersion, ok := s.preparedTransitions[transitionID]

	if !ok {
		return
	}

	delete(s.preparedTransitions, transitionID)

	if protocolVersion == daveDisabledProtocolVersion {
		s.session.Reset()
	}

	s.setupKeyRatchetForUser(s.selfUserID, protocolVersion)

}

func (s *daveSession) prepareTransition(transitionID uint16, protocolVersion uint16) {

	for userID := range s.decryptors {
		s.setupKeyRatchetForUser(userID, protocolVersion)
	}

	if transitionID == daveInitTransitionID {
		s.setupKeyRatchetForUser(s.selfUserID, protocolVersion)
	} else {
		s.preparedTransitions[transitionID] = protocolVersion
	}

	s.lastPreparedTransitionVersion = protocolVersion
	s.protocolVersion = protocolVersion

}

func (s *daveSession) setupKeyRatchetForUser(userID godave.UserID, protocolVersion uint16) {

	disabled := protocolVersion == daveDisabledProtocolVersion

	if userID == s.selfUserID {
		s.encryptor.SetPassthroughMode(disabled)

		if !disabled {
			s.encryptor.SetKeyRatchet(s.session.GetKeyRatchet(string(userID)))
		}

		return
	}

	decryptor := s.decryptors[userID]
	decryptor.TransitionToPassthroughMode(disabled)

	if !disabled {
		decryptor.TransitionToKeyRatchet(s.session.GetKeyRatchet(string(userID)))
	}

}

func (s *daveSession) sendMLSKeyPackage() {

	if err := s.callbacks.SendMLSKeyPackage(s.session.GetMarshalledKeyPackage()); err != nil {
		s.logger.Error("failed to send MLS key package", slog.Any("err", err))
	}

}

func (s *daveSession) sendMLSCommitWelcome(message []byte) {

	if err := s.callbacks.SendMLSCommitWelcome(message); err != nil {
		s.logger.Error("failed to send MLS commit welcome", slog.Any("err", err))
	}

}

func (s *daveSession) sendReadyForTransition(transitionID uint16) {

	if err := s.callbacks.SendReadyForTransition(transitionID); err != nil {
		s.logger.Error("failed to send ready for transition", slog.Any("err", err))
	}

}

func (s *daveSession) sendInvalidCommitWelcome(transitionID uint16) {

	if err := s.callbacks.SendInvalidCommitWelcome(transitionID); err != nil {
		s.logger.Error("failed to send invalid commit welcome", slog.Any("err", err))
	}

}
