// entropy-health checks the server's readiness without a shell or external tools.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

func run(args []string, getenv func(string) string, stderr io.Writer) int {
	flags := flag.NewFlagSet("entropy-health", flag.ContinueOnError)
	flags.SetOutput(stderr)

	endpoint := getenv("ENTROPY_HEALTH_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080/ready"
	}

	address := flags.String("url", endpoint, "local server readiness URL; ENTROPY_HEALTH_URL also sets the default")
	timeout := flags.Duration("timeout", 2*time.Second, "maximum duration of the readiness request")

	caFile := flags.String("ca-file", getenv("ENTROPY_HEALTH_CA_FILE"), "additional public CA/certificate PEM for HTTPS")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	u, err := url.Parse(*address)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || flags.NArg() != 0 || *timeout <= 0 {
		fmt.Fprintln(stderr, "invalid readiness URL, arguments or timeout")
		return 2
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	if *caFile != "" {
		pem, readErr := os.ReadFile(*caFile)
		if readErr != nil {
			fmt.Fprintln(stderr, "cannot read readiness CA/certificate:", readErr)
			return 2
		}

		roots, poolErr := x509.SystemCertPool()
		if poolErr != nil {
			fmt.Fprintln(stderr, "cannot load system trust:", poolErr)
			return 2
		}

		if !roots.AppendCertsFromPEM(pem) {
			fmt.Fprintln(stderr, "readiness CA/certificate contains no certificates")
			return 2
		}

		tlsConfig.RootCAs = roots
	}
	// A local health probe must not inherit an outbound HTTP proxy or follow a
	// redirect to another service. Its result describes this listener's /ready.
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   *timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		fmt.Fprintln(stderr, "invalid readiness request:", err)
		return 2
	}

	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintln(stderr, "readiness request failed:", err)
		return 1
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "server is not ready: HTTP", response.StatusCode)
		return 1
	}

	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr))
}
