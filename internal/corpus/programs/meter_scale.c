/*
 * A meter that scales a reading and packs a dial position beside it.
 *
 * The scaling is written with compound assignments because each one meets a
 * different operand rule: a double target takes a long right operand and
 * widens it, where the same operator with the operands the other way round
 * would have to narrow and is refused. The packing is integer throughout, which
 * is what the shifting and masking forms require -- they take no double at all.
 *
 * The lamp is driven from a cast rather than a comparison. A double reaching
 * bool is a written conversion where a narrowing to long would be, and it
 * calls anything non-zero true, a NaN included, so a faulted gauge lights the
 * lamp rather than clearing it.
 */

const dev gauge = d0;
const dev display = d1;
const dev lamp = d2;
const dev memory = d3;

constexpr long kModeDefault = 0;

constexpr long kGainSteps = 3;
constexpr long kDialModulus = 360;
constexpr long kNibble = 0xF;
constexpr long kFieldShift = 4;
constexpr double kScale = 10.0;

long packed;
long dial;

void main(void) {
    while (true) {
        double reading = __ic_load(gauge, Pressure);

        reading *= kGainSteps;
        reading /= kScale;

        dial += (long)reading;
        dial %= kDialModulus;

        long field = (long)reading;
        field &= kNibble;

        packed = dial;
        packed <<= kFieldShift;
        packed |= field;

        bool live = (bool)reading;

        __ic_store(display, Setting, (double)packed);
        __ic_store(display, Mode, kModeDefault);
        __ic_store(memory, Setting, dial);
        __ic_store(lamp, On, live);
        __ic_yield();
    }
}
