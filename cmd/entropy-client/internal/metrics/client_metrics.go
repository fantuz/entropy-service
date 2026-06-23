// internal/metrics/client_metrics.go
package metrics

import "sync/atomic"

var (
	clientBytesReceived atomic.Uint64
	clientWSSMessages   atomic.Uint64
)

func AddClientBytesReceived(n uint64) { clientBytesReceived.Add(n) }
func ClientBytesReceived() uint64     { return clientBytesReceived.Load() }

func IncClientWSSMessages()     { clientWSSMessages.Add(1) }
func ClientWSSMessages() uint64 { return clientWSSMessages.Load() }
