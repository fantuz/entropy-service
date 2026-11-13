package config

import (
	"io"
	"reflect"
	"testing"
)

func TestLabAllowedHostsConfiguration(t *testing.T) {
	cfg, err := parseConfig([]string{"-lab-allowed-hosts=Lab.Example.Test.,192.0.2.10"}, io.Discard)
	if err != nil || !reflect.DeepEqual(cfg.LabAllowedHosts, []string{"lab.example.test", "192.0.2.10"}) || cfg.LabAllowRemote {
		t.Fatalf("explicit host list changed remote admission: %+v %v", cfg, err)
	}

	for _, value := range []string{"*", "https://example.test", "example.test:8080", "example.test,"} {
		if _, err := parseConfig([]string{"-lab-allowed-hosts=" + value}, io.Discard); err == nil {
			t.Errorf("invalid trusted host accepted: %q", value)
		}
	}
}
