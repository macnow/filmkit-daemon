// PTP Container — the fundamental unit of PTP/USB communication.
// Port of filmkit/src/ptp/container.ts.
package ptp

import (
	"fmt"

	"filmkit-daemon/internal/util"
)

const headerSize = 12

// Container represents a PTP message (command, data, or response).
type Container struct {
	Type          uint16
	Code          uint16
	TransactionID uint32
	Params        []uint32
	Data          []byte
}

// Pack serializes a Container into bytes for USB transmission.
//
// Layout:
//
//	[0-3]  uint32 LE: total length
//	[4-5]  uint16 LE: container type
//	[6-7]  uint16 LE: operation/response code
//	[8-11] uint32 LE: transaction ID
//	[12+]  up to 5 params (uint32 each) OR raw data payload
func Pack(c Container) []byte {
	paramCount := len(c.Params)
	if paramCount > 5 {
		paramCount = 5
	}
	totalLen := uint32(headerSize + paramCount*4 + len(c.Data))

	result := make([]byte, 0, totalLen)
	result = append(result, util.PackU32(totalLen)...)
	result = append(result, util.PackU16(c.Type)...)
	result = append(result, util.PackU16(c.Code)...)
	result = append(result, util.PackU32(c.TransactionID)...)
	for i := 0; i < paramCount; i++ {
		result = append(result, util.PackU32(c.Params[i])...)
	}
	result = append(result, c.Data...)
	return result
}

// Unpack deserializes a Container from USB response bytes.
//
// DATA containers: everything after header is payload (no params).
// RESPONSE containers: up to 5 uint32 params after header (no payload).
func Unpack(raw []byte) (Container, error) {
	if len(raw) < headerSize {
		return Container{}, fmt.Errorf("container too short: %d bytes", len(raw))
	}

	_ = util.UnpackU32(raw, 0) // total length (not needed after read)
	ctype := util.UnpackU16(raw, 4)
	code := util.UnpackU16(raw, 6)
	tid := util.UnpackU32(raw, 8)

	rest := raw[headerSize:]
	var params []uint32
	var data []byte

	switch ctype {
	case ContainerData:
		data = rest
	case ContainerResponse:
		for offset := 0; offset+4 <= len(rest) && len(params) < 5; offset += 4 {
			params = append(params, util.UnpackU32(rest, offset))
		}
	}

	return Container{
		Type:          ctype,
		Code:          code,
		TransactionID: tid,
		Params:        params,
		Data:          data,
	}, nil
}

// Length returns the total length field from a raw container's first 4 bytes.
func Length(raw []byte) uint32 {
	if len(raw) < 4 {
		return 0
	}
	return util.UnpackU32(raw, 0)
}
