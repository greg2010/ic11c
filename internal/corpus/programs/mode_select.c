/*
 * A vent driven from a mode dial.
 *
 * Two dial positions vent the same way and differ only in whether the pump runs
 * beside them, so their arms carry a label and no body and stack onto the arm
 * below. That is the only fallthrough the language permits -- an arm with a body
 * must terminate -- and this is where the corpus reaches it with two case labels
 * rather than a case stacked onto a default.
 *
 * The dial and the vent do not share an enumeration. A dial is a Setting and
 * counts as far as its positions go; a vent's Mode names a direction and takes
 * nothing but outward or inward, so the arm that drives it converts rather than
 * passing the dial through.
 *
 * A position the dial can hold but this program does not drive continues the
 * loop from inside the switch rather than breaking out of it, which leaves the
 * tick unspent.
 *
 * The outer loop has no controlling expression, because the chip runs until it
 * is switched off and there is no condition to write.
 */

const dev dial = d0;
const dev vent = d1;
const dev pump = d2;
const dev panel = d3;

// Dial positions.
constexpr long kOff = 0;
constexpr long kExhaust = 1;
constexpr long kPurge = 2;
constexpr long kFill = 3;
constexpr long kSeal = 4;

// The vent's own enumeration, which is all its Mode accepts.
constexpr long kVentOutward = 0;
constexpr long kVentInward = 1;

constexpr long kModeDefault = 0;

long ticks;

void main(void) {
    for (;;) {
        long mode = (long)__ic_load(dial, Setting);

        switch (mode) {
        case kOff:
            __ic_store(vent, On, false);
            __ic_store(pump, On, false);
            break;
        case kExhaust:
        case kPurge:
            __ic_store(vent, Mode, kVentOutward);
            __ic_store(vent, On, true);
            __ic_store(pump, On, mode == kPurge);
            break;
        case kFill:
            __ic_store(vent, Mode, kVentInward);
            __ic_store(vent, On, true);
            __ic_store(pump, On, false);
            break;
        case kSeal:
            __ic_store(vent, On, false);
            __ic_store(pump, On, true);
            break;
        default:
            __ic_store(panel, Setting, -1);
            __ic_yield();
            continue;
        }

        ticks++;
        __ic_store(panel, Setting, ticks);
        __ic_store(panel, Mode, kModeDefault);
        __ic_yield();
    }
}
