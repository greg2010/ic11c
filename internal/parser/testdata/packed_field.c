// A 32-bit field packed and unpacked with constants that sit on C's type
// boundaries.
//
// C gives an integer constant the narrowest type that represents it, not the
// type of the object it initializes. A hexadecimal constant may land on an
// unsigned type where a decimal one may not, and a bare 1 shifts no further
// than the 31 places an int has. Every constant here is written so that C
// computes it in the type the machine computes it in, and the widening casts
// are what make that true rather than punctuation.

const dev display = d0;
const dev marker = d1;

constexpr long long kWordMask = 0xFFFFFFFF;
constexpr long long kByteMask = 0xFFFFFFFF & 0xFF;
constexpr long long kHalfWord = 0x80000000 >> 1;
constexpr long long kTopBit = (long long)1 << 31;
constexpr long long kStride = 0x7FFFFFFF / 3;
constexpr long long kBias = -2147483648;

long long packed;

void main(void) {
    while (true) {
        packed = (packed + kStride - kBias) & kWordMask;

        __ic_store(display, Setting, (packed >> 8) & kByteMask);
        __ic_store(marker, On, packed >= kHalfWord && packed < kTopBit);
        __ic_yield();
    }
}
