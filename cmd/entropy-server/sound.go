package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
)

const (
	soundSampleRate = 48000
	soundBloom      = "bloom"
	soundChime      = "chime"
)

type soundOptions struct {
	durationMs int
	style      string
}

func parseSoundOptions(rawQuery string) (soundOptions, error) {
	opts := soundOptions{durationMs: 2500, style: soundBloom}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return opts, errors.New("invalid sound query")
	}

	for name, values := range query {
		if len(values) != 1 || values[0] == "" {
			return opts, fmt.Errorf("%s must have exactly one non-empty value", name)
		}

		switch name {
		case "duration_ms":
			opts.durationMs, err = strconv.Atoi(values[0])
			if err != nil || opts.durationMs < 250 || opts.durationMs > 5000 {
				return opts, errors.New("duration_ms must be between 250 and 5000")
			}
		case "style":
			opts.style = values[0]
			if opts.style != soundBloom && opts.style != soundChime {
				return opts, errors.New("style must be bloom or chime")
			}
		default:
			return opts, fmt.Errorf("unknown sound parameter: %s", name)
		}
	}

	return opts, nil
}

// randomSoundHandler consumes 64 DRBG bytes per sound, not per PCM sample.
// source must support concurrent reads (the server's DRBG does).
func randomSoundHandler(source io.Reader) http.HandlerFunc {
	// Bound synthesis work and slow downloads without queuing more requests.
	slots := make(chan struct{}, 2)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		opts, err := parseSoundOptions(r.URL.RawQuery)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		if r.Context().Err() != nil {
			return
		}

		var wav []byte

		if r.Method == http.MethodGet {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				w.Header().Set("Retry-After", "1")
				http.Error(w, "sound service busy; retry shortly", http.StatusServiceUnavailable)

				return
			}

			var entropy [64]byte
			if _, err = io.ReadFull(source, entropy[:]); err != nil {
				log.Printf("sound parameter read: %v", err)
				http.Error(w, "entropy source unavailable", http.StatusInternalServerError)

				return
			}

			wav, err = synthesizeSound(r.Context(), entropy, opts)
			if err != nil {
				// Rendering only fails when the request context is canceled.
				return
			}
		}

		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", strconv.Itoa(44+soundSampleRate*opts.durationMs/1000*2))
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"entropy-%s.wav\"", opts.style))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if r.Method == http.MethodGet {
			if _, err = w.Write(wav); err != nil {
				log.Printf("sound response write: %v", err)
			}
		}
	}
}

type soundPartial struct {
	amplitude, phase, step    float64
	attackLevel, decayLevel   float64
	attackFactor, decayFactor float64
}

// synthesizeSound receives validated options. All frequencies are harmonics
// of one root; randomness changes the timbre without adding a melody or beat.
func synthesizeSound(ctx context.Context, entropy [64]byte, opts soundOptions) ([]byte, error) {
	unit := func(offset int) float64 {
		return float64(binary.LittleEndian.Uint32(entropy[offset:offset+4])) / (1 << 32)
	}
	root, attack, falloff := 170+70*unit(0), 0.25, 1.4
	harmonics := [5]int{1, 2, 3, 4, 5}

	if opts.style == soundChime {
		root, attack, falloff = 240+90*unit(0), 0.01, 1
		harmonics = [5]int{1, 2, 3, 5, 7}
	}

	var partials [5]soundPartial

	for i, harmonic := range harmonics {
		offset := 4 + i*12
		decay := (0.7 + 0.7*unit(offset+8)) / (1 + float64(harmonic)*0.1)
		partials[i] = soundPartial{
			amplitude:   (0.65 + 0.35*unit(offset)) / math.Pow(float64(harmonic), falloff),
			phase:       2 * math.Pi * unit(offset+4),
			step:        2 * math.Pi * root * float64(harmonic) / soundSampleRate,
			attackLevel: 1, decayLevel: 1,
			attackFactor: math.Exp(-1 / (soundSampleRate * attack)),
			decayFactor:  math.Exp(-1 / (soundSampleRate * decay)),
		}
	}

	samples := make([]float64, soundSampleRate*opts.durationMs/1000)
	peak := 0.0

	for i := range samples {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		value := 0.0

		for j := range partials {
			p := &partials[j]
			growth := 1 - p.attackLevel
			value += p.amplitude * growth * growth * p.decayLevel * math.Sin(p.phase)
			p.attackLevel *= p.attackFactor
			p.decayLevel *= p.decayFactor

			p.phase += p.step
			if p.phase >= 2*math.Pi {
				p.phase -= 2 * math.Pi
			}
		}

		// Smooth the first/last 40 ms, including an exact zero at both ends.
		if edge := min(i, len(samples)-1-i); edge < soundSampleRate*40/1000 {
			x := float64(edge) / (soundSampleRate * 40 / 1000)
			value *= x * x * (3 - 2*x)
		}

		samples[i] = value
		peak = math.Max(peak, math.Abs(value))
	}

	wav := make([]byte, 44+len(samples)*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1) // PCM
	binary.LittleEndian.PutUint16(wav[22:], 1) // mono
	binary.LittleEndian.PutUint32(wav[24:], soundSampleRate)
	binary.LittleEndian.PutUint32(wav[28:], soundSampleRate*2)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(wav)-44))

	// Normalize to a modest peak of -12 dBFS, independent of random phases.
	if peak > 0 {
		for i, sample := range samples {
			if i%1024 == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}

			pcm := int16(math.Round(sample / peak * 0.25 * math.MaxInt16))
			binary.LittleEndian.PutUint16(wav[44+i*2:], uint16(pcm))
		}
	}

	return wav, nil
}
