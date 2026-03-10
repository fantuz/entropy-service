package diag

import "time"

type RateMeter struct {
	start time.Time
	Bytes int
	Rate  float64
	Meter int64
	rate *RateMeter
}

func NewRateMeter() *RateMeter {

	return &RateMeter{
		start: time.Now(),
	}
}

func (r *RateMeter) Update(n int) {

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
}

func (r *RateMeter) RateMbps() float64 {
	return r.Rate / 1_000_000
}
