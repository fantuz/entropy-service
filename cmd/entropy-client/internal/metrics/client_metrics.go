// internal/metrics/client_metrics.go
package metrics

import "sync/atomic"

var clientBytesReceived uint64
var clientWSSMessages uint64

func AddClientBytesReceived(n uint64) { atomic.AddUint64(&clientBytesReceived, n) }
func ClientBytesReceived() uint64     { return atomic.LoadUint64(&clientBytesReceived) }

func IncClientWSSMessages()     { atomic.AddUint64(&clientWSSMessages, 1) }
func ClientWSSMessages() uint64 { return atomic.LoadUint64(&clientWSSMessages) }
