// D185 profile patching — patches the camera's native 625-byte profile.
// Port of filmkit/src/profile/d185.ts.
package profile

import "encoding/binary"

// ConversionParams holds user-facing conversion parameters.
// Only set fields are applied — unset fields preserve the camera's EXIF sentinels.
type ConversionParams struct {
	FilmSimulation    *int
	ExposureBias      *int // millistops (e.g. 1000 = +1.0 EV)
	HighlightTone     *int // -4 to +4
	ShadowTone        *int
	Color             *int
	Sharpness         *int
	NoiseReduction    *int // -4 to +4
	Clarity           *int // -5 to +5
	DynamicRange      *int // enum 1/2/3
	WhiteBalance      *int
	WBShiftR          *int
	WBShiftB          *int
	WBColorTemp       *int
	GrainEffect       *int
	SmoothSkinEffect  *int
	WideDRange        *int
	ColorChromeEffect *int
	ColorChromeFxBlue *int
}

// Native d185 profile field indices (camera's 625-byte format).
// Confirmed via X100VI test images (2026-03).
const (
	nativeIdxExposureBias  = 5
	nativeIdxDynamicRange  = 6  // raw %: 100/200/400
	nativeIdxWideDRange    = 7
	nativeIdxFilmSim       = 8
	nativeIdxGrainEffect   = 9  // flat enum: 1=Off 2=WkSm 3=StrSm 4=WkLg 5=StrLg
	nativeIdxColorChrome   = 10 // 1-indexed: 1=Off 2=Weak 3=Strong
	nativeIdxSmoothSkin    = 11
	nativeIdxWhiteBalance  = 12 // 0 = use EXIF
	nativeIdxWBShiftR      = 13
	nativeIdxWBShiftB      = 14
	nativeIdxWBColorTemp   = 15
	nativeIdxHighlight     = 16 // ×10
	nativeIdxShadow        = 17 // ×10
	nativeIdxColor         = 18 // ×10
	nativeIdxSharpness     = 19 // ×10
	nativeIdxNoiseReduce   = 20 // proprietary encoding (not ×10)
	nativeIdxCCFxBlue      = 25 // 1-indexed
	nativeIdxClarity       = 27 // ×10
)

var grainToNative = map[int]int{
	GrainOff:         1,
	GrainWeakSmall:   2,
	GrainStrongSmall: 3,
	GrainWeakLarge:   4,
	GrainStrongLarge: 5,
}

var drToNative = map[int]int{
	DR100: 100,
	DR200: 200,
	DR400: 400,
}

// PatchProfile patches the camera's native base profile with user changes.
// Only fields with non-nil pointers in changes are modified.
// Returns a new []byte — the original is not modified.
func PatchProfile(baseProfile []byte, changes ConversionParams) []byte {
	patched := make([]byte, len(baseProfile))
	copy(patched, baseProfile)

	numParams := int(binary.LittleEndian.Uint16(patched[0:2]))
	offset := len(patched) - numParams*4

	set := func(idx, val int) {
		binary.LittleEndian.PutUint32(patched[offset+idx*4:], uint32(int32(val)))
	}

	if changes.FilmSimulation != nil {
		set(nativeIdxFilmSim, *changes.FilmSimulation)
	}
	if changes.ExposureBias != nil {
		set(nativeIdxExposureBias, *changes.ExposureBias)
	}
	if changes.DynamicRange != nil {
		if v, ok := drToNative[*changes.DynamicRange]; ok {
			set(nativeIdxDynamicRange, v)
		}
	}
	if changes.WideDRange != nil {
		set(nativeIdxWideDRange, *changes.WideDRange)
	}
	if changes.GrainEffect != nil {
		if v, ok := grainToNative[*changes.GrainEffect]; ok {
			set(nativeIdxGrainEffect, v)
		}
	}
	// Effects: UI 0/1/2 → native 1-indexed 1/2/3
	if changes.ColorChromeEffect != nil {
		set(nativeIdxColorChrome, *changes.ColorChromeEffect+1)
	}
	if changes.ColorChromeFxBlue != nil {
		set(nativeIdxCCFxBlue, *changes.ColorChromeFxBlue+1)
	}
	if changes.SmoothSkinEffect != nil {
		set(nativeIdxSmoothSkin, *changes.SmoothSkinEffect+1)
	}
	if changes.WhiteBalance != nil {
		set(nativeIdxWhiteBalance, *changes.WhiteBalance)
	}
	if changes.WBShiftR != nil {
		set(nativeIdxWBShiftR, *changes.WBShiftR)
	}
	if changes.WBShiftB != nil {
		set(nativeIdxWBShiftB, *changes.WBShiftB)
	}
	if changes.WBColorTemp != nil {
		set(nativeIdxWBColorTemp, *changes.WBColorTemp)
	}
	// Tone params: UI integer × 10
	if changes.HighlightTone != nil {
		set(nativeIdxHighlight, *changes.HighlightTone*10)
	}
	if changes.ShadowTone != nil {
		set(nativeIdxShadow, *changes.ShadowTone*10)
	}
	if changes.Color != nil {
		set(nativeIdxColor, *changes.Color*10)
	}
	if changes.Sharpness != nil {
		set(nativeIdxSharpness, *changes.Sharpness*10)
	}
	// NR: proprietary encoding via lookup table
	if changes.NoiseReduction != nil {
		if encoded, ok := NREncode[*changes.NoiseReduction]; ok {
			set(nativeIdxNoiseReduce, encoded)
		}
	}
	if changes.Clarity != nil {
		set(nativeIdxClarity, *changes.Clarity*10)
	}

	return patched
}
