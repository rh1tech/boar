// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"strings"
	"unicode/utf8"

	"boar/internal/term"
)

// Charset is a message character set as named by its CHRS control line
// (FTS-5003). Messages are stored as UTF-8; these convert at the edges.
type Charset int

const (
	ASCII Charset = iota
	CP437         // IBM PC: international echoes, box-drawing art
	CP866         // Russian echoes (RU.*, SU.*)
	Latin1
	UTF8
)

// chrsNames maps CHRS identifiers to charsets. "IBMPC" is the old name for
// CP437 and "+7_FIDO" the Russian network's name for CP866.
var chrsNames = map[string]Charset{
	"ASCII": ASCII, "CP437": CP437, "IBMPC": CP437,
	"CP866": CP866, "+7_FIDO": CP866, "+7": CP866,
	"LATIN-1": Latin1, "ISO-8859-1": Latin1, "UTF-8": UTF8,
}

// ParseCHRS reads the value of a CHRS (or CHARSET) line, such as "CP866 2".
// Unknown sets report false, and the caller keeps its default.
func ParseCHRS(v string) (Charset, bool) {
	name, _, _ := strings.Cut(strings.TrimSpace(v), " ")
	cs, ok := chrsNames[strings.ToUpper(name)]
	return cs, ok
}

// CHRS is the control line value to write for cs: identifier and level.
func (cs Charset) CHRS() string {
	switch cs {
	case CP437:
		return "CP437 2"
	case CP866:
		return "CP866 2"
	case Latin1:
		return "LATIN-1 2"
	case UTF8:
		return "UTF-8 4" // FRL-1021
	default:
		return "ASCII 1"
	}
}

func (cs Charset) String() string {
	name, _, _ := strings.Cut(cs.CHRS(), " ")
	return name
}

// cp866High is bytes 0x80-0xFF of code page 866.
var cp866High = []rune(
	"АБВГДЕЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯ" +
		"абвгдежзийклмноп" +
		"░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀" +
		"рстуфхцчшщъыьэюя" +
		"ЁёЄєЇїЎў°∙·√№¤■ ")

var cp866Encode = func() map[rune]byte {
	m := make(map[rune]byte, 128)
	for i, r := range cp866High {
		m[r] = byte(0x80 + i)
	}
	return m
}()

var cp437Encode = func() map[rune]byte {
	m := make(map[rune]byte, 128)
	for b := 0x80; b <= 0xff; b++ {
		m[term.DecodeCP437(byte(b))] = byte(b)
	}
	return m
}()

// Decode turns message text in cs into UTF-8. Invalid UTF-8 in a message
// that claims UTF-8 is replaced rather than passed on.
func Decode(data []byte, cs Charset) string {
	switch cs {
	case UTF8:
		return strings.ToValidUTF8(string(data), "�")
	case CP437:
		var b strings.Builder
		for _, c := range data {
			if c < 0x80 {
				b.WriteByte(c)
			} else {
				b.WriteRune(term.DecodeCP437(c))
			}
		}
		return b.String()
	case CP866:
		var b strings.Builder
		for _, c := range data {
			if c < 0x80 {
				b.WriteByte(c)
			} else {
				b.WriteRune(cp866High[c-0x80])
			}
		}
		return b.String()
	case Latin1:
		var b strings.Builder
		for _, c := range data {
			b.WriteRune(rune(c))
		}
		return b.String()
	default: // ASCII: the high half has no meaning
		var b strings.Builder
		for _, c := range data {
			if c < 0x80 {
				b.WriteByte(c)
			} else {
				b.WriteByte('?')
			}
		}
		return b.String()
	}
}

// Encode turns UTF-8 text into cs. Characters the set cannot hold become '?'.
func Encode(s string, cs Charset) []byte {
	if cs == UTF8 {
		return []byte(s)
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case cs == Latin1 && r <= 0xff:
			out = append(out, byte(r))
		case cs == CP437:
			out = append(out, lookup(cp437Encode, r))
		case cs == CP866:
			out = append(out, lookup(cp866Encode, r))
		default:
			out = append(out, '?')
		}
	}
	return out
}

func lookup(m map[rune]byte, r rune) byte {
	if b, ok := m[r]; ok {
		return b
	}
	return '?'
}

// Fits reports whether s survives Encode in cs without losing characters.
func Fits(s string, cs Charset) bool {
	if cs == UTF8 {
		return utf8.ValidString(s)
	}
	for _, r := range s {
		if r < 0x80 {
			continue
		}
		switch cs {
		case Latin1:
			if r > 0xff {
				return false
			}
		case CP437:
			if _, ok := cp437Encode[r]; !ok {
				return false
			}
		case CP866:
			if _, ok := cp866Encode[r]; !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}
