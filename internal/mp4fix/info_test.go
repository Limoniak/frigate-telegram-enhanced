package mp4fix

import (
	"path/filepath"
	"slices"
	"testing"
)

const one = 1 << 16 // 1.0 in 16.16 fixed point

// sizedTrak: tkhd (version 0) with a matrix (a, b) and a 16.16 size, mdhd with the timescale.
func sizedTrak(id uint32, a, b int32, width, height, timescale uint32) []byte {
	tkhd := u32(0, 0, 0, id, 0, 0, 0, 0, 0, 0) // version/flags, dates, id, reserved, duration, reserved, layer/volume
	tkhd = append(tkhd, u32(uint32(a), uint32(b), 0, uint32(-b), uint32(a), 0, 0, 0, 1<<30)...)
	mdhd := box("mdhd", u32(0, 0, 0, timescale, 0), []byte{0, 0, 0, 0})
	return box("trak", box("tkhd", tkhd, u32(width, height)), box("mdia", mdhd))
}

// video and sound: Frigate's tracks, 1 and 2.
func video(w, h uint32) []byte { return sizedTrak(1, one, 0, w<<16, h<<16, 12800) }
func sound() []byte            { return sizedTrak(2, one, 0, 0, 0, 48000) }

func mvhd(timescale, duration uint32) []byte {
	return box("mvhd", u32(0, 0, 0, timescale, duration)) // version/flags, dates, timescale, duration
}

func repeat(d uint32, n int) []uint32 { return slices.Repeat([]uint32{d}, n) }

// defaultTraf: a traf whose samples take their duration from tfhd (dflt != 0) or trex.
func defaultTraf(id, dflt uint32, samples int) []byte {
	tfhd := box("tfhd", u32(0x020000, id))
	if dflt != 0 {
		tfhd = box("tfhd", u32(0x020008, id, dflt)) // default-base-is-moof, default_sample_duration
	}
	return box("traf", tfhd, box("trun", u32(0x000001, uint32(samples), 0))) // data_offset only
}

func trex(id, dflt uint32) []byte {
	return box("mvex", box("trex", u32(0, id, 1, dflt, 0, 0)))
}

func TestInfo(t *testing.T) {
	// 5 s at 25 fps: 125 samples of 512 ticks at 12800 per second, in two fragments
	moofs := box("moof", traf(1, repeat(512, 75)...), traf(2, repeat(1024, 140)...))
	moofs = append(moofs, box("moof", traf(1, repeat(512, 50)...), traf(2, repeat(1024, 95)...))...)
	cases := []struct {
		name           string
		moov, rest     []byte
		w, h, duration int
	}{
		{"fragmented, like Frigate", box("moov", mvhd(1000, 0), video(1920, 1080), sound()), moofs, 1920, 1080, 5},
		{"tfhd defaults", box("moov", mvhd(1000, 0), video(1920, 1080)), box("moof", defaultTraf(1, 512, 125)), 1920, 1080, 5},
		{"trex defaults", box("moov", mvhd(1000, 0), video(1920, 1080), trex(1, 512)), box("moof", defaultTraf(1, 0, 125)), 1920, 1080, 5},
		{"no fragments", box("moov", mvhd(1000, 0), video(1920, 1080)), nil, 1920, 1080, 0},
		{"mvhd duration", box("moov", mvhd(90000, 270000), video(1280, 720)), nil, 1280, 720, 3},
		{"rotated", box("moov", mvhd(1000, 2000), sizedTrak(1, 0, one, 1920<<16, 1080<<16, 1000)), nil, 1080, 1920, 2},
		{"sound first", box("moov", mvhd(1000, 2000), sound(), video(640, 360)), nil, 640, 360, 2},
		{"rounded", box("moov", mvhd(1000, 0), sizedTrak(1, one, 0, 1920<<16-1, 1080<<16+1, 1000)), nil, 1920, 1080, 0},
	}
	for _, c := range cases {
		file := append(box("ftyp", []byte("isom")), c.moov...)
		file = append(append(file, c.rest...), box("mdat", make([]byte, 64))...)
		w, h, d, err := Info(write(t, file))
		if err != nil || w != c.w || h != c.h || d != c.duration {
			t.Errorf("%s: %dx%d %ds, %v; want %dx%d %ds", c.name, w, h, d, err, c.w, c.h, c.duration)
		}
	}
}

func TestInfoNotAVideo(t *testing.T) {
	for name, data := range map[string][]byte{
		"text":       []byte("not an mp4 file"),
		"empty":      {},
		"truncated":  {0, 0, 0, 100, 'm', 'o', 'o', 'v'},
		"no moov":    box("mdat", make([]byte, 16)),
		"sound only": box("moov", mvhd(1000, 2000), sound()),
	} {
		if w, h, _, err := Info(write(t, data)); err == nil {
			t.Errorf("%s: %dx%d without an error", name, w, h)
		}
	}
	if _, _, _, err := Info(filepath.Join(t.TempDir(), "missing.mp4")); err == nil {
		t.Error("missing file: no error")
	}
}
