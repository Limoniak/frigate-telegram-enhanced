package mp4fix

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// box builds an MP4 box: size, type, content.
func box(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}

func u32(vs ...uint32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.BigEndian.PutUint32(b[4*i:], v)
	}
	return b
}

// trak: tkhd (version 0) with the track ID, mdhd with the timescale.
func trak(id, timescale uint32) []byte {
	tkhd := box("tkhd", u32(0, 0, 0, id, 0))                            // version/flags, dates, id, reserved
	mdhd := box("mdhd", u32(0, 0, 0, timescale, 0), []byte{0, 0, 0, 0}) // version/flags, dates, timescale, duration
	return box("trak", tkhd, box("mdia", mdhd))
}

// traf: tfhd + trun with data_offset and a duration per sample.
func traf(id uint32, durations ...uint32) []byte {
	tfhd := box("tfhd", u32(0x020000, id))
	trun := box("trun", u32(0x000301, uint32(len(durations)), 0))
	for _, d := range durations {
		trun = append(trun, u32(d, 100)...) // duration, size
	}
	binary.BigEndian.PutUint32(trun, uint32(len(trun)))
	return box("traf", tfhd, trun)
}

func write(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// durations reads back the durations of the trun of track id in the file.
func durations(t *testing.T, path string, id uint32) []uint32 {
	t.Helper()
	data, _ := os.ReadFile(path)
	var out []uint32
	children(data, func(typ string, moof []byte) {
		if typ != "moof" {
			return
		}
		children(moof, func(typ string, tf []byte) {
			var track uint32
			children(tf, func(typ string, b []byte) {
				if typ == "tfhd" {
					track = binary.BigEndian.Uint32(b[4:])
				}
				if typ == "trun" && track == id {
					n := int(binary.BigEndian.Uint32(b[4:]))
					for i := range n {
						out = append(out, binary.BigEndian.Uint32(b[12+8*i:]))
					}
				}
			})
		})
	})
	return out
}

// The real case: first audio sample shifted by 9 h 25 by the camera.
func TestFixClampsAbsurdSampleDuration(t *testing.T) {
	const audio = 16000
	data := bytes.Join([][]byte{
		box("ftyp", []byte("isom")),
		box("moov", trak(1, 90000), trak(2, audio)),
		box("moof", traf(1, 3000, 3000, 3000)),
		box("mdat", make([]byte, 64)),
		box("moof", traf(1, 3000, 3000), traf(2, 542569022, 818, 809, 1874, 1024)),
		box("mdat", make([]byte, 64)),
	}, nil)
	path := write(t, data)

	n, err := Fix(path)
	if err != nil || n != 1 {
		t.Fatalf("Fix = %d, %v; want 1 sample fixed", n, err)
	}
	got := durations(t, path, 2)
	if got[0] != 1024 || got[1] != 818 || got[4] != 1024 {
		t.Errorf("audio durations = %v, want the median (1024) instead of the aberrant duration", got)
	}
	if v := durations(t, path, 1); len(v) != 5 || v[0] != 3000 {
		t.Errorf("the video must not change: %v", v)
	}
	after, _ := os.ReadFile(path)
	if len(after) != len(data) {
		t.Error("the file size must not change")
	}
}

func TestFixLeavesHealthyAndForeignFilesAlone(t *testing.T) {
	healthy := bytes.Join([][]byte{
		box("ftyp", []byte("isom")),
		box("moov", trak(1, 90000), trak(2, 16000)),
		box("moof", traf(1, 3000, 3000), traf(2, 1024, 1024)),
		box("mdat", make([]byte, 16)),
	}, nil)
	path := write(t, healthy)
	if n, err := Fix(path); n != 0 || err != nil {
		t.Errorf("healthy clip: %d, %v", n, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, healthy) {
		t.Error("a healthy clip must not be modified")
	}

	// A file that is not an MP4 (GIF, error HTML…) is refused without being touched.
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	path = write(t, gif)
	if n, err := Fix(path); n != 0 || err == nil {
		t.Errorf("foreign file: %d, %v; want an error", n, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, gif) {
		t.Error("a foreign file must not be modified")
	}
}

// The real case: Frigate stops sending the clip 1,479 bytes before the end of the
// last fragment. Trim only keeps the complete fragments.
func TestTrimDropsIncompleteFragment(t *testing.T) {
	head := bytes.Join([][]byte{
		box("ftyp", []byte("isom")),
		box("moov", trak(1, 90000)),
		box("moof", traf(1, 3000, 3000)),
		box("mdat", make([]byte, 64)),
	}, nil)
	last := append(box("moof", traf(1, 3000)), box("mdat", make([]byte, 64))...)
	join := func(a, b []byte) []byte { return append(slices.Clone(a), b...) }

	cases := map[string]struct {
		data      []byte
		wantLen   int
		wantFrags int
	}{
		"truncated mdat":    {join(head, last[:len(last)-10]), len(head), 1},
		"moof without mdat": {join(head, box("moof", traf(1, 3000))), len(head), 1},
		"header cut":        {join(head, last[:5]), len(head), 1},
		"complet":           {join(head, last), len(head) + len(last), 2},
	}
	for name, tc := range cases {
		path := write(t, tc.data)
		if n, err := Trim(path); err != nil || n != tc.wantFrags {
			t.Errorf("%s: Trim = %d, %v; want %d fragments", name, n, err, tc.wantFrags)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(after, tc.data[:tc.wantLen]) {
			t.Errorf("%s: %d bytes kept, want %d", name, len(after), tc.wantLen)
		}
	}

	// Nothing usable: no complete fragment, or not an MP4.
	for _, data := range [][]byte{head[:len(head)-10], []byte("GIF89a\x01\x00\x01\x00")} {
		if n, _ := Trim(write(t, data)); n != 0 {
			t.Errorf("Trim(%q…) = %d, want 0", data[:8], n)
		}
	}
}
