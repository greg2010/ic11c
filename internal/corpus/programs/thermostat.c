// A thermostat holding a room at a setpoint with hysteresis, so the heater
// does not chatter around the boundary. A room temperature is fractional --
// 293.15 K is where a habitat starts -- so the reading and the band are
// doubles, and only the value published back is narrowed to a long long. The
// narrowing is written as a cast because nothing narrows implicitly here: a
// double reaches a long long only where the line says so.
//
// Both pins say what they are wired to. Nothing enforces that at run time -- a
// housing reaches whatever the player plugged into it -- but it is what lets the
// compiler hold the Temperature read and the On write to what the game says
// those two devices answer for.

[[ic11c::prefab("StructureGasSensor")]] const dev sensor = d0;
[[ic11c::prefab("StructureWallHeater")]] const dev heater = d1;

constexpr double kSetpointKelvin = 293.15;
constexpr double kHysteresis = 2.0;
constexpr double kMaxKelvin = 400.0;

bool heaterOn;
long long lastReading;

double clampReading(double v, double lo, double hi);

double readTemperature(void) {
    return __ic_load(sensor, Temperature);
}

void driveHeater(bool on) {
    __ic_store(heater, On, on);
    heaterOn = on;
}

double clampReading(double v, double lo, double hi) {
    if (v < lo) {
        return lo;
    }
    if (v > hi) {
        return hi;
    }
    return v;
}

void main(void) {
    while (true) {
        double temp = clampReading(readTemperature(), 0.0, kMaxKelvin);
        lastReading = (long long)temp;

        if (temp < kSetpointKelvin - kHysteresis) {
            driveHeater(true);
        } else if (temp > kSetpointKelvin + kHysteresis) {
            driveHeater(false);
        }

        __ic_yield();
    }
}
