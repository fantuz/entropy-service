package main

import "io"

func probePlatformStatus(source io.ReadCloser) (map[string]uint32, error) {
	if status, ok := source.(interface {
		ProbeStatus() (map[string]uint32, error)
	}); ok {
		return status.ProbeStatus()
	}

	return probeNativeStatus(source)
}
