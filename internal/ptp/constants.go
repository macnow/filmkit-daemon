// Package ptp implements the PTP (Picture Transfer Protocol) over USB.
// This is a port of filmkit/src/ptp/constants.ts.
package ptp

import "fmt"

// PTP standard operation codes (ISO 15740)
const (
	OpGetDeviceInfo      = uint16(0x1001)
	OpOpenSession        = uint16(0x1002)
	OpCloseSession       = uint16(0x1003)
	OpGetStorageIDs      = uint16(0x1004)
	OpGetStorageInfo     = uint16(0x1005)
	OpGetNumObjects      = uint16(0x1006)
	OpGetObjectHandles   = uint16(0x1007)
	OpGetObjectInfo      = uint16(0x1008)
	OpGetObject          = uint16(0x1009)
	OpGetThumb           = uint16(0x100A)
	OpDeleteObject       = uint16(0x100B)
	OpSendObjectInfo     = uint16(0x100C)
	OpSendObject         = uint16(0x100D)
	OpGetDevicePropDesc  = uint16(0x1014)
	OpGetDevicePropValue = uint16(0x1015)
	OpSetDevicePropValue = uint16(0x1016)
)

// Fujifilm vendor-specific operation codes
const (
	FujiOpSendObjectInfo = uint16(0x900C)
	FujiOpSendObject2    = uint16(0x900D)
)

// PTP response codes
const (
	RespOK                     = uint16(0x2001)
	RespGeneralError           = uint16(0x2002)
	RespSessionNotOpen         = uint16(0x2003)
	RespInvalidTransactionID   = uint16(0x2004)
	RespOperationNotSupported  = uint16(0x2005)
	RespParameterNotSupported  = uint16(0x2006)
	RespIncompleteTransfer     = uint16(0x2007)
	RespInvalidStorageID       = uint16(0x2008)
	RespInvalidObjectHandle    = uint16(0x2009)
	RespDevicePropNotSupported = uint16(0x200A)
	RespSessionAlreadyOpen     = uint16(0x201E)
)

// PTP container types
const (
	ContainerCommand  = uint16(0x0001)
	ContainerData     = uint16(0x0002)
	ContainerResponse = uint16(0x0003)
	ContainerEvent    = uint16(0x0004)
)

// Fujifilm device property codes
const (
	PropRawConvProfile     = uint16(0xD185)
	PropStartRawConversion = uint16(0xD183)
	PropPresetSlot         = uint16(0xD18C)
	PropPresetName         = uint16(0xD18D)
	PropPresetFirst        = uint16(0xD18E)
	PropPresetLast         = uint16(0xD1A5)
)

// USB identifiers
const FujiVendorID = uint16(0x04CB)

var FujiProductIDs = []uint16{
	0x02E3, // X-T30
	0x02E5, // X100V
	0x02E7, // X-T4
	0x0305, // X100VI
}

// PropNames maps property IDs to human-readable names.
var PropNames = map[uint16]string{
	0xD18C: "PresetSlot",
	0xD18D: "PresetName",
	0xD18E: "P:ImageSize",
	0xD18F: "P:ImageQuality",
	0xD190: "P:DynamicRange%",
	0xD191: "P:?D191",
	0xD192: "P:FilmSimulation",
	0xD193: "P:MonoWC×10",
	0xD194: "P:MonoMG×10",
	0xD195: "P:GrainEffect",
	0xD196: "P:ColorChrome",
	0xD197: "P:ColorChromeFxBlue",
	0xD198: "P:SmoothSkin",
	0xD199: "P:WhiteBalance",
	0xD19A: "P:WBShiftR",
	0xD19B: "P:WBShiftB",
	0xD19C: "P:ColorTemp(K)",
	0xD19D: "P:HighlightTone×10",
	0xD19E: "P:ShadowTone×10",
	0xD19F: "P:Color×10",
	0xD1A0: "P:Sharpness×10",
	0xD1A1: "P:HighIsoNR?",
	0xD1A2: "P:Clarity×10",
	0xD1A3: "P:LongExpNR",
	0xD1A4: "P:ColorSpace",
	0xD1A5: "P:?D1A5",
}

// RespName returns a human-readable name for a PTP response code.
func RespName(code uint16) string {
	switch code {
	case RespOK:
		return "OK"
	case RespGeneralError:
		return "GeneralError"
	case RespSessionNotOpen:
		return "SessionNotOpen"
	case RespInvalidTransactionID:
		return "InvalidTransactionID"
	case RespOperationNotSupported:
		return "OperationNotSupported"
	case RespParameterNotSupported:
		return "ParameterNotSupported"
	case RespIncompleteTransfer:
		return "IncompleteTransfer"
	case RespInvalidStorageID:
		return "InvalidStorageID"
	case RespInvalidObjectHandle:
		return "InvalidObjectHandle"
	case RespDevicePropNotSupported:
		return "DevicePropNotSupported"
	case RespSessionAlreadyOpen:
		return "SessionAlreadyOpen"
	default:
		return fmt.Sprintf("0x%04X", code)
	}
}

// PropName returns a human-readable name for a property ID.
func PropName(id uint16) string {
	if name, ok := PropNames[id]; ok {
		return name
	}
	return fmt.Sprintf("0x%04X", id)
}
