// SPDX-License-Identifier: GPL-3.0-or-later

// Package term prepares BBS text for a remote terminal: pipe color codes,
// character-set encoding (UTF-8, CP437, ASCII) and text sanitising.
package term

import (
	"fmt"
	"unicode/utf8"
)

// Charset is the character encoding used on the wire.
type Charset int

const (
	UTF8 Charset = iota
	CP437
	ASCII
)

func (c Charset) String() string {
	switch c {
	case UTF8:
		return "UTF-8"
	case CP437:
		return "CP437"
	case ASCII:
		return "ASCII"
	default:
		return fmt.Sprintf("Charset(%d)", int(c))
	}
}

// cp437High lists the glyphs for bytes 0x80-0xFF, in order.
const cp437High = "ÇüéâäàåçêëèïîìÄÅ" +
	"ÉæÆôöòûùÿÖÜ¢£¥₧ƒ" +
	"áíóúñÑªº¿⌐¬½¼¡«»" +
	"░▒▓│┤╡╢╖╕╣║╗╝╜╛┐" +
	"└┴┬├─┼╞╟╚╔╩╦╠═╬╧" +
	"╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀" +
	"αßΓπΣσµτΦΘΩδ∞φε∩" +
	"≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ "

var (
	cp437Runes [128]rune
	cp437Bytes = make(map[rune]byte, 128)
)

func init() {
	n := 0
	for _, r := range cp437High {
		if n >= len(cp437Runes) {
			panic("term: cp437 table has more than 128 entries")
		}
		cp437Runes[n] = r
		cp437Bytes[r] = byte(0x80 + n)
		n++
	}
	if n != len(cp437Runes) {
		panic(fmt.Sprintf("term: cp437 table has %d entries, want 128", n))
	}
}

// asciiFallback approximates box-drawing and typographic glyphs in 7-bit ASCII.
var asciiFallback = func() map[rune]byte {
	m := map[rune]byte{
		'─': '-', '━': '-', '═': '=', '│': '|', '┃': '|', '║': '|',
		'█': '#', '▓': '#', '▒': '%', '░': '.', '▄': '=', '▀': '=',
		'▌': '|', '▐': '|', '■': '*', '·': '.', '∙': '.', '•': '*',
		'»': '>', '«': '<', '√': 'v', '…': '.', '–': '-', '—': '-',
		'‘': '\'', '’': '\'', '“': '"', '”': '"', ' ': ' ',
	}
	for _, r := range "┌┐└┘├┤┬┴┼╔╗╚╝╠╣╦╩╬╒╓╕╖╘╙╛╜╞╟╡╢╤╥╧╨╪╫" {
		m[r] = '+'
	}
	return m
}()

// DecodeCP437 returns the Unicode glyph for a CP437 byte.
func DecodeCP437(b byte) rune {
	if b < 0x80 {
		return rune(b)
	}
	return cp437Runes[b-0x80]
}

// Encode converts UTF-8 text to the bytes a terminal using cs expects.
// Glyphs the charset cannot show are approximated or replaced with '?'.
func Encode(cs Charset, s string) []byte {
	if cs == UTF8 {
		return []byte(s)
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			out = append(out, byte(r))
			continue
		}
		if cs == CP437 {
			if b, ok := cp437Bytes[r]; ok {
				out = append(out, b)
				continue
			}
		}
		out = append(out, fallback(r))
	}
	return out
}

func fallback(r rune) byte {
	if b, ok := asciiFallback[r]; ok {
		return b
	}
	return '?'
}

// TranscodeCP437 converts CP437 output (from a door program, say) for a
// terminal. It works byte by byte, so it is safe on arbitrary chunks, and
// leaves control bytes and escape sequences alone.
func TranscodeCP437(data []byte, cs Charset) []byte {
	if cs == CP437 {
		return data
	}
	out := make([]byte, 0, len(data)+len(data)/4)
	for _, b := range data {
		switch {
		case b < 0x80:
			out = append(out, b)
		case cs == UTF8:
			out = utf8.AppendRune(out, DecodeCP437(b))
		default:
			out = append(out, fallback(DecodeCP437(b)))
		}
	}
	return out
}
