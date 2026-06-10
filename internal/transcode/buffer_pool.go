package transcode

import (
	"sync"
	"unsafe"
)

// packetBufPool recycles encoded packet backing stores to cut heap churn during long streams.
var packetBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 256*1024)
		return &buf
	},
}

// packetFromC copies one elementary packet out of libav memory into a pooled Go buffer.
func packetFromC(data unsafe.Pointer, length int) Packet {

	n := length

	if n <= 0 {
		return Packet{}
	}

	slot := packetBufPool.Get().(*[]byte)
	buf := *slot

	if cap(buf) < n {
		buf = make([]byte, n)
	} else {
		buf = buf[:n]
	}

	copy(buf, unsafe.Slice((*byte)(data), n))

	return Packet{
		Data:    buf,
		dataRef: slot,
	}

}

// ReleasePacket returns a pooled packet buffer after the consumer is done with it.
func ReleasePacket(packet Packet) {

	if packet.dataRef == nil {
		return
	}

	*packet.dataRef = packet.Data[:0]
	packetBufPool.Put(packet.dataRef)

}