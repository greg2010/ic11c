package sema_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

func TestAnalyzeAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "scalar conversions",
			src: `bool flag;
void main(void) {
    long long x = true;
    bool b = 1;
    flag = b;
    x = (long long)flag;
    b = (bool)x;
}
`,
		},
		{
			name: "constexpr object names a constant",
			src: `constexpr long long kSize = 4;
long long buf[kSize];
void main(void) {
    switch (buf[0]) {
    case kSize:
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "brace initializers",
			src: `constexpr long long kOne = 1;
const long long kTable[3] = {1, 2, 3};
long long partial[4] = {1, 2};
long long empty[2] = {};
void main(void) {
    long long scalar = {5};
    long long local[2] = {kOne, 0};
    local[1] = kTable[2] + scalar;
}
`,
		},
		{
			name: "constant expressions fold",
			src: `constexpr long long kBits = 8;
constexpr long long kMask = (1 << kBits) - 1;
constexpr long long kInverse = ~kMask & 0xff;
constexpr long long kNegative = -kBits;
constexpr long long kShifted = kMask >> 2;
constexpr bool kEnabled = kMask > 0 && !false;
constexpr bool kCast = (bool)kMask;
constexpr long long kPicked = kEnabled ? kMask / 3 : kMask % 3;
constexpr long long kSum = (long long)kEnabled + (kBits >= 8 ? kInverse : kNegative) + kShifted;
constexpr long long kChar = 'A' + 1;
long long table[kBits];
void main(void) {
    switch (table[0]) {
    case kPicked:
        break;
    case kChar:
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "switch arms stack and terminate",
			src: `long long state;
void main(void) {
    switch (state) {
    case 0:
    case 1:
        state = 2;
        break;
    case 2:
        return;
    default:
        state = 0;
    }
}
`,
		},
		{
			name: "pointers within one object",
			src: `long long data[4];
void bump(long long *p) {
    *p = *p + 1;
}
void main(void) {
    long long *q = &data[0];
    q = q + 1;
    bump(q);
    bump(data);
    if (q == &data[2]) {
        *q = 0;
    }
}
`,
		},
		{
			name: "pointer arithmetic within one array",
			src: `long long data[8];
void main(void) {
    long long *p = &data[0];
    long long *q = data;
    long long *r = 1 + p;
    r -= 1;
    __ic_sleep(p - q + *r);
}
`,
		},
		{
			name: "prototype and mutual recursion",
			src: `bool odd(long long n);
bool even(long long n) {
    return n == 0 ? true : odd(n - 1);
}
bool odd(long long n) {
    return n == 0 ? false : even(n - 1);
}
void main(void) {
    __ic_store(d0, On, even(4));
}
`,
		},
		{
			name: "inner declarations shadow outer ones",
			src: `long long x;
void main(void) {
    long long x = 1;
    {
        bool x = true;
        if (x) {
            return;
        }
    }
    x = 2;
}
`,
		},
		{
			name: "every intrinsic",
			src: `double level;
void main(void) {
    level = __ic_load(d0, Pressure);
    __ic_store(db, On, 1);
    level = __ic_load_slot(d1, 0, Occupied);
    __ic_store_slot(d1, 1, OccupantHash, level);
    long long prefab = __ic_hash("StructureStubFurnace");
    long long storage = __ic_hash("StructureStubLocker");
    level = __ic_rand();
    __ic_store(d3, Setting, __ic_isnan(level));
    level = __ic_load_batch(prefab, Temperature, Average);
    __ic_store_batch(prefab, Setting, 0);
    level = __ic_load_batch_named(prefab, __ic_hash("north"), Temperature, Sum);
    __ic_store_batch_named(storage, __ic_hash("north"), On, 1);
    level = __ic_load_batch_slot(prefab, 0, Occupied, Maximum);
    __ic_store_batch_slot(storage, 0, SlotType_Lock, 1);
    level = __ic_load_batch_named_slot(prefab, __ic_hash("north"), 0, Occupied, BatchMode_Minimum);
    level = __ic_load_reagent(d2, Contents, prefab);
    if (__ic_device_present(d5)) {
        __ic_sleep(1);
    }
    level = __ic_clamp(__ic_min(1, 2), __ic_sqrt(4), __ic_lerp(0, 1, 1));
    level = __ic_abs(__ic_round(__ic_atan2(1, 2)));
    __ic_yield();
}
`,
		},
		{
			name: "control reaches no end that owes a value",
			src: `long long f(long long n) {
    if (n > 0) {
        return 1;
    } else {
        return 0;
    }
}
long long g(void) {
    while (true) {
        if (f(1) == 1) {
            return 2;
        }
    }
}
long long h(long long n) {
    switch (n) {
    case 0:
        return 0;
    default:
        return 1;
    }
}
long long k(long long n) {
    do {
        return n;
    } while (n > 0);
}
void main(void) {
    __ic_sleep(f(1) + g() + h(2) + k(3));
}
`,
		},
		{
			name: "loops and the jumps bound to them",
			src: `void main(void) {
    long long i = 0;
    for (i = 0; i < 10; i++) {
        if (i == 3) {
            continue;
        }
        switch (i) {
        case 4:
            continue;
        default:
            break;
        }
        if (i == 8) {
            break;
        }
    }
    do {
        i--;
    } while (i > 0);
}
`,
		},
		{
			name: "pointer to const",
			src: `const long long kLimit = 5;
long long values[4];
long long reading(const long long *p) {
    return *p;
}
void main(void) {
    const long long *p = &values[0];
    p = p + 1;
    long long value = 1;
    __ic_sleep(reading(&value) + reading(p) + kLimit);
}
`,
		},
		{
			name: "conditional arms are pointers into one object",
			src: `long long data[4];
void main(void) {
    long long *p = &data[0];
    long long *q = &data[2];
    long long *r = true ? p : q;
    *r = 1;
}
`,
		},
		{
			name: "conditional arms are pointers to const",
			src: `long long data[4];
void main(void) {
    const long long *p = &data[0];
    const long long *q = &data[2];
    const long long *r = true ? p : q;
    __ic_sleep(*r);
}
`,
		},
		{
			name: "unnamed parameter in a prototype",
			src: `long long add(long long, long long);
long long add(long long a, long long b) {
    return a + b;
}
void main(void) {
    __ic_sleep(add(1, 2));
}
`,
		},
		{
			name: "a long long controls a condition",
			src: `long long flags;
void main(void) {
    if (flags & 1) {
        flags = 0;
    }
    while (flags) {
        flags--;
    }
    flags = !flags ? 1 : 0;
}
`,
		},
		{
			name: "a long long widens to a double where the other operand is one",
			src: `double reading;
void main(void) {
    reading = __ic_load(d0, Temperature);
    double doubled = reading * 2;
    double shifted = 1 + reading;
    double halved = reading / 2.0;
    bool hot = reading >= 300;
    __ic_store(d1, Setting, doubled + shifted + halved);
    __ic_store(d1, On, hot);
}
`,
		},
		{
			name: "the scalar types convert where the language says they do",
			src: `double t;
long long n;
bool b;
void main(void) {
    t = __ic_load(d0, Temperature);
    n = (long long)t;
    b = (bool)t;
    t = n;
    t = b;
    t = (double)n;
    __ic_store(d1, Setting, t + n);
}
`,
		},
		{
			name: "a double is compared, incremented, and accumulated",
			src: `void main(void) {
    double total = 0.0;
    for (long long i = 0; i < 4; i++) {
        double sample = __ic_load(d0, Pressure);
        if (sample > 0.5 && sample != 1.0) {
            total += sample;
        }
        total++;
    }
    __ic_store(d1, Setting, -total);
}
`,
		},
		{
			name: "the machine's own constants are named",
			src: `void main(void) {
    double radians = 90.0 * deg2rad;
    double degrees = radians * rad2deg;
    __ic_store(d1, Setting, pi + tau + epsilon + rgas + degrees);
}
`,
		},
		{
			name: "a machine constant folds into a const object",
			src: `const double kQuarterTurn = pi / 2.0;
void main(void) {
    __ic_store(d1, Setting, __ic_sin(kQuarterTurn));
}
`,
		},
		{
			name: "a dev names a pin, a constexpr object, and a parameter",
			src: `const dev sensor = d0;
constexpr dev remote = d3;
void publish(dev target, double value) {
    __ic_store(target, Setting, value);
}
void main(void) {
    double v = __ic_load(sensor, Temperature);
    publish(remote, v);
    publish(d1, v);
    publish(db, v);
    if (__ic_device_present(sensor)) {
        __ic_yield();
    }
}
`,
		},
		{
			name: "a dev parameter is passed on to another",
			src: `void inner(dev target) {
    __ic_store(target, On, 1);
}
void outer(dev target) {
    inner(target);
}
void main(void) {
    outer(d2);
}
`,
		},
		{
			name: "array parameter decays",
			src: `long long total(long long values[], long long n) {
    long long sum = 0;
    for (long long i = 0; i < n; i++) {
        sum += values[i];
    }
    return sum;
}
long long fixed(long long values[4]) {
    return values[0];
}
long long table[4];
void main(void) {
    __ic_sleep(total(table, 4) + fixed(table));
}
`,
		},
		// long and long long are one type, so each row writes both spellings
		// where a second type would be caught: a redeclaration, a call, a
		// pointee, an operand.
		{
			name: "a prototype and its definition spell the type differently",
			src: `long long f(long x);
long f(long long x) {
    return x;
}
void main(void) {
    __ic_sleep(f(1));
}
`,
		},
		{
			name: "two declarations of one function spell the type differently",
			src: `void f(long);
void f(long long x) {
    __ic_sleep(x);
}
void main(void) {
    f(1);
}
`,
		},
		{
			name: "a pointer to the short spelling addresses the canonical one",
			src: `long g(const long *p) {
    return *p;
}
void main(void) {
    long long a = 1;
    __ic_sleep(g(&a));
}
`,
		},
		{
			name: "the two spellings are one type in an expression",
			src: `void main(void) {
    long a = 1;
    long long b = 2;
    long c = a + b;
    __ic_sleep(c);
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, tt.src)
			if len(diags) != 0 {
				t.Errorf("analysis rejected a valid program:\n%s", diags.String())
			}
		})
	}
}

func TestAnalyzeRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "void variable",
			src: `/*!*/void x;
void main(void) {}
`,
			want: "may not have type void",
		},
		{
			name: "void parameter",
			src: `void f(/*!*/void x);
void main(void) {}
`,
			want: "a parameter may not have type void",
		},
		{
			name: "unnamed parameter in a definition",
			src: `void f(/*!*/long long) {
}
void main(void) {}
`,
			want: "must be named in a definition",
		},
		{
			name: "reserved name",
			src: `long long /*!*/__ic_count;
void main(void) {}
`,
			want: "reserved for intrinsics",
		},
		{
			name: "undeclared name",
			src: `void main(void) {
    long long x = /*!*/y;
}
`,
			want: "undeclared name 'y'",
		},
		{
			name: "redeclaration",
			src: `long long x;
long long /*!*/x;
void main(void) {}
`,
			want: "already declared",
		},
		{
			name: "const object without an initializer",
			src: `const long long /*!*/k;
void main(void) {}
`,
			want: "requires an initializer",
		},
		{
			name: "assignment to a const object",
			src: `const long long k = 1;
void main(void) {
    /*!*/k = 2;
}
`,
			want: "'k' is const and may not be assigned",
		},
		{
			name: "array bound is not constant",
			src: `long long n;
long long a[/*!*/n];
void main(void) {}
`,
			want: "an array bound must be a constant expression",
		},
		{
			name: "array bound is not positive",
			src: `long long a[/*!*/0];
void main(void) {}
`,
			want: "an array bound must be positive",
		},
		{
			name: "global initializer is not constant",
			src: `long long f(void) {
    return 1;
}
long long g = /*!*/f();
void main(void) {}
`,
			want: "a global initializer must be a constant expression",
		},
		{
			name: "brace initializer element is not constant",
			src: `long long n;
void main(void) {
    long long a[2] = {/*!*/n, 1};
}
`,
			want: "an element of a brace initializer must be a constant expression",
		},
		{
			name: "too many brace initializer elements",
			src: `long long a[2] = {1, 2, /*!*/3};
void main(void) {}
`,
			want: "supplies 3 elements for an array of 2",
		},
		{
			name: "brace initializer for a scalar",
			src: `void main(void) {
    long long x = /*!*/{1, 2};
}
`,
			want: "must supply exactly one value",
		},
		{
			name: "case label is not constant",
			src: `long long n;
void main(void) {
    switch (n) {
    case /*!*/n:
        break;
    }
}
`,
			want: "a case label must be a constant expression",
		},
		{
			name: "duplicate case label",
			src: `long long n;
void main(void) {
    switch (n) {
    case 1:
        break;
    case /*!*/1:
        break;
    }
}
`,
			want: "duplicate case label 1",
		},
		{
			name: "two default labels",
			src: `long long n;
void main(void) {
    switch (n) {
    default:
        break;
    /*!*/default:
        break;
    }
}
`,
			want: "at most one default label",
		},
		{
			name: "case body falls through",
			src: `long long n;
void main(void) {
    switch (n) {
    /*!*/case 1:
        n = 2;
    default:
        break;
    }
}
`,
			want: "control falls out of the body of case 1",
		},
		{
			name: "case label does not match the tag",
			src: `bool flag;
void main(void) {
    switch (flag) {
    case /*!*/1:
        break;
    }
}
`,
			want: "a case label of a bool switch must be bool",
		},
		{
			name: "address of an array",
			src: `long long a[2];
void main(void) {
    long long *p = /*!*/&a;
}
`,
			want: "the address of an array is not expressible",
		},
		{
			name: "string literal outside __ic_hash",
			src: `long long x = /*!*/"abc";
void main(void) {}
`,
			want: "valid only as the argument of __ic_hash",
		},
		{
			name: "unknown intrinsic",
			src: `void main(void) {
    /*!*/__ic_frob(1);
}
`,
			want: "'__ic_frob' is not an intrinsic",
		},
		{
			name: "intrinsic arity",
			src: `void main(void) {
    __ic_load/*!*/(d0);
}
`,
			want: "__ic_load expects 2 arguments, found 1",
		},
		{
			name: "unknown logic type",
			src: `void main(void) {
    double v = __ic_load(d0, /*!*/Nonsense);
}
`,
			want: "'Nonsense' is not a logic type",
		},
		{
			name: "device pin out of range",
			src: `void main(void) {
    double v = __ic_load(/*!*/d9, Pressure);
}
`,
			want: "'d9' is not a device",
		},
		{
			name: "logic type must be a name",
			src: `void main(void) {
    double v = __ic_load(d0, /*!*/1);
}
`,
			want: "the logic type argument of __ic_load must be written as a name",
		},
		{
			name: "slot index is not constant",
			src: `long long i;
void main(void) {
    double v = __ic_load_slot(d0, /*!*/i, Occupied);
}
`,
			want: "the slot index of __ic_load_slot must be a constant expression",
		},
		{
			// The chip resolves the operand when the line is assembled and checks
			// nothing, so a negative index reaches the device at run time and
			// faults there once per tick for as long as the chip runs.
			name: "slot index below zero",
			src: `void main(void) {
    double v = __ic_load_slot(d0, /*!*/-1, Occupied);
}
`,
			want: "the slot index of __ic_load_slot is -1, and a device's slots are numbered from 0",
		},
		{
			name: "__ic_hash without a string literal",
			src: `void main(void) {
    long long h = __ic_hash(/*!*/1);
}
`,
			want: "__ic_hash takes a string literal",
		},
		{
			name: "conditional merges two objects",
			src: `long long a;
long long b;
void main(void) {
    bool c = true;
    long long *p = c /*!*/? &a : &b;
}
`,
			want: "the arms of '?:' designate different objects",
		},
		{
			name: "comparing pointers into different objects",
			src: `long long a;
long long b;
void main(void) {
    bool eq = &a /*!*/== &b;
}
`,
			want: "must trace to exactly one object",
		},
		{
			name: "pointer assigned a second object",
			src: `long long a;
long long b;
void main(void) {
    long long *p = &a;
    /*!*/p = &b;
}
`,
			want: "'p' is assigned a pointer to a different object",
		},
		{
			name: "program without main",
			src: `/*!*/long long x;
`,
			want: "does not define 'void main(void)'",
		},
		{
			name: "main declared but never defined",
			src: `void /*!*/main(void);
`,
			want: "'main' is declared but never defined",
		},
		{
			name: "main declared but never defined, and called",
			src: `void /*!*/main(void);
void f(void) {
    main();
}
`,
			want: "'main' is declared but never defined",
		},
		{
			name: "main with the wrong signature",
			src: `long long /*!*/main(void) {
    return 0;
}
`,
			want: "'main' must be declared 'void main(void)'",
		},
		{
			name: "control reaches the end of a non-void function",
			src: `long long f(long long n) {
    if (n > 0) {
        return 1;
    }
/*!*/}
void main(void) {}
`,
			want: "control reaches the end of 'f'",
		},
		{
			name: "break outside a loop or switch",
			src: `void main(void) {
    /*!*/break;
}
`,
			want: "break is only valid inside a loop or a switch",
		},
		{
			name: "continue in a switch outside a loop",
			src: `void main(void) {
    switch (1) {
    case 1:
        /*!*/continue;
    }
}
`,
			want: "continue is only valid inside a loop",
		},
		{
			name: "wrong argument count",
			src: `void f(long long a) {
    __ic_sleep(a);
}
void main(void) {
    f/*!*/();
}
`,
			want: "'f' expects 1 argument, found 0",
		},
		{
			name: "argument of the wrong type",
			src: `void f(long long *p) {
    *p = 1;
}
void main(void) {
    f(/*!*/1);
}
`,
			want: "cannot use long long as long long * in an argument to 'f'",
		},
		{
			name: "arithmetic on a bool",
			src: `bool flag;
void main(void) {
    long long x = /*!*/flag + 1;
}
`,
			want: "must be a long long or a double, found bool",
		},
		{
			name: "comparing a long long with a bool",
			src: `bool flag;
long long n;
void main(void) {
    if (flag /*!*/== n) {
        n = 0;
    }
}
`,
			want: "cannot compare bool with long long",
		},
		{
			name: "conditional arms of different types",
			src: `bool flag;
void main(void) {
    long long x = flag /*!*/? 1 : true;
}
`,
			want: "the arms of '?:' must have the same type",
		},
		{
			name: "conditional arms differ in the const of the pointee",
			src: `long long data[4];
void main(void) {
    const long long *p = &data[0];
    long long *q = &data[2];
    const long long *r = true /*!*/? p : q;
}
`,
			want: "found const long long * and long long *",
		},
		{
			name: "conditional arms point to different types",
			src: `long long ints[2];
bool flags[2];
void main(void) {
    long long *p = &ints[0];
    bool *q = &flags[0];
    long long *r = true /*!*/? p : q;
}
`,
			want: "found long long * and bool *",
		},
		{
			name: "indexing a scalar",
			src: `long long x;
void main(void) {
    long long y = x/*!*/[0];
}
`,
			want: "cannot index long long",
		},
		{
			name: "dereferencing a scalar",
			src: `long long x;
void main(void) {
    long long y = /*!*/*x;
}
`,
			want: "the operand of unary '*' must be a pointer",
		},
		{
			name: "function name as a value",
			src: `void f(void) {
}
void main(void) {
    long long x = /*!*/f;
}
`,
			want: "a function name is not a value",
		},
		{
			name: "calling something that is not a function",
			src: `long long x;
void main(void) {
    /*!*/x();
}
`,
			want: "'x' is a global, not a function",
		},
		{
			name: "assigning to an array",
			src: `long long a[2];
void main(void) {
    /*!*/a = 0;
}
`,
			want: "an array may not be assigned",
		},
		{
			name: "returning a value from void",
			src: `void f(void) {
    return /*!*/1;
}
void main(void) {}
`,
			want: "returns void and may not return a value",
		},
		{
			name: "returning no value from a non-void function",
			src: `long long f(void) {
    /*!*/return;
}
void main(void) {}
`,
			want: "'f' must return a value of type long long",
		},
		{
			name: "shift count out of range",
			src: `const long long k = (long long)1 /*!*/<< 64;
void main(void) {}
`,
			want: "a shift count must be between 0 and 63",
		},
		{
			name: "bitwise result beyond the exact range",
			src: `const long long k = (long long)1 /*!*/<< 60;
void main(void) {}
`,
			want: "a left shift that reaches 2^53 answers with -2^53",
		},
		{
			name: "division by zero in a constant expression",
			src: `const long long k = 1 /*!*// 0;
void main(void) {}
`,
			want: "division by zero in a constant expression",
		},
		{
			name: "definition disagrees with the prototype",
			src: `long long f(void);
/*!*/void f(void) {
}
void main(void) {}
`,
			want: "returning long long, not void",
		},
		{
			name: "prototype never defined",
			src: `long long /*!*/f(void);
void main(void) {
    __ic_sleep(f());
}
`,
			want: "'f' is called but never defined",
		},
		{
			name: "intrinsic used as a value",
			src: `void main(void) {
    long long x = /*!*/__ic_yield;
}
`,
			want: "the intrinsic '__ic_yield' may only be called",
		},
		{
			name: "duplicate parameter name",
			src: `void f(long long a, long long /*!*/a) {
    __ic_sleep(a);
}
void main(void) {}
`,
			want: "'a' is already declared",
		},
		{
			name: "assignment through a pointer to const",
			src: `long long values[4];
void main(void) {
    const long long *p = &values[0];
    /*!*/*p = 1;
}
`,
			want: "the object is const and may not be assigned",
		},
		{
			name: "address of something that is not an object",
			src: `long long x;
void main(void) {
    long long *p = /*!*/&(x + 1);
}
`,
			want: "the operand of unary '&' must name an object",
		},
		{
			name: "compound assignment on a pointer",
			src: `long long data[4];
void main(void) {
    long long *p = &data[0];
    p /*!*/*= 2;
}
`,
			want: "'*=' does not apply to a pointer",
		},
		{
			name: "a loop a continue can leave does not terminate",
			src: `long long f(long long n) {
    do {
        continue;
    } while (n > 0);
/*!*/}
void main(void) {}
`,
			want: "control reaches the end of 'f'",
		},
		{
			name: "increment of a bool",
			src: `bool flag;
void main(void) {
    flag/*!*/++;
}
`,
			want: "must be a long long, a double, or a pointer, found bool",
		},

		// double. Nothing narrows implicitly: the silent truncation is what made
		// a fractional reading behave as an integer in the first place.
		{
			name: "a double does not become a long long on its own",
			src: `void main(void) {
    long long t = /*!*/__ic_load(d0, Temperature);
}
`,
			want: "cannot use double as long long",
		},
		{
			name: "a double does not become a long long on assignment",
			src: `long long t;
void main(void) {
    t = /*!*/__ic_load(d0, Temperature);
}
`,
			want: "cannot use double as long long in an assignment",
		},
		{
			name: "a double does not become a long long on return",
			src: `long long reading(void) {
    return /*!*/__ic_load(d0, Temperature);
}
void main(void) {
    __ic_sleep(reading());
}
`,
			want: "cannot use double as long long in a return from 'reading'",
		},
		{
			name: "a double does not become a long long as an argument",
			src: `void take(long long n) {
    __ic_sleep(n);
}
void main(void) {
    take(/*!*/__ic_load(d0, Temperature));
}
`,
			want: "cannot use double as long long in an argument to 'take'",
		},
		{
			name: "a double is not a condition",
			src: `void main(void) {
    if (/*!*/__ic_load(d0, Temperature)) {
        __ic_yield();
    }
}
`,
			want: "compare the value rather than testing it against zero",
		},
		{
			name: "a double takes no remainder",
			src: `void main(void) {
    double t = __ic_load(d0, Temperature);
    __ic_store(d1, Setting, /*!*/t % 2);
}
`,
			want: "the left operand of '%' must be a long long, found double",
		},
		{
			name: "a double takes no shift",
			src: `void main(void) {
    double t = __ic_load(d0, Temperature);
    __ic_store(d1, Setting, /*!*/t << 1);
}
`,
			want: "the left operand of '<<' must be a long long, found double",
		},
		{
			name: "a double takes no bitwise complement",
			src: `void main(void) {
    double t = __ic_load(d0, Temperature);
    __ic_store(d1, Setting, ~/*!*/t);
}
`,
			want: "the operand of unary '~' must be a long long, found double",
		},
		{
			name: "a bool does not widen to a double under an operator",
			src: `void main(void) {
    bool b = __ic_device_present(d0);
    __ic_store(d1, Setting, /*!*/b + 1.5);
}
`,
			want: "the left operand of '+' must be a long long or a double, found bool",
		},
		{
			name: "a case label of a long long switch is not a double",
			src: `void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case /*!*/1.5:
        break;
    }
}
`,
			want: "a case label of a long long switch must be long long, found double",
		},
		{
			name: "an array bound is not a double",
			src: `long long a[/*!*/2.0];
void main(void) {
    __ic_sleep(a[0]);
}
`,
			want: "an array bound must be a long long, found double",
		},
		{
			name: "a long long target does not take a double right operand",
			src: `void main(void) {
    long long n = 0;
    n += /*!*/__ic_load(d0, Setting);
}
`,
			want: "the right operand of '+=' must be a long long, found double",
		},

		// dev. A device is resolved when the chip assembles the line, so every
		// rule here is about keeping it a compile-time name.
		{
			name: "a dev must be const",
			src: `/*!*/dev sensor = d0;
void main(void) {
    __ic_store(sensor, On, 1);
}
`,
			want: "declare it const",
		},
		{
			name: "a dev requires an initializer",
			src: `const dev /*!*/sensor;
void main(void) {
    __ic_store(sensor, On, 1);
}
`,
			want: "requires an initializer naming a device",
		},
		{
			name: "a dev initializer names a device",
			src: `const dev sensor = /*!*/1;
void main(void) {
    __ic_store(sensor, On, 1);
}
`,
			want: "must name a device",
		},
		{
			name: "a dev may not be assigned",
			src: `const dev sensor = d0;
void main(void) {
    /*!*/sensor = sensor;
}
`,
			want: "it may not be assigned",
		},
		{
			name: "a dev is not a value",
			src: `const dev sensor = d0;
void main(void) {
    __ic_store(d1, Setting, /*!*/sensor + 1);
}
`,
			want: "the left operand of '+' must be a long long or a double, found const dev",
		},
		{
			name: "an array of dev has no slots to occupy",
			src: `dev pins/*!*/[2];
void main(void) {
    __ic_yield();
}
`,
			want: "an array element may not have type dev",
		},
		{
			name: "a pointer to dev is not expressible",
			src: `void f(dev /*!*/*p) {
}
void main(void) {
    __ic_yield();
}
`,
			want: "a pointer to dev is not supported in MicroC",
		},
		// A parameter's array decays to a pointer, so these are the third and
		// fourth spelling of the two above. Every spelling of a type has to be
		// refused by the same rule, or the one that is not becomes the way to
		// write it.
		{
			name: "a dev array parameter with no bound",
			src: `void f(dev pins/*!*/[]) {
}
void main(void) {
    __ic_yield();
}
`,
			want: "an array element may not have type dev",
		},
		{
			name: "a dev array parameter with a bound",
			src: `void f(dev pins/*!*/[2]) {
}
void main(void) {
    __ic_yield();
}
`,
			want: "an array element may not have type dev",
		},
		{
			name: "a void array parameter",
			src: `void f(void pins/*!*/[]) {
}
void main(void) {
    __ic_yield();
}
`,
			want: "an array element may not have type void",
		},
		{
			name: "the address of a dev object",
			src: `const dev sensor = d0;
void main(void) {
    long long *p = /*!*/&sensor;
    __ic_store(sensor, On, 1);
}
`,
			want: "the address of a dev is not expressible in MicroC",
		},
		{
			name: "the address of a dev parameter",
			src: `void drive(dev target) {
    long long *p = /*!*/&target;
    __ic_store(target, On, 1);
}
void main(void) {
    drive(d0);
}
`,
			want: "the address of a dev is not expressible in MicroC",
		},
		{
			name: "the address of a machine constant",
			src: `void main(void) {
    const double *p = /*!*/&pi;
    __ic_store(d0, Setting, *p);
}
`,
			want: "the address of 'pi' is not expressible in MicroC",
		},
		{
			name: "the address of a constexpr global",
			src: `constexpr double kHalf = 0.5;
void main(void) {
    const double *p = /*!*/&kHalf;
    __ic_store(d0, Setting, *p);
}
`,
			want: "the address of 'kHalf' is not expressible in MicroC",
		},
		{
			name: "the address of a constexpr local",
			src: `void main(void) {
    constexpr double kHalf = 0.5;
    const double *p = /*!*/&kHalf;
    __ic_store(d0, Setting, *p);
}
`,
			want: "the address of 'kHalf' is not expressible in MicroC",
		},
		{
			name: "two devs compared",
			src: `const dev a = d0;
const dev b = d1;
void main(void) {
    if (a /*!*/== b) {
        __ic_yield();
    }
}
`,
			want: "a dev names a device pin rather than a value",
		},
		{
			name: "a function may not return a dev",
			src: `/*!*/dev pick(void);
void main(void) {
    __ic_yield();
}
`,
			want: "a function may not return a dev",
		},
		{
			// A body cannot be spliced into itself, so a recursive function is
			// compiled out of line and its parameter would have to travel in a
			// register the chip does not read as a device.
			name: "a dev parameter on a function that cannot be inlined",
			src: `void drain(dev /*!*/target, long long n) {
    __ic_store(target, On, 1);
    if (n > 0) {
        drain(target, n - 1);
    }
}
void main(void) {
    drain(d0, 3);
}
`,
			want: "compiled out of line rather than inlined",
		},
		{
			name: "a dev object may not be initialized from a dev parameter",
			src: `void drive(dev target) {
    const dev copy = /*!*/target;
    __ic_store(copy, On, 1);
}
void main(void) {
    drive(d0);
}
`,
			want: "has to name one device for the whole program",
		},
		{
			name: "a device pin the housing does not have",
			src: `const dev sensor = /*!*/d7;
void main(void) {
    __ic_store(sensor, On, 1);
}
`,
			want: "'d7' is not a device",
		},
		{
			// An object declared with the short spelling is named by the
			// canonical one in every message about it.
			name: "the short spelling is reported as the canonical one",
			src: `void main(void) {
    long a = 1;
    double *p = /*!*/&a;
}
`,
			want: "cannot use long long * as double * in the initializer of 'p'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestAnalyzeReportsIndependentErrorsTogether proves analysis recovers at
// declaration and statement boundaries rather than stopping at the first
// problem.
func TestAnalyzeReportsIndependentErrorsTogether(t *testing.T) {
	const src = `void f(void x);
long long g(void) {
}
void main(void) {
    break;
    undeclaredName();
}
`
	want := []struct {
		line int
		msg  string
	}{
		{1, "a parameter may not have type void"},
		{3, "control reaches the end of 'g'"},
		{5, "break is only valid inside a loop or a switch"},
		{6, "undeclared name 'undeclaredName'"},
	}

	_, diags := analyze(t, src)
	if len(diags) != len(want) {
		t.Fatalf("got %d diagnostics, want %d:\n%s", len(diags), len(want), diags.String())
	}
	for i, w := range want {
		if diags[i].Pos.Line != w.line {
			t.Errorf("diagnostic %d at line %d, want line %d: %s", i, diags[i].Pos.Line, w.line, diags[i].Msg)
		}
		if !strings.Contains(diags[i].Msg, w.msg) {
			t.Errorf("diagnostic %d is %q, want it to name %q", i, diags[i].Msg, w.msg)
		}
	}
}

// TestAnalyzeReportsOneErrorPerRootCause pins the rule that an expression
// analysis could not type absorbs every later operation silently.
func TestAnalyzeReportsOneErrorPerRootCause(t *testing.T) {
	const src = `void main(void) {
    long long x = missing + missing * 2 - (missing & 1);
}
`
	_, diags := analyze(t, src)
	if len(diags) != 3 {
		t.Fatalf("got %d diagnostics, want one per use of the undeclared name:\n%s", len(diags), diags.String())
	}
	for _, d := range diags {
		if !strings.Contains(d.Msg, "undeclared name 'missing'") {
			t.Errorf("unexpected cascade diagnostic: %s", d)
		}
	}
}

// errorCap and warningCap are how many diagnostics of each severity analysis
// reports before the rest are noise. They are written here rather than read
// from the analyser because a cap the test derived from the code under test
// would move with it.
//
// They are two constants because the analyser holds two budgets. A single one
// would pass whatever the analyser did as long as the two stayed equal, which
// is the property these tests exist to doubt.
const (
	errorCap   = 64
	warningCap = 64
)

// undeclaredReadLines is n statements each naming something that does not
// exist, which is n errors and nothing else.
func undeclaredReadLines(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "    long long v%d = missing%d;\n", i, i)
	}
	return b.String()
}

// undeclaredReads is [undeclaredReadLines] as a whole program.
func undeclaredReads(n int) string {
	return "void main(void) {\n" + undeclaredReadLines(n) + "}\n"
}

// deprecatedWrites is a program that writes a retired logic type n times, which
// is n warnings and nothing else. tail is written into main after them.
func deprecatedWrites(n int, tail string) string {
	var b strings.Builder
	b.WriteString("void main(void) {\n    long long z;\n")
	for i := range n {
		fmt.Fprintf(&b, "    __ic_store(d0, PlantHealth1, %d);\n", i)
	}
	b.WriteString(tail)
	b.WriteString("}\n")
	return b.String()
}

// TestDiagnosticCapsAreCountedPerSeverity pins the point past which more
// messages are noise, and that each severity reaches it on its own.
//
// The cap is there so a cascade of derived messages does not bury the first real
// one, which is a reason that applies to errors and warnings apart rather than
// to their sum. Counting them together let a program that says too much about
// itself in warnings close the list with an error and be rejected for it, having
// nothing wrong with it at all.
//
// Reaching a cap therefore adds a note and no diagnostic: a run that overflowed
// reports exactly as many errors as one that filled the cap exactly, and closes
// the list with the note the other does not carry.
func TestDiagnosticCapsAreCountedPerSeverity(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantErrors   int
		wantWarnings int
		// wantMarkers is the message each severity closes its own list with. A
		// severity absent from it must carry no marker at all.
		wantMarkers map[source.Severity]string
	}{
		{
			name:       "errors filling the cap exactly",
			src:        undeclaredReads(errorCap),
			wantErrors: errorCap,
		},
		{
			name:        "errors one past the cap",
			src:         undeclaredReads(errorCap + 1),
			wantErrors:  errorCap,
			wantMarkers: map[source.Severity]string{source.Error: "too many errors"},
		},
		{
			name:         "warnings filling the cap exactly",
			src:          deprecatedWrites(warningCap, ""),
			wantWarnings: warningCap,
		},
		{
			name:         "warnings one past the cap",
			src:          deprecatedWrites(warningCap+1, ""),
			wantWarnings: warningCap,
			wantMarkers:  map[source.Severity]string{source.Warning: "too many warnings"},
		},
		{
			// The case the two caps exist for: each severity reaches its own
			// limit and closes its own list, and the program is rejected for the
			// errors rather than for how much it said.
			name:         "both severities past their own cap",
			src:          deprecatedWrites(warningCap+1, undeclaredReadLines(errorCap+1)),
			wantErrors:   errorCap,
			wantWarnings: warningCap,
			wantMarkers: map[source.Severity]string{
				source.Error:   "too many errors",
				source.Warning: "too many warnings",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, tt.src)
			markers := capMarkers(t, diags, len(tt.wantMarkers))
			errors := diags.Errors()
			warnings := len(diags) - errors - len(markers)
			if errors != tt.wantErrors || warnings != tt.wantWarnings {
				t.Fatalf("got %d errors and %d warnings, want %d and %d:\n%s", errors, warnings, tt.wantErrors, tt.wantWarnings, diags.String())
			}
			if !maps.Equal(markers, tt.wantMarkers) {
				t.Errorf("the lists are closed with %v, want %v; a marker at the wrong severity decides whether the program is emitted", markers, tt.wantMarkers)
			}
		})
	}
}

// capMarkers is the message each severity closes its list with, keyed by that
// severity, and empty where nothing overflowed.
//
// A marker terminates a list, so all of them sit in the last tail diagnostics
// and one found ahead of that is reported here. The marker takes the position of
// the first message withheld, so appending it before the list is ordered sorts
// it to wherever that position falls — the middle, whenever the other severity
// is still reporting past it.
func capMarkers(t *testing.T, diags source.DiagnosticList, tail int) map[source.Severity]string {
	t.Helper()
	markers := make(map[source.Severity]string)
	for i, d := range diags {
		if !d.Overflow {
			continue
		}
		markers[d.Severity] = d.Msg
		if i < len(diags)-tail {
			t.Errorf("the %s list is closed at index %d with %d diagnostics after it, so the report reads as ending where it did not", d.Severity, i, len(diags)-1-i)
		}
	}
	return markers
}

// TestWarningVolumeDoesNotSuppressDefiniteAssignment pins the analysis that
// warnings must never gate.
//
// Definite assignment runs only where checking found no error, since a name that
// did not resolve would make every mention of it look like a read of something
// else. A warning is not such a name, and nothing downstream reports the mistake
// this analysis exists for: IR generation gives the read no value, the optimizer
// folds through the undef, and the emitted program does nothing and says
// nothing. A program is therefore checked the same whether it drew three
// warnings or more than the cap admits.
func TestWarningVolumeDoesNotSuppressDefiniteAssignment(t *testing.T) {
	tests := []struct {
		name     string
		warnings int
	}{
		{name: "well inside the cap", warnings: 3},
		{name: "past the cap", warnings: warningCap + 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, deprecatedWrites(tt.warnings, "    __ic_store(d1, On, z);\n"))
			found := false
			for _, d := range diags {
				if d.Severity == source.Error && strings.Contains(d.Msg, "'z' is read here without having been assigned") {
					found = true
				}
			}
			if !found {
				t.Fatalf("the read of an unassigned register is not reported under %d warnings:\n%s", tt.warnings, diags.String())
			}
		})
	}
}

// TestConstantArrayIndexIsHeldToTheBound covers the subscript whose slot is
// decided here and nowhere later.
//
// The declared length belongs to the array type, and a constant index is folded
// out of the IR long before instruction selection has an address to check
// against the memory array. What the chip does with the address it is handed is
// not a diagnostic either: get answers a slot outside the array with the unknown
// error and put with a stack overflow, once per tick for as long as the program
// runs.
func TestConstantArrayIndexIsHeldToTheBound(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a write far past a global array",
			src: `long long a[4];
void main(void) {
    a[/*!*/9000] = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 9000",
		},
		{
			name: "a read one past the end",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/4]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 4",
		},
		{
			name: "a negative index",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/-1]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found -1",
		},
		{
			name: "an index past a local array",
			src: `void main(void) {
    long long buf[2] = {1, 2};
    __ic_store(d0, Setting, buf[/*!*/2]);
}
`,
			want: "an array index must be between 0 and 1, the last index of 'buf', found 2",
		},
		{
			name: "an index a constexpr object names",
			src: `constexpr long long kSlot = 9000;
long long a[4];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/kSlot]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 9000",
		},
		{
			name: "an index an expression folds to",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/2 + 2]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 4",
		},
		{
			name: "an index past an array of double",
			src: `double a[3];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/3]);
}
`,
			want: "an array index must be between 0 and 2, the last index of 'a', found 3",
		},
		{
			name: "a read through the address one past the end",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *&a[/*!*/4]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 4",
		},
		{
			name: "a read through the parenthesized address one past the end",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(&a[/*!*/4]));
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 4",
		},
		{
			name: "a write through the address one past the end",
			src: `long long a[4];
void main(void) {
    *&a[/*!*/4] = 7;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 4",
		},
		{
			name: "a compound assignment through the address one past the end",
			src: `long long a[4];
void main(void) {
    *&a[/*!*/4] += 7;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'a', found 4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestTheOnePastTheEndCarveOutCoversTheAddressAlone holds the one index a
// subscript takes beyond the array, and what taking it costs nowhere else.
//
// '&a[n]' for an n-element array is the one-past-the-end pointer C defines. It
// compares against a walking pointer and designates no slot, which is why it
// stands where the read 'a[n]' does not and why refusing it while admitting the
// identical 'a + n' would split two spellings of one thing. Reading or writing
// through it is not defined, and '*&a[n]' is that read with the address written
// in the middle: the chip answers a get outside the array with the unknown
// error and a put with a stack overflow, once per tick for as long as the
// program runs.
func TestTheOnePastTheEndCarveOutCoversTheAddressAlone(t *testing.T) {
	rejected := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "an address two past the end",
			src: `long long a[4];
void main(void) {
    long long *p = &a[/*!*/5];
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an index under '&' must be between 0 and 4, one past the last index of 'a', found 5",
		},
		{
			name: "an address far past the end",
			src: `long long a[4];
void main(void) {
    long long *p = &a[/*!*/9000];
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an index under '&' must be between 0 and 4, one past the last index of 'a', found 9000",
		},
		{
			name: "an address before the array",
			src: `long long a[4];
void main(void) {
    long long *p = &a[/*!*/-1];
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an index under '&' must be between 0 and 4, one past the last index of 'a', found -1",
		},
		{
			name: "an address one past an array of double",
			src: `double a[3];
void main(void) {
    double *p = &a[/*!*/4];
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an index under '&' must be between 0 and 3, one past the last index of 'a', found 4",
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}

	accepted := []struct {
		name string
		src  string
	}{
		{
			name: "the address one past the end",
			src: `long long a[4];
void main(void) {
    long long *p = &a[4];
    __ic_store(d0, Setting, p == a);
}
`,
		},
		{
			name: "the address of the last element",
			src: `long long a[4];
void main(void) {
    long long *p = &a[3];
    __ic_store(d0, Setting, *p);
}
`,
		},
		{
			name: "the index one before the end, computed",
			src: `long long a[4];
void main(void) {
    long long *p = &a[4 - 1];
    __ic_store(d0, Setting, *p);
}
`,
		},
		{
			name: "the same address reached by pointer arithmetic",
			src: `long long a[4];
void main(void) {
    long long *p = a + 4;
    __ic_store(d0, Setting, p == a);
}
`,
		},
		{
			// '&*E' designates E without reading it, so the address survives
			// the '*' that stands under the outer '&'.
			name: "the address one past the end taken back through a dereference",
			src: `long long a[4];
void main(void) {
    long long *p = &*&a[4];
    __ic_store(d0, Setting, p == a);
}
`,
		},
		{
			name: "a read through an address inside the array",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *&a[3]);
}
`,
		},
		{
			name: "a write through an address inside the array",
			src: `long long a[4];
void main(void) {
    *&a[3] = 7;
    __ic_store(d0, Setting, a[0]);
}
`,
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestSubscriptsTheBoundDoesNotDecideStandAsWritten holds the other edge of the
// rule above, which is the whole of what makes it safe to apply.
//
// The bound settles one question: an index the source fixes into an array whose
// length is in the type. An index the program computes carries no such number,
// and a pointer object carries neither a length nor an offset — one walked past
// the array it started in through a variable is left to run time. What the
// expression itself steps is settled, and
// [TestAConstantStepIsHeldToTheObjectItStartsIn] is where that lives.
func TestSubscriptsTheBoundDoesNotDecideStandAsWritten(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "the last index the array has",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, a[3]);
}
`,
		},
		{
			name: "an index the program computes",
			src: `long long a[4];
long long g;
void main(void) {
    g = 9000;
    __ic_store(d0, Setting, a[g]);
}
`,
		},
		{
			name: "a pointer walked past the array it started in",
			src: `long long a[4];
void main(void) {
    long long *p = a;
    __ic_store(d0, Setting, p[9000]);
}
`,
		},
		{
			name: "the address one past the end, which C defines",
			src: `long long a[4];
void main(void) {
    long long *p = a;
    while (p != &a[4]) {
        *p = 0;
        p = p + 1;
    }
    __ic_store(d0, Setting, a[0]);
}
`,
		},
		{
			name: "a subscript on a parameter, which carries no length",
			src: `void fill(long long *p) {
    p[9000] = 1;
}
void main(void) {
    long long a[4];
    fill(a);
    __ic_store(d0, Setting, a[0]);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}
