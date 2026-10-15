// Package qrcode draws a QR code for a short text, such as the setup URL a
// phone scans to reach the Recording Agent. It encodes in byte mode with one
// error-correction block (versions 1 to 5), which holds up to 106 bytes:
// error-correction level M where it fits (version 3 and below), else L.
package qrcode

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a QR code's square of modules.
type Code struct {
	// Size is the number of modules on each side.
	Size    int
	modules [][]bool
}

// Dark reports whether the module at column x, row y is dark. Coordinates
// outside the code are light (the quiet zone).
func (c *Code) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.modules[y][x]
}

// level is an error-correction level: its two format bits.
type level struct {
	name       string
	formatBits int
}

var (
	levelL = level{name: "L", formatBits: 1}
	levelM = level{name: "M", formatBits: 0}
)

// layout is a version's single-block layout at one level.
type layout struct {
	version   int
	level     level
	data, ecc int // codewords
}

// layouts lists the single-block layouts in the order Encode tries them.
var layouts = []layout{
	{1, levelM, 16, 10}, {2, levelM, 28, 16}, {3, levelM, 44, 26},
	{4, levelL, 80, 20}, {5, levelL, 108, 26},
}

// alignmentCenter is the one alignment pattern of versions 2 to 6.
var alignmentCenter = map[int]int{2: 18, 3: 22, 4: 26, 5: 30}

// ErrTooLong is returned for a text no supported version holds.
var ErrTooLong = errors.New("qrcode: text is too long")

// Encode draws text as a QR code.
func Encode(text string) (*Code, error) {
	data := []byte(text)
	for _, l := range layouts {
		// Mode (4 bits) and count (8 bits), then the bytes.
		if 12+8*len(data) > 8*l.data {
			continue
		}
		return draw(l, codewords(l, data)), nil
	}
	return nil, ErrTooLong
}

// codewords is the data, padded to the layout's capacity, followed by its
// error-correction codewords.
func codewords(l layout, data []byte) []byte {
	var bits bitBuffer
	bits.append(0b0100, 4) // byte mode
	bits.append(len(data), 8)
	for _, b := range data {
		bits.append(int(b), 8)
	}
	capacity := 8 * l.data
	bits.append(0, min(4, capacity-len(bits)))
	bits.append(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capacity; pad ^= 0xEC ^ 0x11 {
		bits.append(pad, 8)
	}
	out := make([]byte, l.data, l.data+l.ecc)
	for i, bit := range bits {
		if bit {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return append(out, reedSolomon(out, l.ecc)...)
}

type bitBuffer []bool

func (b *bitBuffer) append(value, count int) {
	for i := count - 1; i >= 0; i-- {
		*b = append(*b, (value>>i)&1 == 1)
	}
}

// gfMultiply multiplies in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1.
func gfMultiply(a, b byte) byte {
	var product byte
	for i := 7; i >= 0; i-- {
		carry := product & 0x80
		product <<= 1
		if carry != 0 {
			product ^= 0x1D
		}
		if (b>>i)&1 == 1 {
			product ^= a
		}
	}
	return product
}

// reedSolomon is the error-correction codewords of data.
func reedSolomon(data []byte, degree int) []byte {
	// The generator polynomial (x - a^0)(x - a^1)...(x - a^(degree-1)),
	// highest coefficient (1) dropped.
	divisor := make([]byte, degree)
	divisor[degree-1] = 1
	root := byte(1)
	for range degree {
		for j := range divisor {
			divisor[j] = gfMultiply(divisor[j], root)
			if j+1 < degree {
				divisor[j] ^= divisor[j+1]
			}
		}
		root = gfMultiply(root, 2)
	}
	remainder := make([]byte, degree)
	for _, b := range data {
		factor := b ^ remainder[0]
		copy(remainder, remainder[1:])
		remainder[degree-1] = 0
		for i := range remainder {
			remainder[i] ^= gfMultiply(divisor[i], factor)
		}
	}
	return remainder
}

// matrix is a code being drawn: its modules and which of them are function
// patterns, which data and masks leave alone.
type matrix struct {
	size     int
	modules  [][]bool
	function [][]bool
}

func newMatrix(size int) *matrix {
	m := &matrix{size: size, modules: make([][]bool, size), function: make([][]bool, size)}
	for y := range size {
		m.modules[y] = make([]bool, size)
		m.function[y] = make([]bool, size)
	}
	return m
}

func (m *matrix) setFunction(x, y int, dark bool) {
	m.modules[y][x] = dark
	m.function[y][x] = true
}

func draw(l layout, words []byte) *Code {
	size := 4*l.version + 17
	m := newMatrix(size)
	for i := range size {
		m.setFunction(6, i, i%2 == 0)
		m.setFunction(i, 6, i%2 == 0)
	}
	for _, center := range [][2]int{{3, 3}, {size - 4, 3}, {3, size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := center[0]+dx, center[1]+dy
				if x < 0 || y < 0 || x >= size || y >= size {
					continue
				}
				distance := max(abs(dx), abs(dy))
				m.setFunction(x, y, distance != 2 && distance != 4)
			}
		}
	}
	if center, ok := alignmentCenter[l.version]; ok {
		for dy := -2; dy <= 2; dy++ {
			for dx := -2; dx <= 2; dx++ {
				m.setFunction(center+dx, center+dy, max(abs(dx), abs(dy)) != 1)
			}
		}
	}
	// Reserve the format areas (drawn per mask below) and the dark module.
	m.drawFormat(l.level, 0)
	m.placeData(words)

	best, bestPenalty := -1, 0
	for mask := range 8 {
		m.applyMask(mask)
		m.drawFormat(l.level, mask)
		if penalty := m.penalty(); best < 0 || penalty < bestPenalty {
			best, bestPenalty = mask, penalty
		}
		m.applyMask(mask) // masks are their own inverse
	}
	m.applyMask(best)
	m.drawFormat(l.level, best)
	return &Code{Size: size, modules: m.modules}
}

// formatBits is the 15-bit format information of a level and mask.
func formatBits(l level, mask int) int {
	data := l.formatBits<<3 | mask
	remainder := data
	for range 10 {
		remainder = remainder<<1 ^ (remainder>>9)*0x537
	}
	return (data<<10 | remainder) ^ 0x5412
}

func (m *matrix) drawFormat(l level, mask int) {
	bits := formatBits(l, mask)
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := range 6 {
		m.setFunction(8, i, bit(i))
	}
	m.setFunction(8, 7, bit(6))
	m.setFunction(8, 8, bit(7))
	m.setFunction(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.setFunction(14-i, 8, bit(i))
	}
	for i := range 8 {
		m.setFunction(m.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.setFunction(8, m.size-15+i, bit(i))
	}
	m.setFunction(8, m.size-8, true)
}

// placeData lays the codewords' bits in the zigzag of two-module columns,
// right to left, skipping function patterns and the vertical timing column.
func (m *matrix) placeData(words []byte) {
	i := 0
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vertical := range m.size {
			for j := range 2 {
				x := right - j
				y := vertical
				if (right+1)&2 == 0 {
					y = m.size - 1 - vertical
				}
				if m.function[y][x] || i >= len(words)*8 {
					continue
				}
				m.modules[y][x] = (words[i/8]>>(7-i%8))&1 == 1
				i++
			}
		}
	}
}

func (m *matrix) applyMask(mask int) {
	for y := range m.size {
		for x := range m.size {
			if m.function[y][x] || !maskCondition(mask, x, y) {
				continue
			}
			m.modules[y][x] = !m.modules[y][x]
		}
	}
}

func maskCondition(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	case 7:
		return ((x+y)%2+x*y%3)%2 == 0
	}
	panic(fmt.Sprintf("qrcode: mask %d", mask))
}

// penalty scores how hard a masked code is to read; the lowest wins.
func (m *matrix) penalty() int {
	score := 0
	line := func(get func(i int) bool) {
		run, color := 0, false
		for i := 0; i < m.size; i++ {
			if i > 0 && get(i) == color {
				run++
				continue
			}
			if run >= 5 {
				score += run - 2
			}
			run, color = 1, get(i)
		}
		if run >= 5 {
			score += run - 2
		}
		// Finder-like 1:1:3:1:1 runs with four light modules on one side.
		for i := 0; i+11 <= m.size; i++ {
			var pattern strings.Builder
			for j := range 11 {
				if get(i + j) {
					pattern.WriteByte('1')
				} else {
					pattern.WriteByte('0')
				}
			}
			if p := pattern.String(); p == "10111010000" || p == "00001011101" {
				score += 40
			}
		}
	}
	dark := 0
	for y := range m.size {
		line(func(x int) bool { return m.modules[y][x] })
		line(func(x int) bool { return m.modules[x][y] })
		for x := range m.size {
			if m.modules[y][x] {
				dark++
			}
			if x+1 < m.size && y+1 < m.size {
				c := m.modules[y][x]
				if c == m.modules[y][x+1] && c == m.modules[y+1][x] && c == m.modules[y+1][x+1] {
					score += 3
				}
			}
		}
	}
	total := m.size * m.size
	score += (abs(dark*20-total*10)+total-1)/total*10 - 10
	return score
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// SVG draws the code as an SVG image, each module scale units square, with
// the standard four-module quiet zone.
func (c *Code) SVG(scale int) string {
	if scale < 1 {
		scale = 1
	}
	side := (c.Size + 8) * scale
	var path strings.Builder
	for y := range c.Size {
		for x := range c.Size {
			if c.modules[y][x] {
				fmt.Fprintf(&path, "M%d %dh%dv%dh-%dz", (x+4)*scale, (y+4)*scale, scale, scale, scale)
			}
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges">`+
		`<rect width="100%%" height="100%%" fill="#fff"/><path fill="#000" d="%s"/></svg>`, side, side, side, side, path.String())
}
