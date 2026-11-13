package lab

import (
	"reflect"
	"strings"
	"testing"
)

func TestTrustedLocalHost(t *testing.T) {
	extra, err := ParseAllowedHosts("Lab.Example.Test.,192.0.2.10,2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}

	for _, host := range []string{"localhost", "LOCALHOST.:8080", "127.0.0.1:8080", "127.0.0.2", "[::1]:8443", "[::ffff:127.0.0.1]:8080", "lab.example.test:8080", "192.0.2.10", "[2001:db8::1]:8080"} {
		if !TrustedLocalHost(host, extra) {
			t.Errorf("rejected trusted host %q", host)
		}
	}

	for _, host := range []string{"", "rebound.invalid:8080", "localhost.evil.test", "localhost@evil.test", "evil.test@localhost", "127.0.0.1.evil.test", "localhost:0", "localhost:65536", "localhost:", "localhost:abc", "localhost/path", "localhost?query", "localhost#fragment", "localhost\r\nHost: evil", " localhost", "::1", "[::1%25lo]:8080", "http://localhost", "%6cocalhost", "2130706433", "127.1"} {
		if TrustedLocalHost(host, extra) {
			t.Errorf("accepted untrusted/malformed host %q", host)
		}
	}
}

func TestAdditionalLabHostsAreExplicitAndBounded(t *testing.T) {
	hosts, err := ParseAllowedHosts(" LAB.EXAMPLE.TEST.,lab.example.test,::ffff:192.0.2.10 ")
	if err != nil || !reflect.DeepEqual(hosts, []string{"lab.example.test", "192.0.2.10"}) {
		t.Fatalf("host normalization: %v %v", hosts, err)
	}

	for _, value := range []string{"*", "*.example.test", "http://example.test", "example.test:8080", "[::1]:8080", "example.test,", "a..test", "-bad.test", "bad-.test", "user@host", "host/path", strings.Repeat("a,", 32) + "last"} {
		if _, err := ParseAllowedHosts(value); err == nil {
			t.Errorf("accepted unsafe host configuration %q", value)
		}
	}

	if TrustedLocalHost("lab.example.test:8080", nil) {
		t.Fatal("a hostname was trusted without explicit configuration")
	}
}
