// Package transcode re-encodes, with ffmpeg, a clip too large for Telegram so that
// it fits under a size limit: the bitrate is computed from the clip's duration, and
// the picture is scaled down as the bitrate drops.
package transcode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Binary is the ffmpeg executable; the Docker image ships it in the PATH.
var Binary = "ffmpeg"

// ErrNoFit reports a clip that cannot fit under the limit at a watchable quality.
var ErrNoFit = errors.New("clip too long to fit under the size limit")

const (
	timeout      = 10 * time.Minute
	audioBitrate = 64_000  // bit/s
	minVideo     = 150_000 // bit/s: below, the picture is no longer worth watching
)

// attempts are the shares of the limit aimed at: x264's bitrate control is not
// exact, a result over the limit is encoded again lower.
var attempts = []float64{0.9, 0.75}

// Fit re-encodes in into a temporary MP4 file of at most max bytes and returns its
// path; the caller must remove it. in is left as is.
func Fit(ctx context.Context, in string, max int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d, err := duration(ctx, in)
	if err != nil {
		return "", err
	}
	for _, share := range attempts {
		video, err := videoBitrate(max, share, d)
		if err != nil {
			return "", err
		}
		out, err := encode(ctx, in, video)
		if err != nil {
			return "", err
		}
		st, err := os.Stat(out)
		if err == nil && st.Size() <= max {
			return out, nil
		}
		os.Remove(out)
		if err != nil {
			return "", err
		}
	}
	return "", ErrNoFit
}

// videoBitrate is the video bitrate (bit/s) that fills share of max bytes over d.
func videoBitrate(max int64, share float64, d time.Duration) (int64, error) {
	if d <= 0 {
		return 0, errors.New("clip without duration")
	}
	v := int64(float64(max)*8*share/d.Seconds()) - audioBitrate
	if v < minVideo {
		return 0, ErrNoFit
	}
	return v, nil
}

// height is the largest picture height worth the bitrate (bit/s), never upscaled.
func height(video int64) int {
	switch {
	case video >= 1_500_000:
		return 720
	case video >= 600_000:
		return 480
	default:
		return 360
	}
}

func args(in, out string, video int64) []string {
	b := strconv.FormatInt(video, 10)
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", in,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)'", height(video)),
		"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
		"-b:v", b, "-maxrate", b, "-bufsize", strconv.FormatInt(2*video, 10),
		"-c:a", "aac", "-b:a", strconv.Itoa(audioBitrate), "-ac", "1",
		"-movflags", "+faststart", out,
	}
}

func encode(ctx context.Context, in string, video int64) (string, error) {
	f, err := os.CreateTemp("", "clip-*.mp4")
	if err != nil {
		return "", err
	}
	out := f.Name()
	f.Close()
	if err := run(ctx, args(in, out, video)...); err != nil {
		os.Remove(out)
		return "", err
	}
	return out, nil
}

// duration reads the length of the video track by copying it to nowhere: fast
// (nothing is decoded), and right for the fragmented MP4 files of Frigate, whose
// header does not always carry it.
func duration(ctx context.Context, in string) (time.Duration, error) {
	cmd := exec.CommandContext(ctx, Binary, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", in,
		"-map", "0:v:0", "-c", "copy", "-f", "null", "-progress", "pipe:1", "-nostats", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseProgress(out)
}

// parseProgress returns the last out_time_us of ffmpeg's -progress output.
func parseProgress(b []byte) (time.Duration, error) {
	var d time.Duration
	found := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		v, ok := strings.CutPrefix(sc.Text(), "out_time_us=")
		if !ok {
			continue
		}
		if us, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && us > 0 {
			d, found = time.Duration(us)*time.Microsecond, true
		}
	}
	if !found {
		return 0, errors.New("clip without duration")
	}
	return d, nil
}

func run(ctx context.Context, a ...string) error {
	cmd := exec.CommandContext(ctx, Binary, a...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
