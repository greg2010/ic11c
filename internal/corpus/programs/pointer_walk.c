/*
 * A window of gauge readings, published as a mean beside a count of how much of
 * it survives an outlier trim. The mean is over the whole window; the trim runs
 * alongside it and drives the alarm, so a window that is mostly outliers is
 * reported rather than quietly averaged away.
 *
 * The window is scanned by two pointers walking toward each other, which is
 * what reaches the pointer operations the language admits and the corpus
 * otherwise leaves alone: arithmetic within one array, an ordering between two
 * pointers into it, and the distance between them.
 *
 * Each helper takes one pointer and a count rather than two pointers, and
 * derives its far end from the near one. Two pointer parameters would be two
 * objects as far as the check can prove -- nothing in a signature says they
 * point into the same array -- and comparing or subtracting them is refused on
 * that ground even where a caller only ever passes ends of one window.
 *
 * A mean over a fractional count is a division rather than a truncating one, so
 * the span between the pointers, which is a count of slots and an integer, is
 * converted where it meets the sum.
 */

const dev gauge = d0;
const dev display = d1;
const dev alarm = d2;
const dev memory = d3;

constexpr long kModeDefault = 0;

constexpr long kWindow = 16;
constexpr double kOutlierBand = 25.0;

double window[kWindow];
long cursor;
long filled;

double meanFrom(const double *first, long count) {
    const double *last = first + count - 1;
    const double *p = first;
    double sum = 0.0;
    while (p <= last) {
        sum += *p;
        ++p;
    }
    return sum / (double)(last - first + 1);
}

// Walk inward from both ends while the sample under either pointer sits outside
// the band, and stop as soon as both are inside it.
long trimFrom(const double *first, long count, double centre) {
    const double *lo = first;
    const double *hi = first + count - 1;
    while (lo < hi) {
        if (__ic_abs(*lo - centre) > kOutlierBand) {
            ++lo;
        } else if (__ic_abs(*hi - centre) > kOutlierBand) {
            --hi;
        } else {
            break;
        }
    }
    return hi - lo;
}

void main(void) {
    while (true) {
        window[cursor] = __ic_load(gauge, Pressure);
        cursor = (cursor + 1) % kWindow;
        if (filled < kWindow) {
            ++filled;
        }

        double centre = window[filled / 2];
        long span = trimFrom(window, filled, centre);
        double mean = meanFrom(window, filled);

        __ic_store(display, Setting, mean);
        __ic_store(display, Mode, kModeDefault);
        __ic_store(memory, Setting, span);
        __ic_store(alarm, On, span * 2 < filled);
        __ic_yield();
    }
}
