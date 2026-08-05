/*
 * Ore dispensing driven from a recipe table held in the data region.
 *
 * The table names, for each alloy, the ores and the counts that smelt it. A
 * furnace does not take a recipe: it smelts what its reagents and its gas
 * mixture make, so the alloy is selected by dispensing the right ores into it
 * and firing it, which is what the player script these values come from calls
 * furnace vending control. The vending machine is asked for an ore by hash on
 * RequestHash; the furnace only gets an Activate.
 *
 * The table is eleven rows of seven columns, flattened because a second
 * dimension is not expressible, and indexed with the multiply a second
 * dimension would have generated. It is the corpus program that spends the
 * memory array rather than the registers.
 *
 * A constexpr array is materialised at runtime, one poke per element, so the
 * table costs lines as well as slots and the two budgets bind together: 77
 * elements are 77 of the program's 119 lines, which leaves nine of the 128
 * unspent. The seventeen rows the game actually defines do not fit beside any
 * logic at all, which is why the player script these values come from writes
 * the table from a second chip and reads it back over the housing's stack.
 *
 * Every hash is the literal the game computes rather than a call, since a
 * constexpr initializer is folded before any intrinsic runs.
 */

const dev selector = d0;
const dev vender = d1;
const dev furnace = d2;
const dev display = d3;
const dev memory = d4;

// A console shows a raw number under mode 0. The alloy hash is a number and not
// a fraction, so it is the mode this program leaves the console in.
constexpr long kModeDefault = 0;

constexpr long kRows = 11;
constexpr long kCols = 7;

constexpr long kAlloy = 0;
constexpr long kOreA = 1;
constexpr long kQtyA = 2;
constexpr long kOreB = 3;
constexpr long kQtyB = 4;
constexpr long kOreC = 5;
constexpr long kQtyC = 6;

constexpr long kRecipes[kRows * kCols] = {
    -404336834, -707307845, 1, 0, 0, 0, 0,
    226410516, -1348105509, 1, 0, 0, 0, 0,
    -1301215609, 1758427767, 1, 0, 0, 0, 0,
    2134647745, -190236170, 1, 0, 0, 0, 0,
    -1406385572, 1830218956, 1, 0, 0, 0, 0,
    -290196476, 1103972403, 1, 0, 0, 0, 0,
    -929742000, -916518678, 1, 0, 0, 0, 0,
    502280180, -1348105509, 1, -916518678, 1, 0, 0,
    -82508479, 1758427767, 1, -190236170, 1, 0, 0,
    -787796599, -1348105509, 2, -654790771, 1, 1830218956, 1,
    -1897868623, -916518678, 1, 1103972403, 2, -983091249, 1,
};

long lastRow = -1;

long findRow(long alloy) {
    for (long r = 0; r < kRows; r++) {
        if (kRecipes[r * kCols + kAlloy] == alloy) {
            return r;
        }
    }
    return -1;
}

long ingots(long row) {
    return kRecipes[row * kCols + kQtyA]
         + kRecipes[row * kCols + kQtyB]
         + kRecipes[row * kCols + kQtyC];
}

void main(void) {
    while (true) {
        long wanted = (long)__ic_load(selector, Setting);
        long row = findRow(wanted);

        if (row < 0) {
            __ic_store(vender, Activate, false);
            __ic_store(furnace, Activate, false);
            __ic_store(display, Setting, -1);
        } else {
            lastRow = row;
            __ic_store(vender, RequestHash, kRecipes[row * kCols + kOreA]);
            __ic_store(vender, Activate, true);
            __ic_store(furnace, Activate, true);
            __ic_store(display, Setting, kRecipes[row * kCols + kAlloy]);
            __ic_store(memory, Setting, ingots(row));
        }
        __ic_store(display, Mode, kModeDefault);

        __ic_yield();
    }
}
