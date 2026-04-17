// Package util provides little-endian binary pack/unpack helpers.
// This is a port of filmkit/src/util/binary.ts.
package util

import (
	"encoding/binary"
	"unicode/utf16"
)

// PTPReader is a cursor-based parser for PTP data structures.
type PTPReader struct {
	data []byte
	pos  int
}

func NewPTPReader(data []byte) *PTPReader {
	return &PTPReader{data: data}
}

func (r *PTPReader) Remaining() int { return len(r.data) - r.pos }

func (r *PTPReader) U8() uint8 {
	v := r.data[r.pos]
	r.pos++
	return v
}

func (r *PTPReader) U16() uint16 {
	v := binary.LittleEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v
}

func (r *PTPReader) U32() uint32 {
	v := binary.LittleEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return v
}

func (r *PTPReader) I8() int8   { return int8(r.U8()) }
func (r *PTPReader) I16() int16 { return int16(r.U16()) }
func (r *PTPReader) I32() int32 { return int32(r.U32()) }

// Str reads a PTP string: uint8 numChars (incl. null) + numChars×UCS-2LE.
func (r *PTPReader) Str() string {
	numChars := int(r.U8())
	if numChars == 0 {
		return ""
	}
	chars := make([]uint16, numChars)
	for i := 0; i < numChars; i++ {
		chars[i] = r.U16()
	}
	// Strip null terminator
	if len(chars) > 0 && chars[len(chars)-1] == 0 {
		chars = chars[:len(chars)-1]
	}
	return string(utf16.Decode(chars))
}

// U16Array reads a PTP uint16 array: uint32 count, then count×uint16.
func (r *PTPReader) U16Array() []uint16 {
	count := r.U32()
	arr := make([]uint16, count)
	for i := range arr {
		arr[i] = r.U16()
	}
	return arr
}

// U32Array reads a PTP uint32 array: uint32 count, then count×uint32.
func (r *PTPReader) U32Array() []uint32 {
	count := r.U32()
	arr := make([]uint32, count)
	for i := range arr {
		arr[i] = r.U32()
	}
	return arr
}

// Pack helpers

func PackU16(v uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	return b
}

func PackI16(v int16) []byte { return PackU16(uint16(v)) }

func PackU32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func PackI32(v int32) []byte { return PackU32(uint32(v)) }

func UnpackU16(data []byte, offset int) uint16 {
	return binary.LittleEndian.Uint16(data[offset:])
}

func UnpackU32(data []byte, offset int) uint32 {
	return binary.LittleEndian.Uint32(data[offset:])
}

// PackPTPString packs a Go string as a PTP string:
// length byte (including null) + UTF-16LE chars + null terminator.
func PackPTPString(s string) []byte {
	if s == "" {
		return []byte{0}
	}
	encoded := utf16.Encode([]rune(s))
	result := make([]byte, 0, 1+len(encoded)*2+2)
	result = append(result, byte(len(encoded)+1)) // length including null
	for _, ch := range encoded {
		result = append(result, PackU16(ch)...)
	}
	result = append(result, 0, 0) // null terminator
	return result
}
