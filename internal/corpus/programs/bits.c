// Bit and character helpers, exercising the integer scalar types, the bitwise
// operators, and batch writes keyed on compile-time prefab hashes. A light
// takes On and a console takes Setting; neither takes the other's. The entry
// point unpacks a packed setting into bytes each tick and republishes what it
// found.
//
// Everything here is written as a long, because everything here is counting
// rather than measuring: the one fractional value in sight is the raw device
// reading, which is narrowed with a cast where it is read.
//
// A long is still a double in the register -- the chip has no integer registers
// and no integer type -- so what the C type buys is exactness to 2^53 and the
// operators. The bitwise and shift operators take a long and nothing else, and
// everything they touch below is a byte field or a small running total, so none
// of it comes near either end of that window.

const dev source = d0;
const dev report = d1;

constexpr long kByteMask = 0xff;
constexpr long kHighBit = 0x80;
constexpr long kDelimiter = ',';
constexpr long kDecades[4] = {1, 10, 100, 1000};

long frame[4];

bool isDigit(long c) {
    return c >= '0' && c <= '9';
}

long digitValue(long c) {
    return isDigit(c) ? c - '0' : -1;
}

long popcount(long v) {
    long n = 0;
    do {
        n += v & 1;
        v >>= 1;
    } while (v != 0);
    return n;
}

long checksum(long buf[], long len) {
    long total = 0;
    long i = 0;
    while (i < len) {
        long b = buf[i] & kByteMask;
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

long scaled(long v, long decade) {
    return decade < 4 ? v * kDecades[decade] : v;
}

void announce(void) {
    long lights = __ic_hash("StructureWallLight");
    long consoles = __ic_hash("StructureConsoleLED5");
    __ic_store_batch(lights, On, 1);
    __ic_store_batch_named(consoles, __ic_hash("Airlock"), Setting, popcount(kByteMask));
}

void unpack(long packed) {
    for (long i = 0; i < 4; i++) {
        frame[i] = (packed >> (i * 8)) & kByteMask;
    }
}

void main(void) {
    announce();

    while (true) {
        unpack((long)__ic_load(source, Setting));

        long digit = digitValue(frame[0]);
        long value = 0;
        if (digit >= 0) {
            value = scaled(digit, popcount(frame[1]) & 3);
        }

        __ic_store(report, Setting, value + checksum(frame, 4));
        __ic_store(report, On, frame[3] == kDelimiter);
        __ic_yield();
    }
}
