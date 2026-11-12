package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

const tlsWindowsOS = "windows"

func tlsStartupHint(certFile, keyFile string, err error) string {
	return missingTLSHint(runtime.GOOS, certFile, keyFile, err)
}

// Provisioning is explicit: startup never creates or replaces an identity.
func missingTLSHint(platform, certFile, keyFile string, err error) string {
	if !errors.Is(err, os.ErrNotExist) {
		return ""
	}

	command := "entropy-server"
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	guide := "https://github.com/fantuz/entropy-service/blob/main/docs/tls-certificates.md"

	if platform == tlsWindowsOS {
		command = `.\entropy-server.exe`
		quote = func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
		guide = "https://github.com/fantuz/entropy-service/blob/main/docs/windows.md#files-logs-and-tls"
	}

	return fmt.Sprintf("\nHTTPS is enabled; provision a certificate/key pair before starting. From the executable directory:\n  %s generate-cert %s %s -hosts=localhost,127.0.0.1,::1\nThen start with the same -cert-file and -key-file paths. Trust only the public certificate; keep the private key private.\nFor initial local testing, disable HTTPS and bind HTTP to loopback:\n  %s -enable-https=false -http=127.0.0.1:8080\nOpen http://127.0.0.1:8080/ for that test. Certificate setup: %s", command, quote("-cert-file="+certFile), quote("-key-file="+keyFile), command, guide)
}
