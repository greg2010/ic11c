/*
 * An airlock cycle driven by a state machine, with a few helpers that exercise
 * recursion and pointer parameters.
 *
 * The state and the retry counter are integers, and the pressure the cycle
 * waits on is not: a chamber reaches a fraction of a kilopascal long before it
 * reaches zero, so the evacuation test is a comparison between doubles.
 *
 * The two pressure bounds come from a pair of dials on the pump, which an
 * operator may set in either order, so the cycle sorts them before using them.
 */

const dev gauge = d0;
const dev innerDoor = d1;
const dev pump = d2;

constexpr long long kIdle = 0;
constexpr long long kPumping = 1;
constexpr long long kOpening = 2;
constexpr long long kFault = 3;

long long state;
long long retries;
double evacuateBelow;

void swap(double *a, double *b) {
    double t = *a;
    *a = *b;
    *b = t;
}

long long gcd(long long a, long long b) {
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
    double lo = __ic_load(pump, SettingInput);
    double hi = __ic_load(pump, SettingOutput);
    order(&lo, &hi);
    evacuateBelow = lo;
    __ic_store(pump, Setpoint, hi);

    while (true) {
        do {
            step();
        } while (state == kPumping && evacuated());

        __ic_sleep(gcd(12, 8));
    }
}
