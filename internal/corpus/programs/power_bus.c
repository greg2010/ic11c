/*
 * A power bus watched from one chip and republished on the chip itself.
 *
 * db names the housing the code is running in, so a store to it lands on the
 * Setting a second chip reads back over the network. That is how one chip hands
 * a computed figure to another without a memory between them, and it is the one
 * device here that is not a pin.
 *
 * A network carries a cable analyser on each side of its batteries, so the two
 * are told apart by name rather than by prefab: a plain batch over the prefab
 * would sum the supply side and the draw side into one figure that describes
 * neither. The names are the ones a player labels the analysers with.
 *
 * Charge and Maximum are summed across the battery bank rather than read from a
 * single battery. A bank that matches nothing sums to NaN, so the ratio derived
 * from it is held at its last good value rather than published as one.
 */

const dev self = db;
const dev display = d0;
const dev breaker = d1;
const dev alarm = d2;
const dev memory = d3;

constexpr double kReserve = 0.25;
constexpr double kShedding = 0.10;

// A console shows a fraction as a percentage under mode 1 and a raw number
// under mode 0, so the ratio is published as a ratio and the console formats it.
constexpr long long kModePercent = 1;

double heldRatio;
long long brownouts;

void main(void) {
    long long batteries = __ic_hash("StructureBattery");
    long long analyzer = __ic_hash("StructureCableAnalysizer");
    long long inputSide = __ic_hash("Power Input");
    long long outputSide = __ic_hash("Power Output");

    while (true) {
        double stored = __ic_load_batch(batteries, Charge, Sum);
        double capacity = __ic_load_batch(batteries, Maximum, Sum);
        double supply = __ic_load_batch_named(analyzer, inputSide, PowerPotential, Sum);
        double draw = __ic_load_batch_named(analyzer, outputSide, PowerActual, Sum);

        double ratio = stored / capacity;
        if (!__ic_isnan(ratio)) {
            heldRatio = ratio;
        }

        if (heldRatio < kShedding) {
            brownouts++;
        }

        __ic_store(self, Setting, heldRatio);
        __ic_store(breaker, On, heldRatio > kShedding && supply >= draw);
        __ic_store(alarm, On, heldRatio < kReserve);
        __ic_store(display, Setting, heldRatio);
        __ic_store(display, Mode, kModePercent);
        __ic_store(memory, Setting, supply - draw);
        __ic_yield();
    }
}
