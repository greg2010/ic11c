/*
 * A printer kept stocked against the recipe it is running.
 *
 * The reagent intrinsic answers four views of one ore: what the machine holds,
 * what the running recipe asks of it, what one unit of that recipe costs, and
 * what the whole network holds. The first two differ by the amount the vending
 * machine beside it has to make up.
 *
 * A vending machine is asked for an item by hash on RequestHash, which is the
 * only writable way to name a thing to a machine; a printer's own RecipeHash is
 * writable in the same way and reads back what it settled on. Neither is a
 * Setting, and a hash written to a Setting would be a number the machine has no
 * use for.
 *
 * A reagent quantity is a mass rather than a count, so every reading is a
 * double, and the one value published without a conversion is the ore hash.
 */

const dev printer = d0;
const dev vender = d1;
const dev display = d2;
const dev memory = d3;

constexpr long kOres = 4;
constexpr double kRestockMass = 5.0;
constexpr long kModeDefault = 0;

long ores[kOres];
double deficit[kOres];
long worst;

void nameOres(void) {
    ores[0] = __ic_hash("Copper");
    ores[1] = __ic_hash("Iron");
    ores[2] = __ic_hash("Gold");
    ores[3] = __ic_hash("Silicon");
}

// A reagent the printer has never held reads NaN rather than zero, and a
// shortfall computed from one would poison the sum it feeds.
double shortfall(long ore) {
    double have = __ic_load_reagent(printer, Contents, ore);
    double need = __ic_load_reagent(printer, Required, ore);
    if (__ic_isnan(have) || __ic_isnan(need)) {
        return 0.0;
    }
    return need - have;
}

void main(void) {
    nameOres();

    while (true) {
        double total = 0.0;
        double peak = 0.0;
        worst = -1;

        for (long i = 0; i < kOres; i++) {
            double missing = shortfall(ores[i]);
            deficit[i] = missing;
            if (missing > peak) {
                peak = missing;
                worst = i;
            }
            if (missing > 0.0) {
                total += missing;
            }
        }

        double perUnit = __ic_load_reagent(printer, Recipe, ores[0]);
        double networkHeld = __ic_load_reagent(printer, TotalContents, ores[0]);

        __ic_store(vender, RequestHash, worst < 0 ? 0 : ores[worst]);
        __ic_store(vender, Activate, total > kRestockMass);
        __ic_store(display, Setting, __ic_load(printer, RecipeHash));
        __ic_store(display, Mode, kModeDefault);
        __ic_store(memory, Setting, perUnit > 0.0 ? networkHeld / perUnit : 0.0);
        __ic_yield();
    }
}
