package client

import (
	//"context"
	"fmt"

	"github.com/fantuz/entropy-service/entropy-client/internal/stats"
)

func reportStats(prefix string, r stats.StreamResult) {

	fmt.Printf(
		"%s bytes=%d  H=%.4f  chi2p=%.6f  monobit=%.6f  serialR=%.6f\n",
		prefix,
		r.TotalBytes,
		r.Shannon,
		r.Chi2P,
		r.MonobitP,
		r.SerialR,
	)
}

