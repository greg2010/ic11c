// A liquid separation train audited at three taps. Each tap is a group of
// liquid pipe analysers sharing a name, so the chip reads a stage rather than a
// pipe and a second analyser added to a stage joins the reading it already
// gives.
//
// What the train is for is taking everything that is not water out of the
// stream, so what says whether it works is the fraction that is still something
// else. The audit adds those fractions up at each tap and also takes the worst
// single one, because a stage passing a large total spread over everything is a
// different fault from one passing a single contaminant it was built to remove.
//
// The pipe answers a fraction per substance and there is no way to ask for the
// liquids as a group, so each substance is written out. A logic type and a batch
// mode are assembled into the instruction rather than held in a register, so a
// loop over substances is not something the machine can express at all; the tap
// is an ordinary value and is passed as a parameter.

const dev console = d0;
const dev drain = d1;

constexpr double kContaminated = 0.02;
constexpr double kSingleFault = 0.008;
constexpr long kModePercent = 1;

double held;

// contamination is everything at one tap that is not water, as a fraction of
// what the pipe holds. Polluted water is counted with the rest: it leaves the
// train on the same side the salts do, and a tap running clean has none.
double contamination(long analysers, long tap) {
    double total = 0.0;
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidHydrochloricAcid, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidSodiumChloride, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidCarbonDioxide, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidNitrousOxide, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidPollutant, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidHydrazine, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidNitrogen, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidHydrogen, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidMethane, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioLiquidOxygen, Average);
    total = total + __ic_load_batch_named(analysers, tap, RatioPollutedWater, Average);
    return total;
}

// heaviest is the largest single contaminant at one tap, taken over the
// analysers rather than averaged across them: one pipe of the stage running
// dirty is the thing a stage average hides.
double heaviest(long analysers, long tap) {
    double worst = 0.0;
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioLiquidHydrochloricAcid, BatchMode_Maximum));
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioLiquidSodiumChloride, BatchMode_Maximum));
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioLiquidCarbonDioxide, BatchMode_Maximum));
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioLiquidNitrousOxide, BatchMode_Maximum));
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioLiquidPollutant, BatchMode_Maximum));
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioLiquidHydrazine, BatchMode_Maximum));
    worst = __ic_max(worst, __ic_load_batch_named(analysers, tap, RatioPollutedWater, BatchMode_Maximum));
    return worst;
}

void main(void) {
    long analysers = __ic_hash("StructureLiquidPipeAnalyzer");
    long feed = __ic_hash("Feed");
    long middle = __ic_hash("Middle");
    long tail = __ic_hash("Tail");

    while (true) {
        double atFeed = contamination(analysers, feed);
        double atMiddle = contamination(analysers, middle);
        double atTail = contamination(analysers, tail);

        // The train is only working while each tap is cleaner than the one
        // upstream of it. A stage passing more than it received is either
        // saturated or plumbed backwards, and both look the same from here.
        bool falling = atTail <= atMiddle && atMiddle <= atFeed;
        bool clean = atTail < kContaminated;

        double spike = __ic_max(heaviest(analysers, middle), heaviest(analysers, tail));
        held = __ic_lerp(held, atTail, 0.2);

        __ic_store(drain, On, !clean || !falling || spike > kSingleFault);
        __ic_store(console, Setting, held * 100.0);
        __ic_store(console, Mode, kModePercent);
        __ic_yield();
    }
}
