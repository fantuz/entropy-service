package main

import (
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/device"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/output"
)

func openOutput(cfg *config.Config) (writeoutSink, error) {
	switch {
	case cfg.WriteOut:
		return device.Open()
	case cfg.OutputDevice != "":
		return device.OpenDestination(cfg.OutputDevice)
	case cfg.OutputPath == "-":
		return output.OpenStdout()
	default:
		return output.OpenFile(cfg.OutputPath)
	}
}
