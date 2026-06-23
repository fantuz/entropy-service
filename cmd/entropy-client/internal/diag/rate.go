package diag

import "time"

type RateMeter struct {
	// Bytes int
	Rate  float64
	Meter int64
	// rate *RateMeter
	rate  float64
	last  time.Time
	bytes int
	total uint64
}

func (r *RateMeter) Bytes() int {
	return r.bytes
}

func NewRateMeter() *RateMeter {
	return &RateMeter{last: time.Now()}
}

func (r *RateMeter) Update(n int) {
	/*
		//delta := r.Bytes - n
		delta := r.Bytes - int(r.Meter)

		now := time.Now()
		elapsed := now.Sub(r.start).Seconds()

		if elapsed < 1 {
			return
		}

		//r.Rate = float64(r.Bytes*8) / elapsed
		r.Rate = float64(delta*8) / elapsed
		r.Bytes += n
		r.Meter = int64(r.Bytes -n)
		r.start = now
	*/
	now := time.Now()
	elapsed := now.Sub(r.last).Seconds()

	r.bytes += n
	r.total += uint64(n)

	if elapsed < 1 {
		return
	}

	r.rate = float64(r.bytes*8) / elapsed // bits/sec
	r.bytes = 0
	r.last = now
}

func (r *RateMeter) RateMbps() float64 { return r.rate / 1_000_000 }

func (r *RateMeter) Total() uint64 {
	return r.total
}
