// A furnace bank. The slot intrinsics read what the one wired furnace holds,
// and the batch forms aggregate the same properties across every furnace on the
// network. A batch that matches nothing answers NaN under Average, so the
// temperature is tested before anything is published from it.

const dev furnace = d0;
const dev display = d1;

constexpr long long kInputSlot = 0;
constexpr long long kOutputSlot = 1;

long long stock;

void main(void) {
    long long furnaces = __ic_hash("StructureFurnace");
    long long north = __ic_hash("north");

    while (true) {
        double onHand = __ic_load_slot(furnace, kInputSlot, Quantity);
        double bankLow = __ic_load_batch_slot(furnaces, kInputSlot, Quantity, Minimum);
        double heat = __ic_load_batch_named(furnaces, north, Temperature, Average);
        double loaded = __ic_load_batch_named_slot(furnaces, north, kOutputSlot, Occupied, Sum);

        __ic_store_slot(furnace, kOutputSlot, Lock, loaded > 0.0);
        __ic_store_batch_slot(furnaces, kInputSlot, Lock, 0);

        stock = (long long)__ic_min(onHand, bankLow);
        __ic_store(display, Setting, __ic_isnan(heat) ? 0 : stock);
        __ic_yield();
    }
}
