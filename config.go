package main

import (
	"flag"
	"fmt"
	"os"
)

type Config struct {
	ReseedMS      int
	ReseedSize    int
	MaxBytes      int
	QRNGBuffer    int
	SeedBuffer    int
	HTTPAddr      string
	HTTPSAddr     string
	CertFile      string
	KeyFile       string
	LogLevel      string
	DevicePath    string
	EnableHTTPS   bool
	RequireDevice bool
}

func ParseConfig() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.HTTPAddr, "http", ":8080", "HTTP listen address")
	flag.StringVar(&cfg.HTTPSAddr, "https", ":8443", "HTTPS listen address")
	flag.StringVar(&cfg.CertFile, "cert-file", "cert.pem", "Public Key File")
	flag.StringVar(&cfg.KeyFile, "key-file", "key.pem", "Private Key File")
	flag.StringVar(&cfg.DevicePath, "device", "/dev/qrandom0", "Entropy source, defaults to /dev/qrandom0")
	flag.IntVar(&cfg.ReseedMS, "reseed-ms", 250, "Reseed interval (ms)")
	flag.IntVar(&cfg.SeedBuffer, "buffer-entropy", 64, "Size of Entropy buffer in KB")
	flag.IntVar(&cfg.ReseedSize, "buffer-reseed", 256, "Size of Reseed buffer in Bytes")
	flag.IntVar(&cfg.QRNGBuffer, "buffer-qrng", 2048, "Size of QRNG buffer in KB")
	flag.IntVar(&cfg.MaxBytes, "max-bytes", 2097152, "Maximum bytes per request")
	flag.BoolVar(&cfg.EnableHTTPS, "enable-https", false, "Enable HTTPS server (disabled by default)")
	flag.BoolVar(&cfg.RequireDevice, "require-device", false, "Fail if entropy device unavailable")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Entropy Server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	return cfg
}
