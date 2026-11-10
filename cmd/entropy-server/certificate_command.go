package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/transport"
)

// runCertificateCommand runs before server configuration, entropy sources or
// listeners are opened. Only paths and public certificate details are reported.
func runCertificateCommand(args []string, output io.Writer) int {
	var (
		opts  transport.CertificateOptions
		hosts string
	)

	flags := flag.NewFlagSet("entropy-server generate-cert", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.CertFile, "cert-file", "cert.pem", "New TLS certificate path (never overwritten)")
	flags.StringVar(&opts.KeyFile, "key-file", "key.pem", "New owner-only TLS private key path (never overwritten)")
	flags.StringVar(&hosts, "hosts", "localhost,127.0.0.1,::1", "Comma-separated DNS names and bare IP addresses for certificate SANs")
	flags.DurationVar(&opts.ValidFor, "valid-for", 7*24*time.Hour, "Certificate lifetime, 1h to 8760h; renewal is explicit")
	flags.BoolVar(&opts.IfMissing, "if-missing", false, "Reuse an existing valid pair for these hosts; fail on partial, expired, unsafe or mismatched pairs")

	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: entropy-server generate-cert [options]")
		fmt.Fprintln(output, "Create a self-signed identity using OS randomness. Parent directories must exist and be operator-controlled.")
		fmt.Fprintln(output, "Clients must explicitly trust this certificate. Use a CA-issued certificate for public deployments.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if flags.NArg() != 0 {
		fmt.Fprintln(output, "generate-cert does not accept positional arguments")
		return 2
	}

	opts.Hosts = strings.Split(hosts, ",")

	created, err := transport.GenerateCertificate(opts)
	if err != nil {
		fmt.Fprintf(output, "TLS certificate provisioning failed: %v\n", err)
		return 1
	}

	if created {
		fmt.Fprintf(output, "Created self-signed TLS certificate %q and owner-only private key %q.\n", opts.CertFile, opts.KeyFile)
	} else {
		fmt.Fprintf(output, "Reusing valid TLS certificate %q and owner-only private key %q.\n", opts.CertFile, opts.KeyFile)
	}

	fmt.Fprintln(output, "Clients must trust the certificate explicitly; monitor expiry and renew before it expires.")

	return 0
}
