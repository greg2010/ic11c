const dev in = d0;
const dev aux = d2;
long long a1[16];
double a2[16];
bool a3[8];
long long g4;
bool g5;
constexpr long long k6 = 173;
constexpr double k7 = -273.15;

long long toInt(double v) {
    if (v > -1000000.0 && v < 1000000.0) {
        return (long long)v;
    }
    return 0;
}

double f8(long long q9[], long long *q10, bool q11) {
    return __ic_load(in, Pressure);
}

long long f13(double q14, long long *q15) {
    double v17 = __ic_lerp((a3[((__ic_device_present(aux) ? g4 : 508365)) & 7] ? __ic_load(in, PressureExternal) : f8(a1, q15, __ic_isnan(__ic_load_slot(aux, 1, SlotType_Pressure)))), __ic_load_batch(__ic_hash("StructureGasSensor"), Ratio, Sum), __ic_atan(k7));
    return ((__ic_device_present(aux) ? (((q15[(((g5 ? 1 : 0) >> 18)) & 15]) & 65535) << 11) : toInt(__ic_load(aux, Open)))) % 101;
}

void main(void) {
    while (true) {
        a3[((k6 * f13(__ic_load_batch_named(__ic_hash("StructureNothingIsHere"), __ic_hash("alpha"), Error, Average), a1))) & 7] = ((f13((a2[(toInt(__ic_load(aux, RequiredPower))) & 15] / k7), a1) - k6) >= (true ? f13(__ic_load_batch_named(__ic_hash("StructureGasSensor"), __ic_hash("alpha"), Open, BatchMode_Minimum), a1) : -556697));
        __ic_yield();
    }
}
