// Package mp4fix répare, sur place, les durées d'échantillons aberrantes des MP4
// fragmentés que produit Frigate.
//
// Certaines caméras envoient un horodatage audio décalé de plusieurs heures au début
// d'un enregistrement. Frigate le recopie tel quel : dans le clip, le premier
// échantillon audio « dure » alors 9 h, et Telegram affiche une vidéo de 9 h dont la
// lecture est cassée. Un clip d'événement ne contient jamais d'échantillon de plus de
// quelques dixièmes de seconde : toute durée au-delà de maxSample est ramenée à la
// durée habituelle de la piste (la médiane du fragment). Seuls ces champs de 4 octets
// changent : ni réencodage, ni changement de taille, son et image conservés.
package mp4fix

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
)

// maxSample est la durée au-delà de laquelle un échantillon est tenu pour faux.
const maxSample = 10 // secondes

// maxBox borne la taille d'une boîte moov ou moof lue en mémoire.
const maxBox = 16 << 20

// Fix répare le fichier path et renvoie le nombre d'échantillons corrigés. Un fichier
// qui n'est pas un MP4 fragmenté est laissé tel quel (0, nil).
func Fix(path string) (int, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}

	timescales := map[uint32]uint32{} // piste → tics par seconde
	fixed := 0
	for off := int64(0); off+8 <= st.Size(); {
		typ, hdr, size, err := header(f, off, st.Size())
		if err != nil {
			return fixed, err
		}
		switch typ {
		case "moov", "moof":
			if size > maxBox {
				return fixed, fmt.Errorf("%s box too large (%d bytes)", typ, size)
			}
			buf := make([]byte, size)
			if _, err := f.ReadAt(buf, off); err != nil {
				return fixed, err
			}
			if typ == "moov" {
				readTimescales(buf[hdr:], timescales)
				break
			}
			if n := fixMoof(buf[hdr:], timescales); n > 0 {
				if _, err := f.WriteAt(buf, off); err != nil {
					return fixed, err
				}
				fixed += n
			}
		}
		off += size
	}
	return fixed, nil
}

// header lit l'en-tête de la boîte à off : type, taille de l'en-tête, taille totale.
func header(f *os.File, off, fileSize int64) (string, int64, int64, error) {
	var b [16]byte
	if _, err := f.ReadAt(b[:8], off); err != nil {
		return "", 0, 0, err
	}
	size, hdr := int64(binary.BigEndian.Uint32(b[:4])), int64(8)
	switch size {
	case 0: // jusqu'à la fin du fichier
		size = fileSize - off
	case 1:
		if _, err := f.ReadAt(b[8:16], off+8); err != nil {
			return "", 0, 0, err
		}
		size, hdr = int64(binary.BigEndian.Uint64(b[8:16])), 16
	}
	if size < hdr || off+size > fileSize {
		return "", 0, 0, errors.New("malformed MP4 box")
	}
	return string(b[4:8]), hdr, size, nil
}

// children parcourt les boîtes contenues dans buf.
func children(buf []byte, fn func(typ string, body []byte)) {
	for len(buf) >= 8 {
		size, hdr := uint64(binary.BigEndian.Uint32(buf)), uint64(8)
		if size == 1 && len(buf) >= 16 {
			size, hdr = binary.BigEndian.Uint64(buf[8:]), 16
		} else if size == 0 {
			size = uint64(len(buf))
		}
		if size < hdr || size > uint64(len(buf)) {
			return
		}
		fn(string(buf[4:8]), buf[hdr:size])
		buf = buf[size:]
	}
}

// readTimescales relève l'échelle de temps de chaque piste (moov/trak/{tkhd,mdia/mdhd}).
func readTimescales(moov []byte, out map[uint32]uint32) {
	children(moov, func(typ string, trak []byte) {
		if typ != "trak" {
			return
		}
		var id, scale uint32
		children(trak, func(typ string, b []byte) {
			switch typ {
			case "tkhd":
				if o, ok := fieldAfterDates(b); ok {
					id = binary.BigEndian.Uint32(b[o:])
				}
			case "mdia":
				children(b, func(typ string, m []byte) {
					if o, ok := fieldAfterDates(m); typ == "mdhd" && ok {
						scale = binary.BigEndian.Uint32(m[o:])
					}
				})
			}
		})
		if id != 0 && scale != 0 {
			out[id] = scale
		}
	})
}

// fieldAfterDates renvoie la position du champ qui suit les dates de création et de
// modification d'une boîte tkhd ou mdhd : 4 octets en version 0, 8 en version 1.
func fieldAfterDates(b []byte) (int, bool) {
	if len(b) < 1 {
		return 0, false
	}
	o := map[byte]int{0: 12, 1: 20}[b[0]]
	return o, o > 0 && len(b) >= o+4
}

// fixMoof corrige les durées aberrantes des trun d'un moof ; modifie moof en place.
func fixMoof(moof []byte, timescales map[uint32]uint32) int {
	fixed := 0
	children(moof, func(typ string, traf []byte) {
		if typ != "traf" {
			return
		}
		var track uint32
		children(traf, func(typ string, b []byte) {
			switch typ {
			case "tfhd":
				if len(b) >= 8 {
					track = binary.BigEndian.Uint32(b[4:])
				}
			case "trun":
				if scale := timescales[track]; scale != 0 {
					fixed += fixTrun(b, uint64(scale)*maxSample)
				}
			}
		})
	})
	return fixed
}

// fixTrun ramène à la médiane les durées d'échantillon supérieures à limit (en tics).
func fixTrun(b []byte, limit uint64) int {
	if len(b) < 8 {
		return 0
	}
	flags := uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if flags&0x100 == 0 { // pas de durée par échantillon
		return 0
	}
	n := int(binary.BigEndian.Uint32(b[4:]))
	off := 8
	if flags&0x1 != 0 { // data_offset
		off += 4
	}
	if flags&0x4 != 0 { // first_sample_flags
		off += 4
	}
	per := 0
	for _, f := range []uint32{0x100, 0x200, 0x400, 0x800} {
		if flags&f != 0 {
			per += 4
		}
	}
	if n <= 0 || off+n*per > len(b) {
		return 0
	}

	var normal []uint32
	for i := range n {
		if d := binary.BigEndian.Uint32(b[off+i*per:]); uint64(d) <= limit {
			normal = append(normal, d)
		}
	}
	if len(normal) == n {
		return 0
	}
	repl := uint32(limit / maxSample / 10) // un dixième de seconde, faute de mieux
	if len(normal) > 0 {
		slices.Sort(normal)
		repl = normal[len(normal)/2]
	}
	fixed := 0
	for i := range n {
		p := off + i*per
		if uint64(binary.BigEndian.Uint32(b[p:])) > limit {
			binary.BigEndian.PutUint32(b[p:], repl)
			fixed++
		}
	}
	return fixed
}
