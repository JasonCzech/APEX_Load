package qrcode

import (
	"bytes"
	"strings"
	"testing"
)

// The format information of mask 0 at each level, from ISO/IEC 18004.
func TestFormatBits(t *testing.T) {
	for _, tc := range []struct {
		level level
		want  int
	}{
		{levelL, 0b111011111000100},
		{levelM, 0b101010000010010},
	} {
		if got := formatBits(tc.level, 0); got != tc.want {
			t.Fatalf("format bits %s/0 = %015b, want %015b", tc.level.name, got, tc.want)
		}
	}
}

// The error-correction codewords of the standard's version 1-M example
// ("01234567" in numeric mode).
func TestReedSolomon(t *testing.T) {
	data := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	want := []byte{0xA5, 0x24, 0xD4, 0xC1, 0xED, 0x36, 0xC7, 0x87, 0x2C, 0x55}
	if got := reedSolomon(data, 10); !bytes.Equal(got, want) {
		t.Fatalf("ecc = % X, want % X", got, want)
	}
}

// Reading a code back (format, unmask, zigzag) gives the codewords it was
// drawn from, for each version Encode chooses.
func TestEncodeReadsBack(t *testing.T) {
	for _, text := range []string{
		"http://10.0.0.2:8/",
		"http://192.168.1.20:8888/",
		"http://192.168.100.200:65535/setup",
		strings.Repeat("a", 60),
		strings.Repeat("b", 106),
	} {
		code, err := Encode(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		var l layout
		for _, candidate := range layouts {
			if 12+8*len(text) <= 8*candidate.data {
				l = candidate
				break
			}
		}
		if code.Size != 4*l.version+17 {
			t.Fatalf("%q: size %d, want version %d", text, code.Size, l.version)
		}
		mask := -1
		for candidate := range 8 {
			bits := formatBits(l.level, candidate)
			match := true
			for i := range 8 {
				if code.Dark(code.Size-1-i, 8) != ((bits>>i)&1 == 1) {
					match = false
				}
			}
			if match {
				mask = candidate
			}
		}
		if mask < 0 {
			t.Fatalf("%q: no format information matches", text)
		}
		// Rebuild the function-pattern map and read the data back.
		m := newMatrix(code.Size)
		ref := newMatrix(code.Size)
		for i := range code.Size {
			ref.setFunction(6, i, false)
			ref.setFunction(i, 6, false)
		}
		for _, center := range [][2]int{{3, 3}, {code.Size - 4, 3}, {3, code.Size - 4}} {
			for dy := -4; dy <= 4; dy++ {
				for dx := -4; dx <= 4; dx++ {
					x, y := center[0]+dx, center[1]+dy
					if x >= 0 && y >= 0 && x < code.Size && y < code.Size {
						ref.function[y][x] = true
					}
				}
			}
		}
		if center, ok := alignmentCenter[l.version]; ok {
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					ref.function[center+dy][center+dx] = true
				}
			}
		}
		ref.drawFormat(l.level, mask)
		for y := range code.Size {
			copy(m.modules[y], code.modules[y])
			copy(m.function[y], ref.function[y])
		}
		m.applyMask(mask)
		words := make([]byte, l.data+l.ecc)
		i := 0
		for right := code.Size - 1; right >= 1; right -= 2 {
			if right == 6 {
				right = 5
			}
			for vertical := range code.Size {
				for j := range 2 {
					x, y := right-j, vertical
					if (right+1)&2 == 0 {
						y = code.Size - 1 - vertical
					}
					if m.function[y][x] || i >= len(words)*8 {
						continue
					}
					if m.modules[y][x] {
						words[i/8] |= 1 << (7 - i%8)
					}
					i++
				}
			}
		}
		if !bytes.Equal(words[l.data:], reedSolomon(words[:l.data], l.ecc)) {
			t.Fatalf("%q: error correction does not match the data read back", text)
		}
		if words[0]>>4 != 0b0100 || int(words[0]&0x0F)<<4|int(words[1]>>4) != len(text) {
			t.Fatalf("%q: mode or count read back wrong: % X", text, words[:2])
		}
		var got []byte
		for k := range len(text) {
			got = append(got, words[1+k]<<4|words[2+k]>>4)
		}
		if string(got) != text {
			t.Fatalf("read back %q, want %q", got, text)
		}
	}
	if _, err := Encode(strings.Repeat("c", 107)); err != ErrTooLong {
		t.Fatalf("107 bytes: %v, want ErrTooLong", err)
	}
}

func TestSVG(t *testing.T) {
	code, err := Encode("http://192.168.1.20:8888/")
	if err != nil {
		t.Fatal(err)
	}
	svg := code.SVG(4)
	if !strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 132 132"`) || !strings.Contains(svg, "M16 16h4v4h-4z") {
		t.Fatalf("svg = %.200s", svg)
	}
}
