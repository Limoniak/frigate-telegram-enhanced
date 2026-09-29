package mp4fix

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// box construit une boîte MP4 : taille, type, contenu.
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

// trak : tkhd (version 0) avec l'identifiant de piste, mdhd avec l'échelle de temps.
func trak(id, timescale uint32) []byte {
	tkhd := box("tkhd", u32(0, 0, 0, id, 0))                            // version/flags, dates, id, réservé
	mdhd := box("mdhd", u32(0, 0, 0, timescale, 0), []byte{0, 0, 0, 0}) // version/flags, dates, timescale, durée
	return box("trak", tkhd, box("mdia", mdhd))
}

// traf : tfhd + trun avec data_offset et une durée par échantillon.
func traf(id uint32, durations ...uint32) []byte {
	tfhd := box("tfhd", u32(0x020000, id))
	trun := box("trun", u32(0x000301, uint32(len(durations)), 0))
	for _, d := range durations {
		trun = append(trun, u32(d, 100)...) // durée, taille
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

// durations relit les durées du trun de la piste id dans le fichier.
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

// Le cas réel : premier échantillon audio décalé de 9 h 25 par la caméra.
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
		t.Fatalf("Fix = %d, %v ; attendu 1 échantillon corrigé", n, err)
	}
	got := durations(t, path, 2)
	if got[0] != 1024 || got[1] != 818 || got[4] != 1024 {
		t.Errorf("durées audio = %v, attendu la médiane (1024) à la place de la durée aberrante", got)
	}
	if v := durations(t, path, 1); len(v) != 5 || v[0] != 3000 {
		t.Errorf("la vidéo ne doit pas changer : %v", v)
	}
	after, _ := os.ReadFile(path)
	if len(after) != len(data) {
		t.Error("la taille du fichier ne doit pas changer")
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
		t.Errorf("clip sain : %d, %v", n, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, healthy) {
		t.Error("un clip sain ne doit pas être modifié")
	}

	// Un fichier qui n'est pas un MP4 (GIF, HTML d'erreur…) est refusé sans être touché.
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	path = write(t, gif)
	if n, err := Fix(path); n != 0 || err == nil {
		t.Errorf("fichier étranger : %d, %v ; attendu une erreur", n, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, gif) {
		t.Error("un fichier étranger ne doit pas être modifié")
	}
}
