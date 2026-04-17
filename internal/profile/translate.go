// Translates between camera preset properties (D18E–D1A5) and UI values.
// Port of filmkit/src/profile/preset-translate.ts.
package profile

import (
	"encoding/binary"

	"filmkit-daemon/internal/ptp"
	"filmkit-daemon/internal/util"
)

// PresetUIValues holds UI-ready values for a preset.
type PresetUIValues struct {
	FilmSimulation    int     `json:"filmSimulation"`
	DynamicRange      int     `json:"dynamicRange"`
	GrainEffect       int     `json:"grainEffect"`
	SmoothSkin        int     `json:"smoothSkin"`
	ColorChrome       int     `json:"colorChrome"`
	ColorChromeFxBlue int     `json:"colorChromeFxBlue"`
	WhiteBalance      int     `json:"whiteBalance"`
	WBShiftR          int     `json:"wbShiftR"`
	WBShiftB          int     `json:"wbShiftB"`
	WBColorTemp       int     `json:"wbColorTemp"`
	HighlightTone     float64 `json:"highlightTone"`
	ShadowTone        float64 `json:"shadowTone"`
	Color             float64 `json:"color"`
	Sharpness         float64 `json:"sharpness"`
	NoiseReduction    int     `json:"noiseReduction"`
	Clarity           float64 `json:"clarity"`
	Exposure          float64 `json:"exposure"`
	DRangePriority    int     `json:"dRangePriority"`
	MonoWC            float64 `json:"monoWC"`
	MonoMG            float64 `json:"monoMG"`
}

var PresetDefaults = PresetUIValues{
	WBColorTemp: 6500,
}

// NR decode/encode tables — Fuji proprietary encoding (not ×10).
var NRDecode = map[int]int{
	0x8000: -4, 0x7000: -3, 0x4000: -2, 0x3000: -1,
	0x2000: 0, 0x1000: 1, 0x0000: 2, 0x6000: 3, 0x5000: 4,
}

// NREncode is exported (used by d185.go).
var NREncode = map[int]int{
	-4: 0x8000, -3: 0x7000, -2: 0x4000, -1: 0x3000,
	0: 0x2000, 1: 0x1000, 2: 0x0000, 3: 0x6000, 4: 0x5000,
}

var drMap = map[int]int{100: 1, 200: 2, 400: 3}

var grainMap = map[int]int{
	1: GrainOff,
	2: GrainWeakSmall,
	3: GrainStrongSmall,
	4: GrainWeakLarge,
	5: GrainStrongLarge,
}

// Unknown property defaults (from Wireshark captures).
var unknownDefaults = map[uint16]uint16{
	0xD18E: 7,      // ImageSize (L 3:2)
	0xD18F: 4,      // ImageQuality
	0xD191: 0,      // Unknown
	0xD1A1: 0x4000, // HighIsoNR sentinel
	0xD1A3: 1,      // LongExpNR = On
	0xD1A4: 1,      // ColorSpace = sRGB
	0xD1A5: 7,      // Unknown
}

// propVal finds a property value by ID. Returns (value, found).
func propVal(settings []ptp.RawProp, id uint16) (int, bool) {
	for _, p := range settings {
		if p.ID != id {
			continue
		}
		switch v := p.Value.(type) {
		case int:
			return v, true
		}
	}
	return 0, false
}

// decodeTone converts a ×10-encoded tone value; 0x8000/-32768 sentinel → 0.
func decodeTone(raw int) float64 {
	if raw == 0x8000 || raw == -32768 {
		return 0
	}
	return float64(raw) / 10
}

func decodeNR(raw int) int {
	u16 := raw & 0xFFFF
	if v, ok := NRDecode[u16]; ok {
		return v
	}
	return 0
}

// TranslatePresetToUI converts camera preset properties to UI values.
func TranslatePresetToUI(settings []ptp.RawProp) PresetUIValues {
	v := PresetDefaults

	if fs, ok := propVal(settings, 0xD192); ok {
		v.FilmSimulation = fs
	}
	if dr, ok := propVal(settings, 0xD190); ok {
		if mapped, ok := drMap[dr]; ok {
			v.DynamicRange = mapped
		}
	}
	if grain, ok := propVal(settings, 0xD195); ok {
		if mapped, ok := grainMap[grain]; ok {
			v.GrainEffect = mapped
		}
	}

	// Effects: camera 1=Off,2=Weak,3=Strong → UI 0/1/2
	if skin, ok := propVal(settings, 0xD198); ok {
		if skin > 0 {
			v.SmoothSkin = skin - 1
		}
	}
	if cc, ok := propVal(settings, 0xD196); ok {
		if cc > 0 {
			v.ColorChrome = cc - 1
		}
	}
	if ccb, ok := propVal(settings, 0xD197); ok {
		if ccb > 0 {
			v.ColorChromeFxBlue = ccb - 1
		}
	}

	// MonoWC/MonoMG — ×10, only for B&W sims
	if MonochromeSims[v.FilmSimulation] {
		if wc, ok := propVal(settings, 0xD193); ok {
			v.MonoWC = float64(wc) / 10
		}
		if mg, ok := propVal(settings, 0xD194); ok {
			v.MonoMG = float64(mg) / 10
		}
	}

	// WB: mask to uint16 (decodePropValue reads as int16)
	if wb, ok := propVal(settings, 0xD199); ok {
		v.WhiteBalance = wb & 0xFFFF
	}
	if r, ok := propVal(settings, 0xD19A); ok {
		v.WBShiftR = r
	}
	if b, ok := propVal(settings, 0xD19B); ok {
		v.WBShiftB = b
	}
	if ct, ok := propVal(settings, 0xD19C); ok && ct > 0 {
		v.WBColorTemp = ct
	}

	// ×10 tone params
	if ht, ok := propVal(settings, 0xD19D); ok {
		v.HighlightTone = decodeTone(ht)
	}
	if st, ok := propVal(settings, 0xD19E); ok {
		v.ShadowTone = decodeTone(st)
	}
	if col, ok := propVal(settings, 0xD19F); ok {
		v.Color = decodeTone(col)
	}
	if shp, ok := propVal(settings, 0xD1A0); ok {
		v.Sharpness = decodeTone(shp)
	}
	if nr, ok := propVal(settings, 0xD1A1); ok {
		v.NoiseReduction = decodeNR(nr)
	}
	if cla, ok := propVal(settings, 0xD1A2); ok {
		v.Clarity = decodeTone(cla)
	}

	return v
}

// CameraProfileToUIValues extracts PresetUIValues from a native d185 profile.
func CameraProfileToUIValues(profileData []byte) PresetUIValues {
	numParams := int(binary.LittleEndian.Uint16(profileData[0:2]))
	offset := len(profileData) - numParams*4

	p := func(idx int) int {
		return int(int32(binary.LittleEndian.Uint32(profileData[offset+idx*4:])))
	}

	drRaw := p(nativeIdxDynamicRange)
	cc := p(nativeIdxColorChrome)
	skin := p(nativeIdxSmoothSkin)
	ccBlue := p(nativeIdxCCFxBlue)

	v := PresetUIValues{
		FilmSimulation: p(nativeIdxFilmSim),
		WBShiftR:       p(nativeIdxWBShiftR),
		WBShiftB:       p(nativeIdxWBShiftB),
		HighlightTone:  decodeTone(p(nativeIdxHighlight)),
		ShadowTone:     decodeTone(p(nativeIdxShadow)),
		Color:          decodeTone(p(nativeIdxColor)),
		Sharpness:      decodeTone(p(nativeIdxSharpness)),
		NoiseReduction: decodeNR(p(nativeIdxNoiseReduce)),
		Clarity:        decodeTone(p(nativeIdxClarity)),
		Exposure:       float64(p(nativeIdxExposureBias)) / 1000,
		DRangePriority: p(nativeIdxWideDRange),
	}

	if mapped, ok := drMap[drRaw]; ok {
		v.DynamicRange = mapped
	}
	if mapped, ok := grainMap[p(nativeIdxGrainEffect)]; ok {
		v.GrainEffect = mapped
	}
	if cc > 0 {
		v.ColorChrome = cc - 1
	}
	if skin > 0 {
		v.SmoothSkin = skin - 1
	}
	if ccBlue > 0 {
		v.ColorChromeFxBlue = ccBlue - 1
	}

	ct := p(nativeIdxWBColorTemp)
	if ct > 0 {
		v.WBColorTemp = ct
	} else {
		v.WBColorTemp = 6500
	}

	return v
}

// TranslateUIToPresetProps converts UI values to camera preset properties for writing.
func TranslateUIToPresetProps(values PresetUIValues, base []ptp.RawProp) []ptp.RawProp {
	baseMap := make(map[uint16]*ptp.RawProp)
	for i := range base {
		baseMap[base[i].ID] = &base[i]
	}

	makeRaw := func(propID uint16, computedBytes []byte) ptp.RawProp {
		bytes := computedBytes
		if bytes == nil {
			if b, ok := baseMap[propID]; ok {
				bytes = b.Bytes
			} else {
				def := unknownDefaults[propID]
				bytes = util.PackU16(def)
			}
		}
		return ptp.RawProp{ID: propID, Name: "", Bytes: bytes}
	}

	isMono := MonochromeSims[values.FilmSimulation]
	var props []ptp.RawProp

	props = append(props, makeRaw(0xD18E, nil))
	props = append(props, makeRaw(0xD18F, nil))

	drPreset := map[int]uint16{1: 100, 2: 200, 3: 400}[values.DynamicRange]
	if drPreset == 0 {
		drPreset = 100
	}
	props = append(props, makeRaw(0xD190, util.PackU16(drPreset)))
	props = append(props, makeRaw(0xD191, nil))

	filmSim := values.FilmSimulation
	if filmSim == 0 {
		filmSim = 1
	}
	props = append(props, makeRaw(0xD192, util.PackU16(uint16(filmSim))))

	// D193/D194: MonoWC/MonoMG — only for B&W sims, camera rejects writing 0
	if isMono && values.MonoWC != 0 {
		props = append(props, makeRaw(0xD193, util.PackI16(int16(round(values.MonoWC*10)))))
	}
	if isMono && values.MonoMG != 0 {
		props = append(props, makeRaw(0xD194, util.PackI16(int16(round(values.MonoMG*10)))))
	}

	grainPreset := map[int]uint16{
		GrainOff: 1, GrainWeakSmall: 2, GrainStrongSmall: 3,
		GrainWeakLarge: 4, GrainStrongLarge: 5,
	}[values.GrainEffect]
	if grainPreset == 0 {
		grainPreset = 1
	}
	props = append(props, makeRaw(0xD195, util.PackU16(grainPreset)))
	props = append(props, makeRaw(0xD196, util.PackU16(uint16(values.ColorChrome+1))))
	props = append(props, makeRaw(0xD197, util.PackU16(uint16(values.ColorChromeFxBlue+1))))
	props = append(props, makeRaw(0xD198, util.PackU16(uint16(values.SmoothSkin+1))))
	props = append(props, makeRaw(0xD199, util.PackU16(uint16(values.WhiteBalance))))

	// D19C: WB Color Temp — only when WB mode is ColorTemp
	if values.WhiteBalance == WBColorTemp && values.WBColorTemp > 0 {
		props = append(props, makeRaw(0xD19C, util.PackU16(uint16(values.WBColorTemp))))
	}

	props = append(props, makeRaw(0xD19A, util.PackI16(int16(values.WBShiftR))))
	props = append(props, makeRaw(0xD19B, util.PackI16(int16(values.WBShiftB))))
	props = append(props, makeRaw(0xD19D, util.PackI16(int16(round(values.HighlightTone*10)))))
	props = append(props, makeRaw(0xD19E, util.PackI16(int16(round(values.ShadowTone*10)))))

	// D19F: Color — only for non-monochrome
	if !isMono {
		props = append(props, makeRaw(0xD19F, util.PackI16(int16(round(values.Color*10)))))
	}

	props = append(props, makeRaw(0xD1A0, util.PackI16(int16(round(values.Sharpness*10)))))

	if encoded, ok := NREncode[values.NoiseReduction]; ok {
		props = append(props, makeRaw(0xD1A1, util.PackU16(uint16(encoded))))
	} else {
		props = append(props, makeRaw(0xD1A1, nil))
	}

	props = append(props, makeRaw(0xD1A2, util.PackI16(int16(round(values.Clarity*10)))))
	props = append(props, makeRaw(0xD1A3, nil))
	props = append(props, makeRaw(0xD1A4, nil))
	props = append(props, makeRaw(0xD1A5, nil))

	return props
}

// round rounds a float64 to the nearest integer.
func round(f float64) int {
	if f < 0 {
		return int(f - 0.5)
	}
	return int(f + 0.5)
}
