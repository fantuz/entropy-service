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
	sourceWebpage     = "webpage"
	sourceRTSP        = "rtsp"
	sourceONVIF       = "onvif"
)

const MaxBytes = 1 << 20

var validID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type Config struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Endpoint     string   `json:"endpoint"`
	MaxBytes     int      `json:"max_bytes"`
	TimeoutMs    int      `json:"timeout_ms"`
	URLs         []string `json:"urls,omitempty"`
	Width        int      `json:"width,omitempty"`
	Height       int      `json:"height,omitempty"`
	ProfileToken string   `json:"profile_token,omitempty"`
}

type Descriptor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMs int    `json:"timeout_ms"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

type Registry struct {
	sources   map[string]Config
	client    *http.Client
	transport *http.Transport
	cameras   chan struct{}
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

	registry := &Registry{sources: make(map[string]Config), cameras: make(chan struct{}, 2)}

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

	if c.Kind == sourceRTSP || c.Kind == sourceONVIF {
		if c.Width == 0 && c.Height == 0 {
			c.Width, c.Height = 320, 240
		}

		if (c.Width != 160 || c.Height != 120) && (c.Width != 320 || c.Height != 240) && (c.Width != 640 || c.Height != 480) {
			return errors.New("camera size must be 160x120, 320x240 or 640x480")
		}

		if c.MaxBytes == 0 {
			c.MaxBytes = c.Width * c.Height * 3
		}

		if c.MaxBytes != c.Width*c.Height*3 {
			return errors.New("camera max_bytes must equal RGB frame size")
		}

		if c.TimeoutMs == 0 {
			c.TimeoutMs = 10000
		}
	} else if c.Width != 0 || c.Height != 0 || c.ProfileToken != "" {
		return errors.New("camera fields require an rtsp or onvif source")
	}

	if len(c.ProfileToken) > 256 || (c.ProfileToken != "" && c.Kind != sourceONVIF) {
		return errors.New("invalid ONVIF profile token")
	}

	if len(c.URLs) > 16 || (len(c.URLs) != 0 && c.Kind != sourceWebpage) {
		return errors.New("only webpage sources accept a pool of up to 16 URLs")
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
	case sourceHTTP, sourceWS, sourceEntropyHTTP, sourceEntropyWS, sourceWebpage, sourceONVIF:
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

		for _, endpoint := range c.URLs {
			candidate := Config{ID: c.ID, Kind: sourceHTTP, Endpoint: endpoint}
			if err := candidate.validate(); err != nil {
				return errors.New("invalid webpage pool URL")
			}
		}
	case sourceRTSP:
		if _, err := cameraURL(c.Endpoint); err != nil {
			return err
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
		return errors.New("unsupported source kind")
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
	return Descriptor{ID: c.ID, Name: c.Name, Kind: c.Kind, MaxBytes: c.MaxBytes, TimeoutMs: c.TimeoutMs, Width: c.Width, Height: c.Height}
}
func (r *Registry) Close() { r.transport.CloseIdleConnections() }

func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}

		if u.Scheme == "https" {
			return "443"
		}

		return "80"
	}

	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
