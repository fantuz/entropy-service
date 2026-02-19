package main

import (
	"flag"
	"fmt"
	"os"
)

type Config struct {
	HTTPAddr    string
	HTTPSAddr   string
	ReseedMS    int
	MaxBytes    int
	QRNGBuffer  int
	CertFile    string
	KeyFile     string
	BufferKB    int
	LogLevel    string
	EnableHTTPS bool
}

func ParseConfig() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.HTTPAddr, "http", ":8080", "HTTP listen address")
	flag.StringVar(&cfg.HTTPSAddr, "https", ":8443", "HTTPS listen address (disabled if empty)")
	flag.IntVar(&cfg.ReseedMS, "reseed-ms", 250, "Reseed interval (ms)")
	flag.IntVar(&cfg.MaxBytes, "max-bytes", 1048576, "Maximum bytes per request") // taget 2097152
	flag.IntVar(&cfg.QRNGBuffer, "qrng-buffer-kb", 2048, "QRNG Buffer in Kilobytes")
	flag.StringVar(&cfg.CertFile, "cert-file", "cert.pem", "Public Key File")
	flag.StringVar(&cfg.KeyFile, "key-file", "key.pem", "Private Key File")
	flag.IntVar(&cfg.BufferKB, "buffer-kb", 64, "Entropy buffer size (KB)")
	flag.BoolVar(&cfg.EnableHTTPS, "enable-https", false, "Enable HTTPS server")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Entropy Server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	return cfg
}
