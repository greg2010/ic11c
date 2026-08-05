// A reading spread across a row of single-digit lamps, multiplexed one column
// a tick the way the row itself is wired: one lamp line, one column-select
// line, and the whole number readable only because the eye is slower than the
// chip.
//
// Laying the digits out is the recursive half. The top place a reading reaches
// is not known before it is found, so the walk climbs to it and writes the
// digits on the way back down; a loop would need the digit count first, and the
// count is exactly what the climb is looking for.
//
// Nothing is held across the recursive call. The walk's state is in globals,
// and a global is one address for the whole program, so every activation of
// render sees the same place: the place this one climbed past is recovered by
// dividing it back down rather than by keeping a copy. A local would have been
// saved and restored correctly -- a recursive call does push a frame -- but
// each activation would then hold one more of the 512 slots, and how deep the
// climb goes is decided by the reading rather than at compile time.

const dev gauge = d0;
const dev lamp = d1;
const dev column = d2;

constexpr long long kColumns = 5;
constexpr long long kCeiling = 99999;
constexpr long long kBlank = -1;

long long digits[kColumns];
long long value;
long long place;
long long cursor;
long long scan;

void render(void) {
    if (place * 10 <= value) {
        place = place * 10;
        render();
        place = place / 10;
    }
    digits[cursor] = (value / place) % 10;
    cursor = cursor + 1;
}

void main(void) {
    while (true) {
        value = (long long)__ic_load(gauge, Setting);
        if (value < 0) {
            value = 0;
        }
        if (value > kCeiling) {
            value = kCeiling;
        }

        place = 1;
        cursor = 0;
        render();
        // A shorter reading leaves the columns above it standing, so they are
        // blanked rather than left showing the last one's leading digits.
        while (cursor < kColumns) {
            digits[cursor] = kBlank;
            cursor = cursor + 1;
        }

        __ic_store(column, Setting, scan);
        __ic_store(lamp, Setting, digits[scan]);
        scan = (scan + 1) % kColumns;
        __ic_yield();
    }
}
