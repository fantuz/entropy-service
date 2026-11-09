package lab

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Credentials belongs to one capture session. It is never placed in a URL
// returned to the browser, configuration export, log or decoder argument list.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func cameraURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Scheme != sourceRTSP {
		return nil, errors.New("camera endpoint must be an RTSP URL without credentials or fragment; RTSPS requires a verified external relay")
	}

	return u, nil
}

func (r *Registry) openCamera(parent, dialCtx context.Context, stream *Stream, c Config, refresh int, credentials Credentials) error {
	if len(credentials.Username) > 256 || len(credentials.Password) > 1024 {
		return errors.New("camera credentials exceed limits")
	}

	select {
	case r.cameras <- struct{}{}:
	default:
		return errors.New("camera decoder limit reached")
	}

	release := true
	defer func() {
		if release {
			<-r.cameras
		}
	}()

	endpoint := c.Endpoint
	if c.Kind == sourceONVIF {
		var err error

		endpoint, err = r.onvifURI(dialCtx, c, credentials)
		if err != nil {
			return err
		}
	}

	u, err := cameraURL(endpoint)
	if err != nil {
		return err
	}

	if credentials.Username != "" {
		u.User = url.UserPassword(credentials.Username, credentials.Password)
	}

	// ffconcat supplies the input URL over stdin. Credentials therefore never
	// appear in argv or in a temporary file. Its single quoted token is escaped
	// using ffconcat's grammar, not evaluated by a shell.
	input := "ffconcat version 1.0\nfile '" + strings.ReplaceAll(u.String(), "'", "'\\''") + "'\noption rtsp_transport tcp\noption timeout " + strconv.Itoa(c.TimeoutMs*1000) + "\n"

	decoderCtx, cancel := context.WithCancel(parent)
	filter := "fps=1000/" + strconv.Itoa(refresh) + ",scale=" + strconv.Itoa(c.Width) + ":" + strconv.Itoa(c.Height) + ":flags=fast_bilinear,format=rgb24"
	command := exec.CommandContext(decoderCtx, "ffmpeg", "-hide_banner", "-loglevel", "quiet", "-nostdin", "-threads", "1", "-filter_threads", "1", "-max_alloc", "67108864", "-f", "concat", "-safe", "0", "-protocol_whitelist", "pipe,rtsp,tcp,udp,rtp", "-i", "pipe:0", "-an", "-sn", "-dn", "-vf", filter, "-pix_fmt", "rgb24", "-threads", "1", "-f", "rawvideo", "pipe:1")
	command.Stdin = strings.NewReader(input)
	// FFmpeg diagnostics can echo input URLs; never forward or retain stderr.
	command.Stderr = io.Discard
	command.WaitDelay = time.Second

	// Own the pipe: Cmd.Wait must not close it before the final frame is read.
	output, writer, err := os.Pipe()
	if err != nil {
		cancel()
		return errors.New("camera decoder pipe unavailable")
	}

	command.Stdout = writer

	if err := command.Start(); err != nil {
		cancel()

		_ = output.Close()
		_ = writer.Close()

		return errors.New("camera decoder unavailable; install FFmpeg on the server")
	}

	_ = writer.Close()

	done := make(chan struct{})

	var processErr error

	go func() { processErr = command.Wait(); close(done) }()

	stream.close = func() error { cancel(); _ = output.Close(); <-done; <-r.cameras; return nil }
	stream.read = func(ctx context.Context, count int) ([]byte, error) {
		stop := context.AfterFunc(ctx, func() { cancel(); _ = output.Close() })
		defer stop()

		frame := make([]byte, c.Width*c.Height*3)
		if _, err := io.ReadFull(output, frame); err != nil {
			select {
			case <-done:
				if processErr != nil {
					return nil, errors.New("camera decoder failed")
				}
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			return nil, err
		}

		return frame[:min(count, len(frame))], nil
	}
	release = false

	return nil
}
