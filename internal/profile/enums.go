// Package profile handles Fujifilm camera profile encoding and translation.
// Port of filmkit/src/profile/enums.ts.
package profile

// Film simulation values
const (
	FilmSimProvia        = 0x01
	FilmSimVelvia        = 0x02
	FilmSimAstia         = 0x03
	FilmSimProNegHi      = 0x04
	FilmSimProNegStd     = 0x05
	FilmSimMonochrome    = 0x06
	FilmSimMonochromeYe  = 0x07
	FilmSimMonochromeR   = 0x08
	FilmSimMonochromeG   = 0x09
	FilmSimSepia         = 0x0A
	FilmSimClassicChrome = 0x0B
	FilmSimAcros         = 0x0C
	FilmSimAcrosYe       = 0x0D
	FilmSimAcrosR        = 0x0E
	FilmSimAcrosG        = 0x0F
	FilmSimEterna        = 0x10
	FilmSimClassicNeg    = 0x11
	FilmSimEternaBleach  = 0x12
	FilmSimNostalgicNeg  = 0x13
	FilmSimRealaAce      = 0x14
)

// MonochromeSims is the set of film simulations that are B&W (no Color adjustment).
var MonochromeSims = map[int]bool{
	FilmSimMonochrome:   true,
	FilmSimMonochromeYe: true,
	FilmSimMonochromeR:  true,
	FilmSimMonochromeG:  true,
	FilmSimSepia:        true,
	FilmSimAcros:        true,
	FilmSimAcrosYe:      true,
	FilmSimAcrosR:       true,
	FilmSimAcrosG:       true,
}

var FilmSimLabels = map[int]string{
	FilmSimProvia:        "Provia (Standard)",
	FilmSimVelvia:        "Velvia (Vivid)",
	FilmSimAstia:         "Astia (Soft)",
	FilmSimProNegHi:      "PRO Neg. Hi",
	FilmSimProNegStd:     "PRO Neg. Std",
	FilmSimMonochrome:    "Monochrome",
	FilmSimMonochromeYe:  "Monochrome + Yellow",
	FilmSimMonochromeR:   "Monochrome + Red",
	FilmSimMonochromeG:   "Monochrome + Green",
	FilmSimSepia:         "Sepia",
	FilmSimClassicChrome: "Classic Chrome",
	FilmSimAcros:         "Acros",
	FilmSimAcrosYe:       "Acros + Yellow",
	FilmSimAcrosR:        "Acros + Red",
	FilmSimAcrosG:        "Acros + Green",
	FilmSimEterna:        "Eterna (Cinema)",
	FilmSimEternaBleach:  "Eterna Bleach Bypass",
	FilmSimNostalgicNeg:  "Nostalgic Neg.",
	FilmSimRealaAce:      "Reala Ace",
	FilmSimClassicNeg:    "Classic Neg.",
}

// White balance mode values
const (
	WBAsShot           = 0x0000
	WBAuto             = 0x0002
	WBDaylight         = 0x0004
	WBIncandescent     = 0x0006
	WBUnderwater       = 0x0008
	WBFluorescent1     = 0x8001
	WBFluorescent2     = 0x8002
	WBFluorescent3     = 0x8003
	WBShade            = 0x8006
	WBColorTemp        = 0x8007
	WBAmbiencePriority = 0x8021
)

// Grain effect combined values (low byte = strength, high byte = size).
const (
	GrainOff         = 0x0000
	GrainWeakSmall   = 0x0002
	GrainStrongSmall = 0x0003
	GrainWeakLarge   = 0x0102
	GrainStrongLarge = 0x0103
)

// Dynamic range enum values
const (
	DR100 = 1
	DR200 = 2
	DR400 = 3
)
