/*
 * A median held over a window of gauge readings.
 *
 * An average follows an outlier and a median does not, so the middle sample is
 * selected rather than the mean taken. The selection is what puts a call here:
 * a body cannot be spliced into itself, so the recursive halves are emitted out
 * of line and their frames are what grows into the slots the window leaves.
 *
 * Other programs in the corpus call too, and the pairing is what this one is
 * for. selectNth is written recursing into itself as well as into shortPass,
 * but only the pair costs a frame: a call in tail position becomes a jump back
 * to the top of the same body, so what the emitted program pushes is the edge
 * between the two functions and nothing else.
 *
 * The window is 64 of the 512 slots and is spoken for before the first frame is
 * pushed, so a frame here has both an unbounded depth to reach and a data
 * region already sitting under it. Nothing bounds the recursion but the range
 * it is given, so the depth the frames reach is data-dependent, which is the
 * case the layout report describes rather than enforces.
 */

const dev gauge = d0;
const dev display = d1;
const dev alarm = d2;

constexpr long kWindow = 64;
constexpr long kShortRange = 8;
constexpr double kAlarmKilopascals = 250.0;

double window[kWindow];
long cursor;
long filled;

void selectNth(long lo, long hi, long n);

void swapAt(long a, long b) {
    double t = window[a];
    window[a] = window[b];
    window[b] = t;
}

long partitionRange(long lo, long hi) {
    double pivot = window[hi];
    long cut = lo;
    for (long i = lo; i < hi; i++) {
        if (window[i] < pivot) {
            swapAt(i, cut);
            cut++;
        }
    }
    swapAt(cut, hi);
    return cut;
}

// A short range costs less to order outright than to partition, and a range
// that turns out long is handed back to the selection -- which is what makes
// the two mutually recursive rather than each merely recursive.
void shortPass(long lo, long hi, long n) {
    if (hi - lo > kShortRange) {
        selectNth(lo, hi, n);
        return;
    }
    for (long i = lo + 1; i <= hi; i++) {
        long j = i;
        while (j > lo && window[j - 1] > window[j]) {
            swapAt(j - 1, j);
            j--;
        }
    }
}

void selectNth(long lo, long hi, long n) {
    if (lo >= hi) {
        return;
    }
    if (hi - lo <= kShortRange) {
        shortPass(lo, hi, n);
        return;
    }
    long cut = partitionRange(lo, hi);
    if (n < cut) {
        selectNth(lo, cut - 1, n);
    } else if (n > cut) {
        selectNth(cut + 1, hi, n);
    }
}

void main(void) {
    while (true) {
        window[cursor] = __ic_load(gauge, Pressure);
        cursor = (cursor + 1) % kWindow;
        if (filled < kWindow) {
            filled++;
        }

        long middle = filled / 2;
        selectNth(0, filled - 1, middle);
        double median = window[middle];

        __ic_store(display, Setting, median);
        __ic_store(alarm, On, median > kAlarmKilopascals);
        __ic_yield();
    }
}
