// A fault panel. Three conditions fold into one latched bitmask, an
// acknowledgement clears the filter bit and silences the horn for a while, and
// the mask is republished negated so a display can tell it from a reading.

const dev vent = d0;
const dev panel = d1;
const dev horn = d2;

constexpr long long kPressure = 1;
constexpr long long kPower = 2;
constexpr long long kFilter = 4;

constexpr long long kSilenceTicks = 30;

// The filter is assumed dirty until a service pass acknowledges it.
long long latched = kFilter;
long long silence;

long long sample(void) {
    long long mask = 0;
    if (__ic_load(vent, Pressure) < 10.0) {
        mask |= kPressure;
    }
    if (!(long long)__ic_load(vent, On)) {
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

        if ((long long)__ic_load(panel, Activate)) {
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
