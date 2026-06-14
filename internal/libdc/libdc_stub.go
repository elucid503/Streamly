//go:build !cgo

package libdc

import "errors"

var ErrUnavailable = errors.New("libdatachannel requires CGO_ENABLED=1")

type DescriptionHandler func(sdp string, offer bool)

type Peer struct { }

func NewPeer(string) (*Peer, error) {

	return nil, ErrUnavailable

}

func (p *Peer) Destroy() { }

func (p *Peer) OnLocalDescription(DescriptionHandler) { }

func (p *Peer) AddAudioTrack(uint32, int) error {

	return ErrUnavailable

}

func (p *Peer) AddVideoTrack(uint32, uint32, int, int) error {

	return ErrUnavailable

}

func (p *Peer) CreateOffer() { }

func (p *Peer) SetRemoteAnswer(string) error {

	return ErrUnavailable

}

func (p *Peer) SetupPacketizers(uint32, int, uint32, int) error {

	return ErrUnavailable

}

func (p *Peer) Connected() bool {

	return false

}

func (p *Peer) AudioOpen() bool {

	return false

}

func (p *Peer) VideoOpen() bool {

	return false

}

func (p *Peer) MediaReady() bool {

	return false

}

func (p *Peer) SendAudio([]byte, float64) { }

func (p *Peer) AdvanceAudio(float64) { }

func (p *Peer) AdvanceVideo(float64) { }

func (p *Peer) SendVideo([]byte, float64) { }
