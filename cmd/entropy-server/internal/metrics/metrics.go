package metrics

import (
	"sync/atomic"
)

var (
	rngBytesGenerated uint64
	rngReseeds        uint64
	rngBytesBuffered  uint64
	rngBytesTestA     uint64
	rngBytesTestB     uint64
	httpRequests      uint64
	wsPayloads        uint64
)


func AddBufferedBytes(n uint64) {
	atomic.AddUint64(&rngBytesBuffered, n)
}

func AddReseeds(n int) {
	atomic.AddUint64(&rngReseeds, uint64(n))
}

func AddBytesGenerated(n int) {
	atomic.AddUint64(&rngBytesGenerated, uint64(n))
}

func AddHttpRequests(n int) {
	atomic.AddUint64(&httpRequests, uint64(n))
}

func AddWSPayloads(n int) {
	atomic.AddUint64(&wsPayloads, uint64(n))
}

func AddTestA(n int) {
	atomic.AddUint64(&rngBytesTestA, uint64(n))
}

func AddTestB(n int) {
	atomic.AddUint64(&rngBytesTestB, uint64(n))
}

func BufferedBytes() uint64 {
    return atomic.LoadUint64(&rngBytesBuffered)
}

func Reseeds() uint64 {
    return atomic.LoadUint64(&rngReseeds)
}

func BytesGenerated() uint64 {
    return atomic.LoadUint64(&rngBytesGenerated)
}

func NumHttpRequests() uint64 {
    return atomic.LoadUint64(&httpRequests)
}

func NumWSPayloads() uint64 {
    return atomic.LoadUint64(&wsPayloads)
}

func TestA() uint64 {
    return atomic.LoadUint64(&rngBytesTestA)
}

func TestB() uint64 {
    return atomic.LoadUint64(&rngBytesTestB)
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

