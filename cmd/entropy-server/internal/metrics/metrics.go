package metrics

import (
	"sync/atomic"
)

var (
	rngBytesGenerated atomic.Uint64
	rngReseeds        atomic.Uint64
	rngBytesBuffered  atomic.Uint64
	rngBytesTestA     atomic.Uint64
	rngBytesTestB     atomic.Uint64
	httpRequests      atomic.Uint64
	wsPayloads        atomic.Uint64
	httpBodyBytes     atomic.Uint64
	wsPayloadBytes    atomic.Uint64
	wsSessions        atomic.Uint64
	tcpConnections    atomic.Uint64
)

// ServingTotals is a process-lifetime snapshot. Requests include WS handshakes;
// sessions count successful upgrades, and payload bytes exclude protocol framing.
type ServingTotals struct {
	HTTPBodyBytes  uint64
	WSPayloadBytes uint64
	TCPConnections uint64
	HTTPRequests   uint64
	WSSessions     uint64
	WSMessages     uint64
	GeneratedBytes uint64
}

func AddHTTPBodyBytes(n uint64)  { httpBodyBytes.Add(n) }
func AddWSPayloadBytes(n uint64) { wsPayloadBytes.Add(n) }
func AddWSSession()              { wsSessions.Add(1) }
func AddTCPConnection()          { tcpConnections.Add(1) }

func ServingSnapshot() ServingTotals {
	return ServingTotals{
		HTTPBodyBytes: httpBodyBytes.Load(), WSPayloadBytes: wsPayloadBytes.Load(),
		TCPConnections: tcpConnections.Load(), HTTPRequests: httpRequests.Load(),
		WSSessions: wsSessions.Load(), WSMessages: wsPayloads.Load(),
		GeneratedBytes: rngBytesGenerated.Load(),
	}
}

func AddBufferedBytes(n uint64) {
	rngBytesBuffered.Add(n)
}

func AddReseeds(n int) {
	rngReseeds.Add(uint64(n))
}

func AddBytesGenerated(n int) {
	rngBytesGenerated.Add(uint64(n))
}

func AddHTTPRequests(n int) {
	httpRequests.Add(uint64(n))
}

func AddWSPayloads(n int) {
	wsPayloads.Add(uint64(n))
}

func AddTestA(n int) {
	rngBytesTestA.Add(uint64(n))
}

func AddTestB(n int) {
	rngBytesTestB.Add(uint64(n))
}

func BufferedBytes() uint64 {
	return rngBytesBuffered.Load()
}

func Reseeds() uint64 {
	return rngReseeds.Load()
}

func BytesGenerated() uint64 {
	return rngBytesGenerated.Load()
}

func NumHTTPRequests() uint64 {
	return httpRequests.Load()
}

func NumWSPayloads() uint64 {
	return wsPayloads.Load()
}

func TestA() uint64 {
	return rngBytesTestA.Load()
}

func TestB() uint64 {
	return rngBytesTestB.Load()
}

/*
func incReseed() {
	atomic.AddUint64(&rngReseeds, 1)
}

func incRNGBytes(n int) {
	atomic.AddUint64(&rngBytesGenerated, uint64(n))
}

func incBuffer() {
	atomic.AddUint64(&rngBytesBuffered, 1)
}

func incHTTP() {
	atomic.AddUint64(&httpRequests, 1)
}

func incWSS() {
	atomic.AddUint64(&wsPayloads, 1)
}
*/
