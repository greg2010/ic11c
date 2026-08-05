/*
 * A filtration unit scrubbing pollutant out of an oxygen line.
 *
 * Filtration is the machine whose readings carry the port as well as the gas:
 * one input and two outputs, with the filtered gas leaving on the second. That
 * is what makes the names here longer than a sensor's -- a gas sensor answers
 * RatioPollutant, and this answers which side of the machine the pollutant is
 * on. A gas mixer has no such family; it reports one Ratio and nothing else,
 * which is why the port-scoped names belong to this program and not to that
 * one.
 *
 * Mode chooses between idle and active and is the machine's own enumeration,
 * so it takes 0 or 1 and nothing else. Every ratio, pressure and temperature
 * reads back. The rate the controller settles on goes to a logic memory rather
 * than to the machine, because what a filtration unit does with a Setting is
 * not something this program should assume.
 */

const dev filtration = d0;
const dev panel = d1;
const dev alarm = d2;
const dev memory = d3;

constexpr long kModeDefault = 0;

constexpr double kCleanTarget = 0.98;
constexpr double kWasteFloor = 0.5;
constexpr double kGain = 40.0;
constexpr double kPercent = 100.0;
constexpr double kBand = 0.05;
constexpr double kHotKelvin = 340.0;

constexpr long kDirtyFault = 1;
constexpr long kStalledFault = 2;
constexpr long kStarvedFault = 4;
constexpr long kHotFault = 8;

constexpr long kModeFilter = 1;
constexpr long kModeBypass = 0;

double rate;
long faults;

void main(void) {
    while (true) {
        double dirtyIn = __ic_load(filtration, RatioPollutantInput);
        double cleanOut = __ic_load(filtration, RatioOxygenOutput);
        double wasteOut = __ic_load(filtration, RatioPollutantOutput2);
        double inletPressure = __ic_load(filtration, PressureInput);
        double outletKelvin = __ic_load(filtration, TemperatureOutput);

        faults = 0;

        // An input the machine has no atmosphere on reads NaN, and a rate
        // corrected from one would settle where the controller never chose.
        if (__ic_isnan(dirtyIn) || inletPressure <= 0.0) {
            faults |= kStarvedFault;
        } else {
            double error = kCleanTarget - cleanOut;
            rate = __ic_clamp(rate + error * kGain, 0.0, kPercent);
        }

        if (__ic_abs(cleanOut - kCleanTarget) > kBand) {
            faults |= kDirtyFault;
        }
        // Pollutant entering but not leaving on the waste side means the filter
        // has stopped separating rather than that the line is clean.
        if (dirtyIn > 0.0 && wasteOut < kWasteFloor) {
            faults |= kStalledFault;
        }
        if (outletKelvin > kHotKelvin) {
            faults |= kHotFault;
        }

        __ic_store(filtration, Mode, faults == 0 ? kModeFilter : kModeBypass);
        __ic_store(filtration, On, (faults & kStarvedFault) == 0);
        __ic_store(alarm, On, faults != 0);
        __ic_store(panel, Setting, faults);
        __ic_store(panel, Mode, kModeDefault);
        __ic_store(memory, Setting, rate);
        __ic_yield();
    }
}
