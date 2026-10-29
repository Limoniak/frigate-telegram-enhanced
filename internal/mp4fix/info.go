package mp4fix

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
)

// Info returns the size of path's video as it is shown, and its duration in whole
// seconds (0 when unknown). Telegram is given them with a clip: left to work them out
// from a fragmented upload such as Frigate's clips, it records the video as 320x320,
// shown squashed, and its duration as 0.
func Info(path string) (width, height, seconds int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, 0, 0, err
	}

	var (
		moov            bool
		video           uint32                // track ID of the video
		scales          = map[uint32]uint32{} // track → ticks per second
		trex            = map[uint32]uint32{} // track → default sample duration
		scale, duration uint32                // mvhd: 0 in a fragmented file
		ticks           uint64                // the video samples of the fragments
	)
	for off := int64(0); off+8 <= st.Size(); {
		typ, hdr, size, err := header(f, off, st.Size())
		if err != nil {
			return 0, 0, 0, err
		}
		if typ == "moov" || typ == "moof" && video != 0 {
			if size > maxBox {
				return 0, 0, 0, errors.New(typ + " box too large")
			}
			buf := make([]byte, size)
			if _, err := f.ReadAt(buf, off); err != nil {
				return 0, 0, 0, err
			}
			if typ == "moof" {
				ticks += videoTicks(buf[hdr:], video, trex[video])
			} else {
				moov = true
				readTimescales(buf[hdr:], scales)
				children(buf[hdr:], func(typ string, b []byte) {
					switch typ {
					case "mvhd": // version 1 (64-bit duration) is left out: Frigate writes version 0
						if o, ok := fieldAfterDates(b); ok && b[0] == 0 && len(b) >= o+8 {
							scale, duration = binary.BigEndian.Uint32(b[o:]), binary.BigEndian.Uint32(b[o+4:])
						}
					case "trak": // the video track is the one with a size: sound tracks have none
						children(b, func(typ string, t []byte) {
							if o, ok := fieldAfterDates(t); typ == "tkhd" && ok && width == 0 {
								if width, height = displaySize(t); width > 0 {
									video = binary.BigEndian.Uint32(t[o:])
								}
							}
						})
					case "mvex":
						children(b, func(typ string, t []byte) {
							if typ == "trex" && len(t) >= 16 { // version/flags, track_ID, description, duration
								trex[binary.BigEndian.Uint32(t[4:])] = binary.BigEndian.Uint32(t[12:])
							}
						})
					}
				})
			}
		}
		off += size
	}
	switch {
	case !moov:
		return 0, 0, 0, errors.New("no moov box")
	case width == 0 || height == 0:
		return 0, 0, 0, errors.New("no video track")
	case duration > 0 && scale > 0:
		seconds = int(math.Round(float64(duration) / float64(scale)))
	case ticks > 0 && scales[video] > 0:
		seconds = int(math.Round(float64(ticks) / float64(scales[video])))
	}
	return width, height, seconds, nil
}

// videoTicks adds up the durations of a moof's samples of track video: each sample's
// own, or else the default of its tfhd, or else dflt (from trex).
func videoTicks(moof []byte, video, dflt uint32) (ticks uint64) {
	children(moof, func(typ string, traf []byte) {
		if typ != "traf" {
			return
		}
		track, def := uint32(0), dflt
		children(traf, func(typ string, b []byte) {
			switch typ {
			case "tfhd":
				if len(b) < 8 {
					return
				}
				flags := uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
				track = binary.BigEndian.Uint32(b[4:])
				p := 8
				if flags&0x1 != 0 { // base_data_offset
					p += 8
				}
				if flags&0x2 != 0 { // sample_description_index
					p += 4
				}
				if flags&0x8 != 0 && len(b) >= p+4 { // default_sample_duration
					def = binary.BigEndian.Uint32(b[p:])
				}
			case "trun":
				if track != video {
					return
				}
				n, off, per, durations := trunLayout(b)
				if !durations {
					ticks += uint64(n) * uint64(def)
					return
				}
				for i := range n {
					ticks += uint64(binary.BigEndian.Uint32(b[off+i*per:]))
				}
			}
		})
	})
	return ticks
}

// displaySize returns a tkhd's width and height as shown. They already account for
// non-square pixels; a quarter-turn matrix (rotated video) swaps them.
func displaySize(tkhd []byte) (width, height int) {
	o, ok := fieldAfterDates(tkhd)
	if !ok {
		return 0, 0
	}
	// after the dates: track_ID, reserved, duration (4 bytes, 8 in version 1), 16
	// reserved/layer/volume bytes, then the 3x3 matrix, then width and height
	matrix := o + 4 + 4 + 4 + 16
	if tkhd[0] == 1 {
		matrix += 4
	}
	at := matrix + 36
	if len(tkhd) < at+8 {
		return 0, 0
	}
	fixed := func(i int) int { // unsigned 16.16 fixed point, rounded
		return int((uint64(binary.BigEndian.Uint32(tkhd[i:])) + 1<<15) >> 16)
	}
	width, height = fixed(at), fixed(at+4)
	a, b := int32(binary.BigEndian.Uint32(tkhd[matrix:])), int32(binary.BigEndian.Uint32(tkhd[matrix+4:]))
	if a == 0 && (b == 1<<16 || b == -1<<16) {
		width, height = height, width
	}
	return width, height
}
