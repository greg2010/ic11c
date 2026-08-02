// A tick odometer and a bit field, both carrying values past the 2^31 a C int
// stops at. The integer type is long long for exactly this: the machine holds
// every integer exactly to 2^53, and a C type narrower than that would make a
// program the compiler accepts mean something else to the editor reading it.
//
// The odometer counts ticks and rolls at 2^32; the marker is a bit above the
// 32nd, so both constants are ones an int would have lost.

const dev display = d0;
const dev marker = d1;

constexpr long long kRollover = 4294967296;
constexpr long long kMarkerBit = (long long)1 << 40;

long long ticks;
long long flags;

void main(void) {
    while (true) {
        ticks = ticks + 1;
        if (ticks >= kRollover) {
            ticks = 0;
            flags = flags | kMarkerBit;
        }

        __ic_store(display, Setting, ticks);
        __ic_store(marker, On, (flags & kMarkerBit) != 0);
        __ic_yield();
    }
}
