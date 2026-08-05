// A service meter whose running total outlives the chip. The count is kept on a
// logic memory rather than in the data region, so a housing that is rebuilt or
// reprogrammed resumes the total instead of starting again from zero, and the
// low word of what comes back is packed into a status field for a display.
//
// The integer type is long long throughout because the machine holds every
// integer exactly to 2^53 and a C type narrower than that would make a program
// the compiler accepts mean something else to the editor reading it. The
// constants sit on C's own typing boundaries: a constant takes the narrowest
// type that represents it rather than the type of the object it initializes, a
// hexadecimal constant may land on an unsigned type where a decimal one may not,
// and a bare 1 is an int, so it shifts 30 places and no further: `1 << 31` is
// refused because the answer does not fit the int C computes it in. The
// widening casts are what make each constant mean what it reads as.
//
// What no constant can settle is how large the reading is. A literal past the
// exact-integer bound is refused where it is written; a value arriving from a
// device is refused nowhere, and a memory answers whatever it was last left
// holding. A bitwise operator handed a magnitude at that bound is therefore the
// one case the machine decides and the compiler never sees, which is why the
// reading is masked down to a word before anything is added to it: what the
// meter computes after that stays where the constants already are.

const dev display = d0;
const dev marker = d1;
const dev memory = d2;

constexpr long long kRollover = 4294967296;
constexpr long long kMarkerBit = (long long)1 << 40;
constexpr long long kWordMask = 0xFFFFFFFF;
constexpr long long kByteMask = 0xFFFFFFFF & 0xFF;
constexpr long long kHalfWord = 0x80000000 >> 1;
constexpr long long kTopBit = (long long)1 << 31;
constexpr long long kStride = 0x7FFFFFFF / 3;
constexpr long long kBias = -2147483648;

long long flags;

void main(void) {
    while (true) {
        long long carried = (long long)__ic_load(memory, Setting);
        long long ticks = carried & kWordMask;
        long long packed = (ticks + kStride - kBias) & kWordMask;

        ticks = ticks + 1;
        if (ticks >= kRollover) {
            ticks = 0;
            flags = flags | kMarkerBit;
        }

        __ic_store(memory, Setting, ticks);
        __ic_store(display, Setting, (packed >> 8) & kByteMask);
        // The rollover marker is latched, and it is published by driving the
        // console's Mode with it: a bool widens to the 0 or 1 Mode takes, so
        // the console switches display mode once and stays there. The lamp
        // below it is not latched -- it follows the field into the upper half
        // of the word and out again.
        __ic_store(display, Mode, (flags & kMarkerBit) != 0);
        __ic_store(marker, On, packed >= kHalfWord && packed < kTopBit);
        __ic_yield();
    }
}
