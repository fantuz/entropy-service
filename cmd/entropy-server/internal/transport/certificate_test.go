package transport

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/internal/privatefile"
)

func certificateOptions(t *testing.T) CertificateOptions {
	t.Helper()
	dir := t.TempDir()

	return CertificateOptions{
		CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem"),
		Hosts: []string{"localhost", "127.0.0.1", "::1", "entropy.example"}, ValidFor: 24 * time.Hour,
	}
}

func readCertificateFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestGenerateCertificateIdentityAndReuse(t *testing.T) {
	opts := certificateOptions(t)
	started := time.Now()

	if created, err := GenerateCertificate(opts); err != nil || !created {
		t.Fatalf("created=%t error=%v", created, err)
	}

	cfg, err := NewTLSConfig(opts.CertFile, opts.KeyFile)
	if err != nil {
		t.Fatal(err)
	}

	leaf := cfg.Certificates[0].Leaf
	if leaf.Subject.String() == "" || leaf.Issuer.String() == "" || !bytes.Equal(leaf.RawSubject, leaf.RawIssuer) {
		t.Fatal("self-signed identity must have matching nonempty issuer and subject names for TLS client compatibility")
	}

	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	for _, host := range opts.Hosts {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err != nil {
			t.Fatalf("generated certificate failed normal server verification for %q: %v", host, err)
		}
	}

	if leaf.IsCA || leaf.NotAfter.Before(started.Add(opts.ValidFor-time.Second)) || leaf.NotAfter.After(time.Now().Add(opts.ValidFor)) {
		t.Fatal("certificate has unexpected CA status or lifetime")
	}

	for _, path := range []string{opts.CertFile, opts.KeyFile} {
		if err := privatefile.Check(path); err != nil {
			t.Fatalf("TLS file %q must satisfy its platform's private-file policy: %v", path, err)
		}
	}

	certBefore, keyBefore := readCertificateFile(t, opts.CertFile), readCertificateFile(t, opts.KeyFile)
	if created, err := GenerateCertificate(opts); created || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing pair was not rejected: created=%t error=%v", created, err)
	}

	opts.IfMissing = true
	if created, err := GenerateCertificate(opts); err != nil || created {
		t.Fatalf("existing pair was not reused: created=%t error=%v", created, err)
	}

	if !bytes.Equal(certBefore, readCertificateFile(t, opts.CertFile)) || !bytes.Equal(keyBefore, readCertificateFile(t, opts.KeyFile)) {
		t.Fatal("existing identity changed during provisioning")
	}

	other := certificateOptions(t)
	if _, err := GenerateCertificate(other); err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(keyBefore, readCertificateFile(t, other.KeyFile)) {
		t.Fatal("separate installations generated the same private key")
	}
}

func TestGenerateCertificateRejectsInvalidOptionsWithoutFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*CertificateOptions)
	}{
		{"empty cert path", func(o *CertificateOptions) { o.CertFile = "" }},
		{"empty key path", func(o *CertificateOptions) { o.KeyFile = "" }},
		{"same path", func(o *CertificateOptions) { o.KeyFile = filepath.Join(filepath.Dir(o.CertFile), ".", "cert.pem") }},
		{"short lifetime", func(o *CertificateOptions) { o.ValidFor = time.Minute }},
		{"long lifetime", func(o *CertificateOptions) { o.ValidFor = 366 * 24 * time.Hour }},
		{"no hosts", func(o *CertificateOptions) { o.Hosts = nil }},
		{"too many hosts", func(o *CertificateOptions) { o.Hosts = make([]string, 101) }},
		{"empty host", func(o *CertificateOptions) { o.Hosts = []string{"localhost", ""} }},
		{"URL", func(o *CertificateOptions) { o.Hosts = []string{"https://localhost"} }},
		{"port", func(o *CertificateOptions) { o.Hosts = []string{"localhost:8443"} }},
		{"wildcard", func(o *CertificateOptions) { o.Hosts = []string{"*.example"} }},
		{"long DNS name", func(o *CertificateOptions) { o.Hosts = []string{strings.Repeat("a", 254)} }},
		{"long DNS label", func(o *CertificateOptions) { o.Hosts = []string{strings.Repeat("a", 64) + ".example"} }},
		{"invalid DNS label", func(o *CertificateOptions) { o.Hosts = []string{"-host.example"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := certificateOptions(t)
			original := opts
			tc.edit(&opts)

			if created, err := GenerateCertificate(opts); created || err == nil {
				t.Fatalf("invalid options accepted: created=%t error=%v", created, err)
			}

			for _, path := range []string{original.CertFile, original.KeyFile} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid request left TLS file %q: %v", path, err)
				}
			}
		})
	}
}

func TestGenerateCertificateRefusesUnsafeExistingPairs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *CertificateOptions)
	}{
		{"partial pair", func(t *testing.T, o *CertificateOptions) {
			t.Helper()

			if err := os.Remove(o.CertFile); err != nil {
				t.Fatal(err)
			}
		}},
		{"mismatched pair", func(t *testing.T, o *CertificateOptions) {
			t.Helper()

			_, otherKey, _ := testCertificate(t)
			if err := os.WriteFile(o.KeyFile, readCertificateFile(t, otherKey), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"world-readable key", func(t *testing.T, o *CertificateOptions) {
			t.Helper()

			if runtime.GOOS == "windows" {
				t.Skip("Unix mode-bit policy; Windows ACL broadening is tested in internal/privatefile")
			}

			if err := os.Chmod(o.KeyFile, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong host", func(_ *testing.T, o *CertificateOptions) { o.Hosts = []string{"other.example"} }},
		{"expired", func(t *testing.T, o *CertificateOptions) {
			t.Helper()
			o.CertFile, o.KeyFile, _ = testCertificatePolicy(t, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour), x509.ExtKeyUsageServerAuth)
			o.Hosts = []string{"127.0.0.1"}
		}},
		{"future", func(t *testing.T, o *CertificateOptions) {
			t.Helper()
			o.CertFile, o.KeyFile, _ = testCertificatePolicy(t, time.Now().Add(time.Hour), time.Now().Add(2*time.Hour), x509.ExtKeyUsageServerAuth)
			o.Hosts = []string{"127.0.0.1"}
		}},
		{"symlink", func(t *testing.T, o *CertificateOptions) {
			t.Helper()

			original := o.KeyFile

			o.KeyFile += ".link"
			if err := os.Symlink(original, o.KeyFile); err != nil {
				t.Skipf("account cannot create symlinks: %v", err)
			}
		}},
		{"client-only certificate", func(t *testing.T, o *CertificateOptions) {
			t.Helper()
			o.CertFile, o.KeyFile, _ = testCertificatePolicy(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), x509.ExtKeyUsageClientAuth)
			o.Hosts = []string{"127.0.0.1"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := certificateOptions(t)
			if _, err := GenerateCertificate(opts); err != nil {
				t.Fatal(err)
			}

			opts.IfMissing = true
			tc.setup(t, &opts)

			before := readCertificateFile(t, opts.KeyFile)
			if created, err := GenerateCertificate(opts); created || err == nil {
				t.Fatalf("unsafe pair reused or replaced: created=%t error=%v", created, err)
			}

			if !bytes.Equal(before, readCertificateFile(t, opts.KeyFile)) {
				t.Fatal("failed provisioning changed existing key")
			}
		})
	}
}

func TestGenerateCertificateRollsBackNewKeyOnCertificateFailure(t *testing.T) {
	opts := certificateOptions(t)

	opts.CertFile = filepath.Join(filepath.Dir(opts.CertFile), "absent", "cert.pem")
	if created, err := GenerateCertificate(opts); created || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected creation result: created=%t error=%v", created, err)
	}

	if _, err := os.Lstat(opts.KeyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed certificate write left a partial key: %v", err)
	}
}

func TestRejectRetiredPublicKey(t *testing.T) {
	// Public SPKI only: no copy of the retired private key is needed in tests.
	const publicSPKI = "MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEApHEyDbvJ+pGYHGUmbOaEw7jiwuEuTCW2QABGPA4aDGITWNkvHDQoBBkejTs0x60cLYwet2+nO+rkWGY6MaX5xtAgKUIJFDr6NOT28/DzgtMJoS/NSwbhX2QB2H9bhBb4CuTXj//RASxom+HEymOJuxYIHlK/3P3kK6iJpMJ6LUe54IClo1izt/QADuxWelLHfvsy+rVG3YGsseOcua7y0k9XYM/0fsA7zNAwwNj7suptaD5499/9ALtOdgeEk6IBFVQUStDxM0KyoYZP8wjXlMQgjA20Ud5ht56WG4OnUy5TTzPhVdITxg2+e9QdpFYyWButbNm7s1LY1KxtzDLBlfuovUa1uvy2N/qwpbrU29rAYtVjd6GeTx7phKKMbTAeYQMH7ZY8kuBEfsdHznqkFQ3euJc1aJHx4mT0QLsvUMWt7FmJ9hg+29PL6ABzJfoLEavYysGGi42bBLo0H4vDD1aHx8bbqZilxtyWwinbv32SRMxmuToUYk49Ji0wCe7DtkfnANKv644vM6wGUK5JTjBoXMR1lZOZp8fwm+RXWFcEoMWjLbraYFkvRUWNvGs8C75U/TCLVisqjD8lNF/UuS1/pBIHllAfRNek6ratQShvzwuQU0MlLJMbc3j0yKaCv5QDR0UyVOSEZKNt+LeR85SAwOQbxFLINqP/fGXbY3cCAwEAAQ=="

	der, err := base64.StdEncoding.DecodeString(publicSPKI)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := x509.ParsePKIXPublicKey(der); err != nil {
		t.Fatal(err)
	}

	for _, serial := range []int64{1, 2} {
		leaf := &x509.Certificate{RawSubjectPublicKeyInfo: der, SerialNumber: big.NewInt(serial)}
		if err := rejectRetiredPublicKey(leaf); err == nil {
			t.Fatalf("public demonstration key accepted in certificate %d", serial)
		}
	}
}
