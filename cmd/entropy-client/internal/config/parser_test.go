package config

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClientParserIsolatedDefaultsAndRepeatedCalls(t *testing.T) {
	global := flag.CommandLine

	first, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if first.Mode != "pull" || first.Slice != 2097152 || first.Refresh != time.Second || first.Headless || first.Retries != 0 || first.RetryDelay != time.Second {
		t.Fatalf("defaults: %+v", first)
	}

	changed, err := parseConfig([]string{"-headless", "-mode=stream", "-slice=64", "-retries=2", "-duration=3s"}, io.Discard)
	if err != nil || !changed.Headless || changed.Mode != "stream" || changed.Slice != 64 || changed.Retries != 2 || changed.Duration != 3*time.Second {
		t.Fatalf("flags: %+v %v", changed, err)
	}

	last, err := parseConfig(nil, io.Discard)
	if err != nil || !reflect.DeepEqual(first, last) || global != flag.CommandLine {
		t.Fatal("parser mutated defaults/global flags across calls")
	}
}

func TestClientParserHelpAndRejectedArguments(t *testing.T) {
	for _, args := range [][]string{{"-unknown"}, {"-slice=oops"}, {"-duration=forever"}, {"-headless=perhaps"}, {"-mode"}, {"run"}, {"--", "ignored"}, {"-headless", "false", "-output=ignored"}} {
		var output bytes.Buffer

		cfg, err := parseConfig(args, &output)
		if err == nil || cfg != nil || output.Len() == 0 {
			t.Fatalf("accepted %q: cfg=%+v err=%v", args, cfg, err)
		}
	}

	privateArg := "https://example.invalid/?token=" + t.Name()

	var diagnostic bytes.Buffer
	if _, err := parseConfig([]string{privateArg}, &diagnostic); err == nil || strings.Contains(diagnostic.String(), t.Name()) {
		t.Fatal("positional argument error should not print URL secrets")
	}

	for _, arg := range []string{"-help", "--help", "-h"} {
		var output bytes.Buffer
		if _, err := parseConfig([]string{arg}, &output); !errors.Is(err, flag.ErrHelp) {
			t.Fatal(err)
		}

		for _, want := range []string{"Entropy Client", "entropy-client [options]", "-output", "-retries"} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("missing %s from help", want)
			}
		}
	}
}

func FuzzClientArguments(f *testing.F) {
	for _, seed := range []string{"", "-headless -mode=stream", "-headless false -output=x", "-slice=9223372036854775808", "-help", "-- positional"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}

		cfg, err := parseConfig(strings.Fields(input), io.Discard)
		if (err == nil) != (cfg != nil) {
			t.Fatal("parser returned inconsistent config/error")
		}
	})
}
