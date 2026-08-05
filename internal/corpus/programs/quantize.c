/*
 * The chip's roundings, written against one reading and against its negation.
 *
 * trunc, floor and ceil agree on a positive value and disagree on a negative
 * one, so the four are applied to the same sample and published side by side:
 * the difference between them in the emitted program is the opcode and nothing
 * else.
 *
 * round is the chip's, which is not C's. It breaks a tie to the even neighbour
 * rather than away from zero, so 0.5 answers 0 and 1.5 answers 2. A program
 * that wants C's rounding has to write it.
 *
 * abs and sgn split a reading into the two halves that multiply back to it,
 * and the lamp is driven from where nearest and toward-zero part company, which
 * is a sample carrying more than half a unit -- and, because of the tie rule,
 * exactly half of one only where the neighbour below is odd.
 */

const dev gauge = d0;
const dev display = d1;
const dev tally = d2;
const dev memory = d3;

// A console shows a raw number under mode 0, which is what these are.
constexpr long long kModeDefault = 0;

constexpr double kQuantum = 0.25;
constexpr double kScale = 1000.0;

double drift;

// Snap to the nearest multiple of a step. The division is exact for a step that
// is a power of two, so the rounding is the only thing that loses anything.
double quantize(double v, double step) {
    return __ic_round(v / step) * step;
}

// A reading and its negation round to values that differ by more than a sign,
// so both directions are exercised from one sample.
double spread(double v) {
    double up = __ic_ceil(v) + __ic_ceil(-v);
    double down = __ic_floor(v) + __ic_floor(-v);
    return up - down;
}

void main(void) {
    while (true) {
        double raw = __ic_load(gauge, Pressure) * kScale;

        double towardZero = __ic_trunc(raw);
        double nearest = __ic_round(raw);
        double magnitude = __ic_abs(raw);
        double sign = __ic_sgn(raw);

        drift += nearest - towardZero;

        __ic_store(display, Setting, quantize(raw, kQuantum));
        __ic_store(display, Mode, kModeDefault);
        __ic_store(memory, Setting, spread(raw) * sign);
        __ic_store(tally, Setting, drift);
        // The magnitude test is what keeps the lamp off for a reading that is
        // only noise: a sample under a quarter unit is not a disagreement worth
        // reporting even where the two roundings do part company on it.
        __ic_store(tally, On, nearest != towardZero && magnitude > kQuantum);
        __ic_yield();
    }
}
