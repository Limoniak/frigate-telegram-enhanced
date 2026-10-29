package notifier

import (
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"frigate-telegram-enhanced/internal/telegram"
)

// sizedMP4 is the smallest MP4 that carries a video size: moov/trak/tkhd (version 0).
func sizedMP4(width, height uint32) []byte {
	tkhd := make([]byte, 8+84)
	binary.BigEndian.PutUint32(tkhd, uint32(len(tkhd)))
	copy(tkhd[4:], "tkhd")
	binary.BigEndian.PutUint32(tkhd[8+40:], 1<<16)     // matrix a
	binary.BigEndian.PutUint32(tkhd[8+56:], 1<<16)     // matrix d
	binary.BigEndian.PutUint32(tkhd[8+76:], width<<16) // 16.16
	binary.BigEndian.PutUint32(tkhd[8+80:], height<<16)
	wrap := func(typ string, body []byte) []byte {
		b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
		return append(append(b, typ...), body...)
	}
	return wrap("moov", wrap("trak", tkhd))
}

// TestClipFileCarriesSize checks that a clip goes to Telegram with its size, so that
// it is not squashed, and that a clip whose size cannot be read is still sent.
func TestClipFileCarriesSize(t *testing.T) {
	n := &Notifier{Log: slog.New(slog.DiscardHandler)}
	dir := t.TempDir()

	clip := filepath.Join(dir, "clip.mp4")
	os.WriteFile(clip, sizedMP4(1920, 1080), 0o600)
	want := telegram.InputFile{Name: "clip.mp4", Path: clip, Width: 1920, Height: 1080}
	if got := n.clipFile(clip); !reflect.DeepEqual(got, want) {
		t.Errorf("clipFile = %+v, want %+v", got, want)
	}

	other := filepath.Join(dir, "other.mp4")
	os.WriteFile(other, []byte("mp4data"), 0o600)
	want = telegram.InputFile{Name: "clip.mp4", Path: other}
	if got := n.clipFile(other); !reflect.DeepEqual(got, want) {
		t.Errorf("unreadable clip: %+v, want %+v", got, want)
	}
}
