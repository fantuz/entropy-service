package diag

import "time"

type RateMeter struct {
	start time.Time
	bytes int
	rate  float64
	//rate *RateMeter
}

func NewRateMeter() *RateMeter {

	return &RateMeter{
		start: time.Now(),
	}
}

func (r *RateMeter) Update(n int) {

	delta := r.bytes - n
	r.bytes += n

	now := time.Now()
	elapsed := now.Sub(r.start).Seconds()

	if elapsed < 1 {
		return
	}

	r.rate = float64(delta*8) / elapsed

	//r.rate = float64(r.bytes*8) / elapsed

	//r.bytes = r.bytes
	r.bytes = 0
	r.start = now
}

func (r *RateMeter) RateMbps() float64 {
	return r.rate / 1_000_000
}
