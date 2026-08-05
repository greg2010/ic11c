// A ring buffer of pressure samples with a rolling average. The window is the
// only thing in the data region, and nothing grows above it: record, average
// and peak are spliced into main rather than called out of line -- the compiler
// splices every function that cannot reach itself -- so the emitted program
// pushes no frame and the other 504 of the 512 slots stay free.
//
// Pressure is fractional, so the samples and both aggregates are doubles and
// the mean is a plain division rather than a truncating one. The cursor stays
// a long long: it counts slots.

const dev gauge = d0;
const dev display = d1;
const dev alarm = d2;

constexpr long long kWindow = 8;
constexpr double kAlarmKilopascals = 200.5;

double samples[kWindow];
long long cursor;
long long filled;

void record(double value) {
    samples[cursor] = value;
    cursor = (cursor + 1) % kWindow;
    if (filled < kWindow) {
        filled = filled + 1;
    }
}

double average(void) {
    if (filled == 0) {
        return 0.0;
    }

    double sum = 0.0;
    for (long long i = 0; i < filled; i++) {
        sum += samples[i];
    }
    return sum / filled;
}

double peak(void) {
    double best = 0.0;
    long long i;
    for (i = 0; i < filled; i++) {
        if (samples[i] > best) {
            best = samples[i];
        }
    }
    return best;
}

void main(void) {
    while (true) {
        record(__ic_load(gauge, Pressure));
        __ic_store(display, Setting, average());
        __ic_store(alarm, On, peak() > kAlarmKilopascals);
        __ic_yield();
    }
}
