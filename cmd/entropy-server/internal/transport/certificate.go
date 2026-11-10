package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// CertificateOptions describes an explicitly requested, self-signed server identity.
// Keys come from the OS CSPRNG, independently of the service's DRBG and sources.
type CertificateOptions struct {
	CertFile, KeyFile string
	Hosts             []string
	ValidFor          time.Duration
	IfMissing         bool
}

// GenerateCertificate creates an owner-only pair without replacing existing files.
// With IfMissing, a complete, valid pair for every requested host is reused.
// Parents must already exist and be controlled by the operator. Pair publication
// is not atomic: a crash can leave a partial pair, which requires manual recovery.
func GenerateCertificate(opts CertificateOptions) (bool, error) {
	template, err := certificateTemplate(opts, time.Now())
	if err != nil {
		return false, err
	}

	if err := distinctCertificatePaths(opts); err != nil {
		return false, err
	}

	exists, err := existingCertificate(opts)
	if err != nil || exists {
		return false, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generate TLS key: %w", err)
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return false, fmt.Errorf("create TLS certificate: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return false, fmt.Errorf("encode TLS key: %w", err)
	}
	defer clear(keyDER)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	defer clear(keyPEM)

	keyInfo, err := writeNewCertificateFile(opts.KeyFile, keyPEM)
	if err != nil {
		return false, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if _, err := writeNewCertificateFile(opts.CertFile, certPEM); err != nil {
		return false, errors.Join(err, removeCreatedFile(opts.KeyFile, keyInfo))
	}

	return true, nil
}

func certificateTemplate(opts CertificateOptions, now time.Time) (*x509.Certificate, error) {
	if opts.ValidFor < time.Hour || opts.ValidFor > 365*24*time.Hour {
		return nil, errors.New("valid-for must be between 1h and 8760h (365 days)")
	}

	if len(opts.Hosts) == 0 || len(opts.Hosts) > 100 {
		return nil, errors.New("hosts must contain between 1 and 100 DNS names or IP addresses")
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}

	serial.SetBit(serial, 127, 1)

	template := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(opts.ValidFor),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, value := range opts.Hosts {
		host := strings.TrimSpace(value)
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else if validCertificateDNSName(host) {
			template.DNSNames = append(template.DNSNames, host)
		} else {
			return nil, fmt.Errorf("invalid certificate host %q: use a DNS name or bare IP, without port, URL or wildcard", value)
		}
	}

	return template, nil
}

func validCertificateDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}

	for label := range strings.SplitSeq(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}

	return true
}

func distinctCertificatePaths(opts CertificateOptions) error {
	if opts.CertFile == "" || opts.KeyFile == "" {
		return errors.New("cert-file and key-file must both be nonempty")
	}

	cert, err := filepath.Abs(opts.CertFile)
	if err != nil {
		return fmt.Errorf("resolve cert-file: %w", err)
	}

	key, err := filepath.Abs(opts.KeyFile)
	if err != nil {
		return fmt.Errorf("resolve key-file: %w", err)
	}

	if cert == key {
		return errors.New("cert-file and key-file must be different paths")
	}

	return nil
}

func existingCertificate(opts CertificateOptions) (bool, error) {
	cert, certErr := os.Lstat(opts.CertFile)

	key, keyErr := os.Lstat(opts.KeyFile)
	if errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist) {
		return false, nil
	}

	if certErr != nil || keyErr != nil {
		return false, fmt.Errorf("incomplete or inaccessible TLS pair; repair explicitly before retrying: %w", errors.Join(certErr, keyErr))
	}

	if !opts.IfMissing {
		return false, fmt.Errorf("TLS files already exist; refusing to overwrite: %w", os.ErrExist)
	}

	if !cert.Mode().IsRegular() || !key.Mode().IsRegular() || key.Mode().Perm()&0o077 != 0 {
		return false, errors.New("existing TLS pair must use regular files, without symlinks, and an owner-only private key")
	}

	cfg, err := NewTLSConfig(opts.CertFile, opts.KeyFile)
	if err != nil {
		return false, err
	}

	leaf := cfg.Certificates[0].Leaf
	if (len(leaf.ExtKeyUsage) != 0 || len(leaf.UnknownExtKeyUsage) != 0) &&
		!slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) &&
		!slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageAny) {
		return false, errors.New("existing TLS certificate does not permit server authentication")
	}

	if now := time.Now(); now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return false, errors.New("existing TLS certificate is not currently valid; renew explicitly")
	}

	for _, host := range opts.Hosts {
		if err := leaf.VerifyHostname(strings.TrimSpace(host)); err != nil {
			return false, fmt.Errorf("existing TLS certificate does not cover requested host: %w", err)
		}
	}

	return true, nil
}

func writeNewCertificateFile(path string, contents []byte) (os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create TLS file %q without overwriting: %w", path, err)
	}

	info, statErr := f.Stat()
	_, writeErr := f.Write(contents)
	syncErr := f.Sync()

	closeErr := f.Close()
	if err := errors.Join(statErr, writeErr, syncErr, closeErr); err != nil {
		return nil, errors.Join(fmt.Errorf("write TLS file %q: %w", path, err), removeCreatedFile(path, info))
	}

	return info, nil
}

// Do not remove a file replaced by another actor during an unsuccessful write.
func removeCreatedFile(path string, created os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("inspect partial TLS file %q: %w", path, err)
	}

	if created == nil || !os.SameFile(created, current) {
		return fmt.Errorf("TLS file %q changed during creation; inspect manually", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove partial TLS file %q: %w", path, err)
	}

	return nil
}
