/*
 * The calling convention, written as a function that reaches itself through its
 * own argument list.
 *
 * frames.c pairs two functions that recurse into each other and return nothing,
 * so what a frame there carries is the arguments of a call that has not come
 * back. This is the other half: one function, a value to return, and an inner
 * call standing in the argument list of an outer one, so an activation's own
 * operand is produced by a call made while that activation is live and has to
 * survive it. The pair of calls below repeats that at the top level, where the
 * argument being computed by a call is the only live value rather than one of
 * several.
 *
 * The function is Ackermann's, which is why the recursion cannot be rewritten
 * as a loop and why the depth follows the answer rather than the input. The
 * order is held at two, where the answer is 2n + 3 and the frames stay well
 * inside what the data region leaves them; nothing in the source bounds the
 * depth, so the layout report is where the room left for it is stated.
 *
 * Nothing here reads a device. A frame is only pushed on top of another one
 * where the depth moves, and a pin answering the same reading every tick would
 * leave the ladder standing on one rung.
 */

const dev display = d0;
const dev report = d1;

constexpr long kModeDefault = 0;

constexpr long kOrder = 2;
constexpr long kRungs = 5;

long rung;

long ladder(long order, long step) {
    if (order == 0) {
        return step + 1;
    }
    if (step == 0) {
        return ladder(order - 1, 1);
    }
    return ladder(order - 1, ladder(order, step - 1));
}

void main(void) {
    while (true) {
        long height = ladder(kOrder, rung);
        long lifted = ladder(kOrder - 1, height);

        __ic_store(display, Setting, height);
        __ic_store(display, Mode, kModeDefault);
        __ic_store(report, Setting, lifted);

        rung = (rung + 1) % kRungs;
        __ic_yield();
    }
}
