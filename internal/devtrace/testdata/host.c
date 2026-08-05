// The intrinsics a MicroC program calls, implemented for a native build of it.
//
// Nothing here decides anything. Every device operation and every machine
// function is a request the harness answers out of the world it owns, so the
// native run and the chip run meet one world and one set of numbers. What the
// native build contributes is the program's own computation, which is the whole
// of what the comparison is about; a second world model here would only be
// somewhere for the two runs to disagree without either being wrong.
//
// The two exceptions compute rather than ask. __ic_hash is a CRC-32 of a string
// literal, which the compiler folds at compile time, so a host that asked would
// leave the folding unchecked. __ic_isnan is C's own unordered comparison and
// needs nothing from the world.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "ic10_prelude.h"

// The MicroC entry point. Its name is rewritten on the command line so this
// file can own main and the program can be started and stopped from outside it.
void ic_microc_main(void);

// replyMax bounds one reply, which is a bit pattern in hexadecimal or a single
// character.
#define replyMax 32

static void fail(const char *what) {
    fprintf(stderr, "host: %s\n", what);
    exit(70);
}

static double fromBits(uint64_t bits) {
    double value;
    memcpy(&value, &bits, sizeof value);
    return value;
}

static uint64_t toBits(double value) {
    uint64_t bits;
    memcpy(&bits, &value, sizeof bits);
    return bits;
}

// reply flushes the request just written and reads the harness's answer.
static void reply(char *line) {
    if (fflush(stdout) != 0) {
        fail("the request stream closed");
    }
    if (!fgets(line, replyMax, stdin)) {
        fail("the harness closed the reply stream");
    }
}

// stopped is the harness ending the run. A MicroC control loop has no other way
// out, and any request may be the one answered with it: the harness stops after
// the segments it asked for, and it stops where the chip's own run stopped.
static bool stopped(const char *line) { return line[0] == 'x'; }

static double valueReply(void) {
    char line[replyMax];
    reply(line);
    if (stopped(line)) {
        exit(0);
    }
    return fromBits(strtoull(line + 1, nullptr, 16));
}

static void ackReply(void) {
    char line[replyMax];
    reply(line);
    if (stopped(line)) {
        exit(0);
    }
}

double __ic_load(dev d, ic10_logic t) {
    printf("l %d %d\n", (int)d, (int)t);
    return valueReply();
}

void __ic_store(dev d, ic10_logic t, double v) {
    printf("s %d %d #%016llx\n", (int)d, (int)t, (unsigned long long)toBits(v));
    ackReply();
}

double __ic_load_slot(dev d, long long slot, ic10_slot t) {
    printf("ls %d %lld %d\n", (int)d, slot, (int)t);
    return valueReply();
}

void __ic_store_slot(dev d, long long slot, ic10_slot t, double v) {
    printf("ss %d %lld %d #%016llx\n", (int)d, slot, (int)t, (unsigned long long)toBits(v));
    ackReply();
}

double __ic_load_batch(long long hash, ic10_logic t, ic10_batch m) {
    printf("lb %lld %d %d\n", hash, (int)t, (int)m);
    return valueReply();
}

void __ic_store_batch(long long hash, ic10_logic t, double v) {
    printf("sb %lld %d #%016llx\n", hash, (int)t, (unsigned long long)toBits(v));
    ackReply();
}

double __ic_load_batch_named(long long hash, long long name, ic10_logic t, ic10_batch m) {
    printf("lbn %lld %lld %d %d\n", hash, name, (int)t, (int)m);
    return valueReply();
}

void __ic_store_batch_named(long long hash, long long name, ic10_logic t, double v) {
    printf("sbn %lld %lld %d #%016llx\n", hash, name, (int)t, (unsigned long long)toBits(v));
    ackReply();
}

double __ic_load_batch_slot(long long hash, long long slot, ic10_slot t, ic10_batch m) {
    printf("lbs %lld %lld %d %d\n", hash, slot, (int)t, (int)m);
    return valueReply();
}

void __ic_store_batch_slot(long long hash, long long slot, ic10_slot t, double v) {
    printf("sbs %lld %lld %d #%016llx\n", hash, slot, (int)t, (unsigned long long)toBits(v));
    ackReply();
}

double __ic_load_batch_named_slot(long long hash, long long name, long long slot, ic10_slot t, ic10_batch m) {
    printf("lbns %lld %lld %lld %d %d\n", hash, name, slot, (int)t, (int)m);
    return valueReply();
}

double __ic_load_reagent(dev d, ic10_reagent m, long long hash) {
    printf("lr %d %d %lld\n", (int)d, (int)m, hash);
    return valueReply();
}

bool __ic_device_present(dev d) {
    printf("dse %d\n", (int)d);
    return valueReply() != 0;
}

void __ic_yield(void) {
    printf("y\n");
    ackReply();
}

void __ic_sleep(double seconds) {
    printf("sleep #%016llx\n", (unsigned long long)toBits(seconds));
    ackReply();
}

double __ic_rand(void) {
    printf("rand\n");
    return valueReply();
}

// The machine's functions are asked for rather than computed here for the same
// reason the world is: a bit-pattern comparison over two numeric libraries
// reports which library it ran on. What the comparison still establishes is that
// the emitted program reaches the right function with the right operands in the
// right order, which is the part belonging to the compiler.
static double machine1(const char *op, double a) {
    printf("%s #%016llx\n", op, (unsigned long long)toBits(a));
    return valueReply();
}

static double machine2(const char *op, double a, double b) {
    printf("%s #%016llx #%016llx\n", op, (unsigned long long)toBits(a), (unsigned long long)toBits(b));
    return valueReply();
}

static double machine3(const char *op, double a, double b, double c) {
    printf("%s #%016llx #%016llx #%016llx\n", op,
           (unsigned long long)toBits(a), (unsigned long long)toBits(b), (unsigned long long)toBits(c));
    return valueReply();
}

double __ic_sqrt(double v) { return machine1("sqrt", v); }
double __ic_abs(double v) { return machine1("abs", v); }
double __ic_sgn(double v) { return machine1("sgn", v); }
double __ic_round(double v) { return machine1("round", v); }
double __ic_trunc(double v) { return machine1("trunc", v); }
double __ic_ceil(double v) { return machine1("ceil", v); }
double __ic_floor(double v) { return machine1("floor", v); }
double __ic_log(double v) { return machine1("log", v); }
double __ic_exp(double v) { return machine1("exp", v); }
double __ic_sin(double v) { return machine1("sin", v); }
double __ic_cos(double v) { return machine1("cos", v); }
double __ic_tan(double v) { return machine1("tan", v); }
double __ic_asin(double v) { return machine1("asin", v); }
double __ic_acos(double v) { return machine1("acos", v); }
double __ic_atan(double v) { return machine1("atan", v); }

double __ic_min(double a, double b) { return machine2("min", a, b); }
double __ic_max(double a, double b) { return machine2("max", a, b); }
double __ic_pow(double a, double b) { return machine2("pow", a, b); }
double __ic_atan2(double a, double b) { return machine2("atan2", a, b); }

double __ic_clamp(double a, double b, double c) { return machine3("clamp", a, b, c); }
double __ic_lerp(double a, double b, double c) { return machine3("lerp", a, b, c); }

// An unordered comparison against itself, which is what the machine's isnan
// answers and what C guarantees for a NaN.
bool __ic_isnan(double v) { return v != v; }

// __ic_hash is the CRC-32 the game stamps into a prefab name, read back as a
// signed 32 bit integer. The compiler folds it, so computing it here is what
// holds that folding to the same table rather than to itself.
long long __ic_hash(const char *s) {
    uint32_t crc = 0xffffffffu;
    for (const unsigned char *p = (const unsigned char *)s; *p != '\0'; p++) {
        crc ^= *p;
        for (int bit = 0; bit < 8; bit++) {
            crc = (crc >> 1) ^ (0xedb88320u & (uint32_t)-(int32_t)(crc & 1u));
        }
    }
    return (long long)(int32_t)(crc ^ 0xffffffffu);
}

int main(void) {
    // Unbuffered replies and explicitly flushed requests, so a request cannot
    // sit in a buffer while the harness waits for it.
    setvbuf(stdin, nullptr, _IONBF, 0);
    ic_microc_main();
    // Reached only by a program whose main returns, which the harness needs to
    // tell apart from a run it stopped itself.
    printf("end\n");
    if (fflush(stdout) != 0) {
        fail("the request stream closed");
    }
    return 0;
}
