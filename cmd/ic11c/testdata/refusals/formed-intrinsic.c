const dev in = d0;
const dev out = d1;
const dev aux = d2;
const dev sink = d3;
long long a1[8];
double a2[16];
bool a3[16];
long long a4[16];
bool g5;
bool g6;
double g7;
bool g8;
constexpr long long k9 = -251;

long long toInt(double v) {
    if (v > -1000000.0 && v < 1000000.0) {
        return (long long)v;
    }
    return 0;
}

long long f15(long long q16, long long q17, double q18) {
    return (((a4[(((g6 ? 1 : 0) * k9)) & 15] + (g5 ? 1 : 0)) + (q16 - (g5 ? 1 : 0)))) % 101;
}

void main(void) {
    while (true) {
        long long v19 = ((a4[((g6 ? 1 : 0)) & 15] >> 5)) % 65537;
        bool *p20 = &a3[(toInt(__ic_load_slot(aux, 2, MaturityRatio))) & 7];
        p20++;
        long long *p21 = &a1[(f15((((g6 ? 1 : 0) / 579)) % 17179869184, ((~49361)) % 101, ((double)g6))) & 3];
        p21 = p21 + (((v19 % 608)) & 3);
        switch (((a4[(((*p21) >> 3)) & 15] >> 1)) % 4) {
        case 0:
        case 1:
            a2[((~((p21 - a1) * (g6 ? 1 : 0)))) & 15] = __ic_clamp(((double)toInt(__ic_load(in, On))), __ic_trunc(0.5), __ic_load_batch_named(__ic_hash("StructureNothingIsHere"), __ic_hash("alpha"), Maximum, Average));
            *p21 = ((((*p21) / -969) & (toInt(__ic_load(in, PowerActual)) % -387))) % 65537;
            break;
        case 2:
            __ic_store(out, Quantity, (((double)g5) / (__ic_load_slot(in, 1, SlotType_Charge) + a2[((g5 ? (p20 - a3) : a4[(((((*p21)) & 255) << 10)) & 15])) & 15])));
            v19 = (((f15((f15(((toInt(__ic_load(aux, Pressure)) / 868)) % 17179869184, (((p21 - a1) - (*p21))) % 101, (g7 / __ic_load_slot(aux, 1, SlotType_Pressure)))) % 17179869184, ((toInt(__ic_load(in, RatioCarbonDioxide)) - f15((((g8 ? 1 : 0) ^ a4[(toInt(__ic_load_slot(in, 3, SlotType_Quantity))) & 15])) % 17179869184, ((p20 - a3)) % 101, __ic_load(in, Open)))) % 101, (__ic_load(aux, RequiredPower) - 6.283185307179586)) ^ v19) + (p21 - a1))) % 65537;
            break;
        default:
            __ic_store(sink, Lock, ((__ic_device_present(in) ? g7 : __ic_load(aux, Error)) * a2[(((*p21) + (g6 ? 1 : 0))) & 15]));
            break;
        }
        __ic_yield();
    }
}
