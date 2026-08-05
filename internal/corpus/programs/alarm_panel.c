// A fault panel. Three conditions fold into one latched bitmask, an
// acknowledgement clears the filter bit and silences the horn for a while, and
// the mask is republished negated so a display can tell it from a reading.
//
// Every device read answers a double, and a double is not a condition here:
// `if (__ic_load(vent, On))` is refused. A reading tested for truth is
// therefore cast to a long first, which is the two casts below. That is
// deliberate rather than a gap -- almost nothing a chip reads wanted a test
// against zero, so the line has to say that is what it meant.

const dev vent = d0;
const dev panel = d1;
const dev horn = d2;

constexpr long kPressure = 1;
constexpr long kPower = 2;
constexpr long kFilter = 4;

constexpr long kSilenceTicks = 30;

// The filter is assumed dirty until a service pass acknowledges it.
long latched = kFilter;
long silence;

long sample(void) {
    long mask = 0;
    if (__ic_load(vent, Pressure) < 10.0) {
        mask |= kPressure;
    }
    if (!(long)__ic_load(vent, On)) {
        mask |= kPower;
    }
    if (__ic_load(vent, Ratio) > 0.8 || __ic_load(vent, Error) != 0.0) {
        mask |= kFilter;
    }
    return mask;
}

void main(void) {
    while (true) {
        latched |= sample();

        if ((long)__ic_load(panel, Activate)) {
            latched = latched & ~kFilter;
            silence = kSilenceTicks;
        }
        if (silence > 0) {
            silence--;
        }

        __ic_store(panel, Setting, -latched);
        __ic_store(horn, On, latched != 0 && silence == 0);
        __ic_yield();
    }
}
