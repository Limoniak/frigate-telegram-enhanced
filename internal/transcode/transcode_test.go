package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestParseProgress(t *testing.T) {
	out := []byte("frame=10\nout_time_us=1000000\nprogress=continue\nframe=20\nout_time_us=148040000\nout_time=00:02:28.040000\nprogress=end\n")
	d, err := parseProgress(out)
	if err != nil || d != 148040*time.Millisecond {
		t.Fatalf("d, err = %v, %v; want 2m28.04s", d, err)
	}
	if _, err := parseProgress([]byte("out_time_us=N/A\nprogress=end\n")); err == nil {
		t.Error("no duration: want an error")
	}
}

func TestVideoBitrate(t *testing.T) {
	// The clip of the bug report: 70 MB over 2 min 28 s, to bring under 50 MiB.
	v, err := videoBitrate(50<<20, 0.9, 148*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if v < 2_400_000 || v > 2_600_000 {
		t.Errorf("video = %d bit/s, want about 2.5 Mbit/s", v)
	}
	if height(v) != 720 {
		t.Errorf("height = %d, want 720", height(v))
	}
	if _, err := videoBitrate(50<<20, 0.9, 2*time.Hour); !errors.Is(err, ErrNoFit) {
		t.Errorf("2 h clip: err = %v, want ErrNoFit", err)
	}
	if _, err := videoBitrate(50<<20, 0.9, 0); err == nil {
		t.Error("no duration: want an error")
	}
}

func TestHeight(t *testing.T) {
	for _, c := range []struct {
		video int64
		want  int
	}{{3_000_000, 720}, {1_500_000, 720}, {1_000_000, 480}, {600_000, 480}, {300_000, 360}} {
		if got := height(c.video); got != c.want {
			t.Errorf("height(%d) = %d, want %d", c.video, got, c.want)
		}
	}
}

func TestArgs(t *testing.T) {
	a := args("in.mp4", "out.mp4", 1_000_000)
	for _, want := range [][]string{
		{"-i", "in.mp4"},
		{"-map", "0:a:0?"}, // a camera without sound is not an error
		{"-vf", "scale=-2:'min(480,ih)'"},
		{"-b:v", "1000000"},
		{"-bufsize", "2000000"},
	} {
		if !hasPair(a, want[0], want[1]) {
			t.Errorf("args lack %v: %v", want, a)
		}
	}
	if a[len(a)-1] != "out.mp4" {
		t.Errorf("last arg = %q, want the output", a[len(a)-1])
	}
}

func hasPair(a []string, k, v string) bool {
	for i := range len(a) - 1 {
		if a[i] == k && a[i+1] == v {
			return true
		}
	}
	return false
}

// TestFit runs the real ffmpeg; skipped where it is not installed.
func TestFit(t *testing.T) {
	if _, err := exec.LookPath(Binary); err != nil {
		t.Skip("ffmpeg not installed")
	}
	in := filepath.Join(t.TempDir(), "in.mp4")
	// 20 s of noise at a high bitrate, fragmented like Frigate's clips.
	gen := exec.Command(Binary, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=15",
		"-f", "lavfi", "-i", "sine", "-t", "20", "-vf", "noise=alls=60:allf=t",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "8M", "-c:a", "aac",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof", in)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generating the clip: %v: %s", err, out)
	}
	st, err := os.Stat(in)
	if err != nil {
		t.Fatal(err)
	}
	max := st.Size() / 3
	out, err := Fit(context.Background(), in, max)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out)
	got, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size() > max || got.Size() < max/4 {
		t.Errorf("size = %d, want under %d and not much less", got.Size(), max)
	}
	if d, err := duration(context.Background(), out); err != nil || d < 19*time.Second {
		t.Errorf("duration = %v, %v; want about 20 s", d, err)
	}
}

func TestRemux(t *testing.T) {
	if _, err := exec.LookPath(Binary); err != nil {
		t.Skip("ffmpeg not installed")
	}
	in := filepath.Join(t.TempDir(), "in.mp4")
	// fragmented like Frigate's clips
	gen := exec.Command(Binary, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine", "-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof", in)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generating the clip: %v: %s", err, out)
	}
	out, err := Remux(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out)
	if got := topBoxes(t, out); !slices.Contains(got, "moov") || slices.Contains(got, "moof") ||
		slices.Index(got, "moov") > slices.Index(got, "mdat") {
		t.Errorf("boxes = %v, want a plain MP4 with moov before mdat", got)
	}
	if d, err := duration(context.Background(), out); err != nil || d < 2900*time.Millisecond {
		t.Errorf("duration = %v, %v; want about 3 s", d, err)
	}
	if _, err := os.Stat(in); err != nil {
		t.Errorf("input removed: %v", err)
	}
}

// topBoxes lists the types of the top-level boxes of an MP4 file.
func topBoxes(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for len(data) >= 8 {
		size := int(binary.BigEndian.Uint32(data))
		out = append(out, string(data[4:8]))
		if size < 8 || size > len(data) {
			break
		}
		data = data[size:]
	}
	return out
}

func TestThumbnail(t *testing.T) {
	if _, err := exec.LookPath(Binary); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// a 1280x720 snapshot, the size Frigate's detection gives
	snapshot, err := exec.Command(Binary, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x720",
		"-frames:v", "1", "-f", "image2", "-c:v", "mjpeg", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	thumb, err := Thumbnail(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	c, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || c.Width != 320 || c.Height != 180 || len(thumb) >= maxThumbnail {
		t.Errorf("thumbnail %dx%d, %d bytes, %v; want a 320x180 JPEG under 200 kB", c.Width, c.Height, len(thumb), err)
	}
	if _, err := Thumbnail(context.Background(), []byte("not an image")); err == nil {
		t.Error("no error for a broken image")
	}
}
