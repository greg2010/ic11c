// A delivery ledger kept against the top of the machine's exact-integer window.
//
// Every value on the chip is a double, so a long long counts by ones only as
// far as 2^53. The ledger is therefore not allowed to run past that. It holds
// the room left under the ceiling rather than the total delivered, takes each
// delivery out of it, tops the delivery that closes a cycle off at exactly what
// is left, and rolls once the room is gone. Every value it holds is one a
// double and a 64-bit integer both denote, which is what lets the ledger mean
// the same thing to the chip and to a compiler whose long long really is 64
// bits.
//
// The probe beside it is where that stops being true. It walks a plain double
// through 2^53 - 2, 2^53 - 1 and 2^53 and asks at each what the smallest step
// is that the value still notices. Below the ceiling the answer is one; on it
// the answer is two, because 2^53 + 1 is not a value a double holds and rounds
// back to where the step started. The base is taken from the turn counter so
// that the walk happens on the chip: a probe whose base the optimizer could
// fold would be reporting what the compiler's own arithmetic did instead.
//
// The two cycles are kept coprime so that neither the delivery that closes the
// ledger nor the step that lands on the ceiling arrives at a fixed phase of the
// other.
//
// Nothing masks or shifts the ledger. A bitwise operand is read through a
// conversion that reduces modulo 2^53, and the one value the ledger is built to
// reach is the one value that conversion sends to zero.

const dev display = d0;
const dev tally = d1;
const dev alarm = d2;

constexpr long long kModeDefault = 0;

// 2^53, the largest integer the machine still counts to by ones.
constexpr long long kCeiling = 9007199254740992;
// Large enough that a cycle closes every thirty-odd ticks rather than once past
// the end of a chip's life.
constexpr long long kDelivery = 281474976710656;
constexpr long long kJitter = 8;

// 2^53 - 2, so the walk steps up to the ceiling and stops on it.
constexpr double kProbeBase = 9007199254740990.0;
constexpr long long kProbeSteps = 3;

constexpr long long kPeriod = kJitter * kProbeSteps;

long long room = kCeiling;
long long turn;

// The smallest step v still notices.
double grainAt(double v) {
    double step = 1.0;
    while (v + step == v) {
        step += step;
    }
    return step;
}

void main(void) {
    while (true) {
        long long delivery = kDelivery + turn % kJitter;
        if (delivery > room) {
            delivery = room;
        }
        room -= delivery;

        double probe = kProbeBase + (double)(turn % kProbeSteps);

        __ic_store(display, Setting, kCeiling - room);
        __ic_store(display, Mode, kModeDefault);
        __ic_store(tally, Setting, grainAt(probe));
        __ic_store(alarm, On, room == 0);

        if (room == 0) {
            room = kCeiling;
        }
        turn = (turn + 1) % kPeriod;
        __ic_yield();
    }
}
