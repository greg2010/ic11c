// Five habitat sections watched from one chip, each answering four gas
// readings, with the summary published to a console on the sixth pin.
//
// Every reading is taken before anything is written, so all twenty are live at
// once and the allocator has to spill: the machine has eighteen registers and
// this is the corpus program that asks for more than that.

const dev north = d0;
const dev south = d1;
const dev east = d2;
const dev west = d3;
const dev core = d4;
const dev display = d5;

constexpr double kMinKilopascals = 90.0;
constexpr double kMinOxygen = 0.19;
constexpr double kMinKelvin = 285.0;

double lowestPressure;

long long habitable(double kelvin, double kilopascals, double oxygen) {
    if (kelvin < kMinKelvin) {
        return 0;
    }
    if (kilopascals < kMinKilopascals) {
        return 0;
    }
    return oxygen >= kMinOxygen;
}

void main(void) {
    while (true) {
        double nt = __ic_load(north, Temperature);
        double np = __ic_load(north, Pressure);
        double no = __ic_load(north, RatioOxygen);
        double nc = __ic_load(north, RatioCarbonDioxide);
        double st = __ic_load(south, Temperature);
        double sp = __ic_load(south, Pressure);
        double so = __ic_load(south, RatioOxygen);
        double sc = __ic_load(south, RatioCarbonDioxide);
        double et = __ic_load(east, Temperature);
        double ep = __ic_load(east, Pressure);
        double eo = __ic_load(east, RatioOxygen);
        double ec = __ic_load(east, RatioCarbonDioxide);
        double wt = __ic_load(west, Temperature);
        double wp = __ic_load(west, Pressure);
        double wo = __ic_load(west, RatioOxygen);
        double wc = __ic_load(west, RatioCarbonDioxide);
        double ct = __ic_load(core, Temperature);
        double cp = __ic_load(core, Pressure);
        double co = __ic_load(core, RatioOxygen);
        double cc = __ic_load(core, RatioCarbonDioxide);

        long long live = habitable(nt, np, no) + habitable(st, sp, so)
                 + habitable(et, ep, eo) + habitable(wt, wp, wo)
                 + habitable(ct, cp, co);

        double fouled = __ic_max(__ic_max(__ic_max(nc, sc), __ic_max(ec, wc)), cc);
        lowestPressure = __ic_min(__ic_min(__ic_min(np, sp), __ic_min(ep, wp)), cp);

        __ic_store(north, Setting, nt);
        __ic_store(south, Setting, st);
        __ic_store(east, Setting, et);
        __ic_store(west, Setting, wt);
        __ic_store(core, Setting, ct);
        __ic_store(display, Setting, live);
        __ic_store(display, Mode, lowestPressure);
        __ic_store(display, Ratio, fouled);
        __ic_yield();
    }
}
