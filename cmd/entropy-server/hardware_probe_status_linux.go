package main

import (
	"io"
	"os"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
)

func probeNativeStatus(source io.ReadCloser) (map[string]uint32, error) {
	if file, ok := source.(*os.File); ok {
		return hardware.ProbeFileStatus(file)
	}

	return map[string]uint32{}, nil
}
