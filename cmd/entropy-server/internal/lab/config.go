// Package lab reads explicitly registered experimental sources. It does not
// participate in DRBG seeding or change the meaning of generated entropy APIs.
package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	sourceHTTP        = "http"
	sourceWS          = "websocket"
	sourceEntropyHTTP = "entropy-http"
	sourceEntropyWS   = "entropy-ws"
	sourceTCP         = "tcp"
	sourceUDP         = "udp"
	sourceUnix        = "unix"
	sourceDevice      = "device"
)

const MaxBytes = 1 << 20

var validID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type Config struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Endpoint  string `json:"endpoint"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMs int    `json:"timeout_ms"`
}

type Descriptor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMs int    `json:"timeout_ms"`
}

type Registry struct {
	sources   map[string]Config
	client    *http.Client
	transport *http.Transport
}

func Load(path string) (*Registry, error) {
	var document struct {
		Sources []Config `json:"sources"`
	}

	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open lab source configuration: %w", err)
		}
		defer file.Close()

		decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
		decoder.DisallowUnknownFields()

		if err := decoder.Decode(&document); err != nil {
			return nil, fmt.Errorf("decode lab source configuration: %w", err)
		}

		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return nil, errors.New("lab configuration must contain one JSON document under 64 KiB")
		}

		stat, err := file.Stat()
		if err != nil || stat.Size() > 64<<10 {
			return nil, errors.New("lab source configuration exceeds 64 KiB")
		}
	}

	return New(document.Sources)
}

func New(configs []Config) (*Registry, error) {
	if len(configs) > 32 {
		return nil, errors.New("at most 32 lab sources may be registered")
	}

	registry := &Registry{sources: make(map[string]Config)}

	for _, config := range configs {
		if err := config.validate(); err != nil {
			return nil, fmt.Errorf("lab source %q: %w", config.ID, err)
		}

		if _, exists := registry.sources[config.ID]; exists {
			return nil, fmt.Errorf("duplicate lab source ID %q", config.ID)
		}

		registry.sources[config.ID] = config
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("unsupported default HTTP transport")
	}

	registry.transport = transport.Clone()
	registry.transport.MaxConnsPerHost = 8
	registry.transport.MaxIdleConns = 32
	registry.transport.IdleConnTimeout = 30 * time.Second
	registry.client = &http.Client{Transport: registry.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	return registry, nil
}

func (c *Config) validate() error {
	if !validID.MatchString(c.ID) {
		return errors.New("id must be 1–32 lowercase letters, digits, hyphens or underscores, starting with a letter")
	}

	if c.Name == "" {
		c.Name = c.ID
	}

	if len(c.Name) > 80 {
		return errors.New("name exceeds 80 bytes")
	}

	if c.MaxBytes == 0 {
		c.MaxBytes = MaxBytes
	}

	if c.TimeoutMs == 0 {
		c.TimeoutMs = 5000
	}

	if c.MaxBytes < 1 || c.MaxBytes > MaxBytes || c.TimeoutMs < 100 || c.TimeoutMs > 60000 {
		return errors.New("max_bytes must be 1–1048576 and timeout_ms 100–60000")
	}

	switch c.Kind {
	case sourceHTTP, sourceWS, sourceEntropyHTTP, sourceEntropyWS:
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(c.Endpoint) > 2048 {
			return errors.New("invalid source URL; userinfo and fragments are not allowed")
		}

		allowed := u.Scheme == sourceHTTP || u.Scheme == "https"
		if c.Kind == sourceWS {
			allowed = u.Scheme == "ws" || u.Scheme == "wss"
		}

		if !allowed {
			return errors.New("source URL has the wrong scheme")
		}

		cleaned := path.Clean(u.Path)
		if cleaned == "/lab" || strings.HasPrefix(cleaned, "/lab/") {
			return errors.New("lab relay endpoints cannot be used as upstream sources")
		}

		if strings.HasPrefix(c.Kind, "entropy-") && (u.Path != "" && u.Path != "/" || u.RawQuery != "") {
			return errors.New("entropy-server endpoint must be a base HTTP(S) URL without path or query")
		}
	case sourceTCP, sourceUDP:
		host, port, err := net.SplitHostPort(c.Endpoint)
		if err != nil || host == "" {
			return errors.New("socket endpoint must be host:port")
		}

		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("socket port must be 1–65535")
		}
	case sourceUnix, sourceDevice:
		if !filepath.IsAbs(c.Endpoint) || len(c.Endpoint) > 4096 {
			return errors.New("socket/device endpoint must be an absolute path")
		}
	default:
		return errors.New("kind must be http, websocket, entropy-http, entropy-ws, tcp, udp, unix or device")
	}

	return nil
}

func (r *Registry) List() []Descriptor {
	list := make([]Descriptor, 0, len(r.sources))
	for _, config := range r.sources {
		list = append(list, describe(config))
	}

	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	return list
}

func (r *Registry) Lookup(id string) (Descriptor, bool) {
	config, ok := r.sources[id]
	return describe(config), ok
}

func describe(c Config) Descriptor {
	return Descriptor{ID: c.ID, Name: c.Name, Kind: c.Kind, MaxBytes: c.MaxBytes, TimeoutMs: c.TimeoutMs}
}
func (r *Registry) Close() { r.transport.CloseIdleConnections() }
