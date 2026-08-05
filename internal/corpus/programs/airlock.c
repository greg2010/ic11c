/*
 * An airlock cycle driven by a state machine, with a few helpers that exercise
 * recursion and pointer parameters.
 *
 * The state and the retry counter are integers, and the pressure the cycle
 * waits on is not: a chamber reaches a fraction of a kilopascal long before it
 * reaches zero, so the evacuation test is a comparison between doubles.
 *
 * The two pressure bounds come from a pair of dials, which an operator may set
 * in either order, so the cycle sorts them before using them. A pump takes one
 * Setting and reports no second bound of its own, so the bounds are read from
 * the dials and only the upper one is written back.
 */

const dev gauge = d0;
const dev innerDoor = d1;
const dev pump = d2;
const dev lowDial = d3;
const dev highDial = d4;

constexpr long kIdle = 0;
constexpr long kPumping = 1;
constexpr long kOpening = 2;
constexpr long kFault = 3;

long state;
long retries;
double evacuateBelow;

void swap(double *a, double *b) {
    double t = *a;
    *a = *b;
    *b = t;
}

long gcd(long a, long b) {
    if (b == 0) {
        return a;
    }
    return gcd(b, a % b);
}

void order(double *lo, double *hi) {
    if (*lo > *hi) {
        swap(lo, hi);
    }
}

bool evacuated(void) {
    double kpa = __ic_load(gauge, Pressure);
    return kpa < evacuateBelow;
}

void step(void) {
    switch (state) {
    case kIdle:
        retries = 0;
        if (__ic_device_present(innerDoor)) {
            state = kPumping;
        }
        break;
    case kPumping:
        __ic_store(pump, On, true);
        if (evacuated()) {
            state = kOpening;
        } else {
            retries++;
            if (retries > 60) {
                state = kFault;
            }
        }
        break;
    case kOpening:
        __ic_store(pump, On, false);
        __ic_store(innerDoor, Open, true);
        state = kIdle;
        break;
    case kFault:
    default:
        __ic_store(pump, On, false);
        __ic_store(innerDoor, Open, false);
        break;
    }
}

void main(void) {
    double lo = __ic_load(lowDial, Setting);
    double hi = __ic_load(highDial, Setting);
    order(&lo, &hi);
    evacuateBelow = lo;
    __ic_store(pump, Setting, hi);

    while (true) {
        do {
            step();
        } while (state == kPumping && evacuated());

        // Four seconds, computed on the chip. gcd is the one function here the
        // compiler could not splice into its caller, because a body cannot be
        // spliced into itself, so it is the only real call the emitted program
        // makes; swap, order, evacuated and step all disappear into main. The
        // constant arguments do not fold it away, since a call is not a
        // constant expression whatever it is handed.
        __ic_sleep(gcd(12, 8));
    }
}
