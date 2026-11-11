//go:build !linux

package main

import "io"

func probeNativeStatus(io.ReadCloser) (map[string]uint32, error) { return map[string]uint32{}, nil }
