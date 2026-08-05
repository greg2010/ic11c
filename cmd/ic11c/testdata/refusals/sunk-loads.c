const dev in = d0;
const dev out = d1;
const dev aux = d2;
long long a1[32];
double a2[16];
long long a4[16];
bool g6;
bool g7;
constexpr long long k8 = -84;
constexpr double k9 = 1.0000000000000002;

long long toInt(double v) {
    if (v > -1000000.0 && v < 1000000.0) {
        return (long long)v;
    }
    return 0;
}

long long f10(double q11, double q12, double q13) {
    return ((-(a4[((k8 * a4[((a1[((k8 * (g6 ? 1 : 0))) & 31] >> 1)) & 15])) & 15]))) % 274877906944;
}

void main(void) {
    while (true) {
        g6 = (f10((293.15 / a2[(toInt(__ic_load(in, PressureExternal))) & 15]), __ic_clamp(__ic_load(aux, PressureInternal), 0.5, __ic_load(aux, On)), 1e9) > k8);
        g7 = (g6 == __ic_device_present(in));
        a2[((((a4[((toInt(__ic_load(in, On)) >> 3)) & 15]) & 65535) << 6)) & 15] = __ic_lerp((g7 ? k9 : 0.1), (__ic_load_slot(in, 3, FilterType) + __ic_load_slot(aux, 1, FreeSlots)), (__ic_load(in, PressureInternal) - k9));
        __ic_store(out, Pressure, ((g7 == g7) ? ((double)g6) : (g7 ? a2[((-799091 - a1[((-38 * a4[((f10((a2[(toInt(a2[((7 | toInt(__ic_load(in, Quantity)))) & 15])) & 15] / k9), __ic_lerp(__ic_load_slot(in, 1, PressureAir), k9, __ic_load(in, Pressure)), a2[((((a4[(((g6 ? 1 : 0) / 783)) & 15]) & 65535) << 18)) & 15]) % (511900 | 1))) & 15])) & 31])) & 15] : -1.0)));
        __ic_yield();
    }
}
