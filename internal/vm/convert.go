package vm

import (
	"hash/crc32"
	"math"
)

// Bit widths the chip's integer conversions are built around. The payload width
// and the rotate width genuinely differ, and no single mask serves both.
const (
	// payloadBits is the mantissa a double stores exactly. Every bitwise result
	// is squeezed back through this width, which is why anything needing more
	// than 53 significant bits comes back corrupted.
	payloadBits = 53
	// payloadMask is the low payloadBits set: 0x1FFFFFFFFFFFFF.
	payloadMask int64 = 1<<payloadBits - 1
	// signBit is the bit LongToDouble treats as the sign of the result.
	signBit int64 = 1 << payloadBits
	// unsignedMask is the low 54 bits, which DoubleToLong applies when asked
	// for an unsigned value. It is one bit wider than payloadMask.
	unsignedMask int64 = 1<<54 - 1
	// rotateBits is the width rol and ror rotate over. It is 54, not 53.
	rotateBits = 54
	// rotateMask is the low rotateBits set: 0x3FFFFFFFFFFFFF.
	rotateMask int64 = 1<<rotateBits - 1
	// doubleModulus is 2^53, the modulus DoubleToLong reduces by.
	doubleModulus = 9007199254740992.0
)

// LongToDouble is ProgrammableChip.LongToDouble.
//
// It keeps the low 53 bits and sign extends from bit 53, so every bitwise
// result is a value in [-2^53, 2^53). A computation whose true result needs
// more than 53 significant bits does not saturate or wrap at 64 bits; it comes
// back through this funnel, which is the corruption the target notes.
func LongToDouble(l int64) float64 {
	negative := l&signBit != 0
	l &= payloadMask
	if negative {
		l |= ^payloadMask
	}
	return float64(l)
}

// DoubleToLong is ProgrammableChip.DoubleToLong.
//
// The double is reduced modulo 2^53 before conversion, so the result always
// fits. When signed is false the low 54 bits are kept, one bit wider than the
// 53 LongToDouble reads back, which is how a negative operand reaches the
// unsigned instructions as a large positive value.
//
// NaN reduces to NaN and converts to zero, matching the saturating float to
// integer conversion .NET Core guarantees.
func DoubleToLong(d float64, signed bool) int64 {
	n := int64Saturating(math.Mod(d, doubleModulus))
	if !signed {
		n &= unsignedMask
	}
	return n
}

// int64Saturating converts like a .NET Core double to long cast: NaN becomes
// zero and out of range values clamp to the end of the range, where Go's own
// conversion would be undefined.
func int64Saturating(d float64) int64 {
	switch {
	case math.IsNaN(d):
		return 0
	case d >= math.MaxInt64:
		return math.MaxInt64
	case d <= math.MinInt64:
		return math.MinInt64
	default:
		return int64(d)
	}
}

// int32Saturating converts like a .NET Core double to int cast.
func int32Saturating(d float64) int {
	switch {
	case math.IsNaN(d):
		return 0
	case d >= math.MaxInt32:
		return math.MaxInt32
	case d <= math.MinInt32:
		return math.MinInt32
	default:
		return int(int32(d))
	}
}

// packedASCIIBytes is how many ASCII bytes the STR("...") form fits in a
// double. The name is Ascii6 for this number.
const packedASCIIBytes = 6

// packASCII6 is ProgrammableChip.PackAscii6, the STR("...") preprocessor form.
// Up to six ASCII bytes are packed big endian into a double.
func packASCII6(text string, line int) (float64, error) {
	if text == "" {
		return 0, newFault(ExcInvalidStringNull, line)
	}
	if len(text) > packedASCIIBytes {
		return 0, newFault(ExcInvalidStringLength, line)
	}
	var n int64
	for i := range len(text) {
		c := text[i]
		if c > 0x7f {
			return 0, newFault(ExcInvalidStringNonASCII, line)
		}
		n = n<<8 | int64(c)
	}
	return float64(n), nil
}

// hashName is UnityEngine.Animator.StringToHash, the HASH("...") preprocessor
// form and the same hash the game stamps into prefab and device names.
//
// The implementation is native to the Unity runtime and so is not in the
// decompiled assembly. It is CRC-32/ISO-HDLC reinterpreted as a signed 32 bit
// integer, which is what every published Stationeers prefab hash matches.
func hashName(name string) int {
	return int(int32(crc32.ChecksumIEEE([]byte(name))))
}
