package main

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
)

func incRNGBytes(n int) {
	atomic.AddUint64(&rngBytesGenerated, uint64(n))
}

func incReseed() {
	atomic.AddUint64(&rngReseeds, 1)
}

func incBuffer() {
	atomic.AddUint64(&rngBytesBuffered, 1)
}

func incTestA(m int) {
	atomic.AddUint64(&rngBytesTestA, uint64(m))
}

func incTestB(y int) {
	atomic.AddUint64(&rngBytesTestB, uint64(y))
}

func incHTTP() {
	atomic.AddUint64(&httpRequests, 1)
}
