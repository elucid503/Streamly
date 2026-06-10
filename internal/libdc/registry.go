//go:build cgo

package libdc

import (
	"sync"
	"sync/atomic"
)

var (
	peerSeq  atomic.Uint64
	peerByID sync.Map
)

func registerPeer(peer *Peer) uint64 {

	id := peerSeq.Add(1)
	peer.id = id
	peerByID.Store(id, peer)

	return id

}

func unregisterPeer(id uint64) {

	if id == 0 {
		return
	}

	peerByID.Delete(id)

}

func lookupPeer(id uint64) (*Peer, bool) {

	value, ok := peerByID.Load(id)

	if !ok {
		return nil, false
	}

	peer, ok := value.(*Peer)

	return peer, ok

}
