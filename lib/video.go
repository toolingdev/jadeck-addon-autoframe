package lib

// Video in: ffmpeg decodes a camera's small stream (or a test file) into
// RGB frames at the tracker's rate. Only the newest frame is kept; the
// detector takes whatever is newest when it's ready, so a slow detector
// drops frames instead of falling behind.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	VideoW = 640
	VideoH = 360
)

type Source struct {
	URL    string
	FFmpeg string
	FPS    int

	mu     sync.Mutex
	latest *Frame
	seq    uint64
	status string
	errAt  time.Time
}

func (s *Source) Latest() *Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

func (s *Source) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Source) set(st string) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

// Run reads until ctx ends, restarting ffmpeg when the stream drops.
func (s *Source) Run(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := s.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			wait = time.Second
		}
		s.set(fmt.Sprintf("no video (%v), retrying", err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < 15*time.Second {
			wait *= 2
		}
	}
}

func (s *Source) once(ctx context.Context) error {
	fps := s.FPS
	if fps <= 0 {
		fps = 10
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if strings.HasPrefix(s.URL, "rtsp://") {
		args = append(args, "-rtsp_transport", "tcp", "-fflags", "nobuffer", "-flags", "low_delay")
	} else if !strings.Contains(s.URL, "://") { // a file: loop it at its own speed
		args = append(args, "-re", "-stream_loop", "-1")
	}
	args = append(args, "-i", s.URL, "-an",
		"-vf", fmt.Sprintf("fps=%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", fps, VideoW, VideoH, VideoW, VideoH),
		"-pix_fmt", "rgb24", "-f", "rawvideo", "-")
	ff := s.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ff, args...)
	var stderr strings.Builder
	cmd.Stderr = &limited{w: &stderr, n: 2000}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "executable file not found") {
			return fmt.Errorf("ffmpeg isn't installed")
		}
		return err
	}
	s.set("connecting")
	br := bufio.NewReaderSize(out, VideoW*VideoH*3)
	for {
		buf := make([]byte, VideoW*VideoH*3)
		if _, err := io.ReadFull(br, buf); err != nil {
			_ = cmd.Wait()
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
					msg = msg[i+1:]
				}
				return fmt.Errorf("%s", msg)
			}
			return fmt.Errorf("stream ended")
		}
		s.mu.Lock()
		s.seq++
		s.latest = &Frame{W: VideoW, H: VideoH, RGB: buf, At: time.Now(), Seq: s.seq}
		s.status = "live"
		s.mu.Unlock()
	}
}

type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// small helpers shared by the package

func itoa(v int) string { return fmt.Sprint(v) }

func ftoa(v float64) string { return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") }

func join(p []string) string { return strings.Join(p, " · ") }
