// Bit and character helpers, exercising the integer scalar types, the bitwise
// operators, and a batch write keyed on a compile-time prefab hash. The entry
// point unpacks a packed setting into bytes each tick and republishes what it
// found.
//
// Everything here really is an integer, so nothing is a double: the one
// fractional value in sight is the raw device reading, which is narrowed where
// it is read.

const dev source = d0;
const dev report = d1;

constexpr long long kByteMask = 0xff;
constexpr long long kHighBit = 0x80;
constexpr long long kDelimiter = ',';
constexpr long long kDecades[4] = {1, 10, 100, 1000};

long long frame[4];

bool isDigit(long long c) {
    return c >= '0' && c <= '9';
}

long long digitValue(long long c) {
    return isDigit(c) ? c - '0' : -1;
}

long long popcount(long long v) {
    long long n = 0;
    do {
        n += v & 1;
        v >>= 1;
    } while (v != 0);
    return n;
}

long long checksum(long long buf[], long long len) {
    long long total = 0;
    long long i = 0;
    while (i < len) {
        long long b = buf[i] & kByteMask;
        i++;
        if (b == 0) {
            continue;
        }
        total = (total << 1) ^ b;
        if (total & kHighBit) {
            total ^= kByteMask;
        }
        if (total < 0) {
            break;
        }
    }
    return total & kByteMask;
}

long long scaled(long long v, long long decade) {
    return decade < 4 ? v * kDecades[decade] : v;
}

void announce(void) {
    long long prefab = __ic_hash("StructureWallLight");
    __ic_store_batch(prefab, On, 1);
    __ic_store_batch_named(prefab, __ic_hash("Airlock"), Setting, popcount(kByteMask));
}

void unpack(long long packed) {
    for (long long i = 0; i < 4; i++) {
        frame[i] = (packed >> (i * 8)) & kByteMask;
    }
}

void main(void) {
    announce();

    while (true) {
        unpack((long long)__ic_load(source, Setting));

        long long digit = digitValue(frame[0]);
        long long value = 0;
        if (digit >= 0) {
            value = scaled(digit, popcount(frame[1]) & 3);
        }

        __ic_store(report, Setting, value + checksum(frame, 4));
        __ic_store(report, On, frame[3] == kDelimiter);
        __ic_yield();
    }
}
