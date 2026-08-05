// A bank of wall coolers held under a ceiling temperature, with a wired sensor
// beside it as a reference.
//
// A cooler reports no temperature of its own, so the ceiling is read from the
// gas sensors on the network and the coolers are only switched. The bank
// average is a batch read, which answers NaN when the network holds no sensor,
// and the long long it is narrowed to carries that NaN into the comparison
// below it. The wired reading beside it cannot be a NaN. The two comparisons
// are written against the same bound so that the difference between them in the
// emitted program is the guard and nothing else.

const dev sensor = d0;
const dev display = d1;

constexpr long long kCeilingKelvin = 300;

long long bankTrips;
long long sensorTrips;

void main(void) {
    long long coolers = __ic_hash("StructureWallCooler");
    long long sensors = __ic_hash("StructureGasSensor");

    while (true) {
        long long bank = (long long)__ic_load_batch(sensors, Temperature, Average);
        if (bank >= kCeilingKelvin) {
            bankTrips++;
            __ic_store_batch(coolers, On, 1);
        }

        long long wired = (long long)__ic_load(sensor, Temperature);
        if (wired >= kCeilingKelvin) {
            sensorTrips++;
        }

        __ic_store(display, Setting, bankTrips - sensorTrips);
        __ic_yield();
    }
}
