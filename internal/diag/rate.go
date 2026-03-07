package diag

import "time"

/*
type RateMeter struct {
	lastTime  time.Time
	lastBytes int
	rate      float64
}

func NewRateMeter() *RateMeter {
	return &RateMeter{
		lastTime: time.Now(),
	}
}

func (r *RateMeter) Update(bytes int) {

	now := time.Now()
	elapsed := now.Sub(r.lastTime).Seconds()

	if elapsed == 0 {
		return
	}

	delta := bytes - r.lastBytes

	r.rate = float64(delta*8) / elapsed

	r.lastBytes = bytes
	r.lastTime = now
}

*/

type RateMeter struct {
	start time.Time
	bytes int
	rate  float64
}

func NewRateMeter() *RateMeter {

	return &RateMeter{
		start: time.Now(),
	}
}

func (r *RateMeter) Update(n int) {

	r.bytes += n

	now := time.Now()
	elapsed := now.Sub(r.start).Seconds()

	if elapsed < 1 {
		return
	}

	r.rate = float64(r.bytes*8) / elapsed

	r.bytes = 0
	r.start = now
}

func (r *RateMeter) RateMbps() float64 {
	return r.rate / 1_000_000
}
