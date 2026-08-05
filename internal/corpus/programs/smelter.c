// A furnace bank. The slot intrinsics read what the one wired furnace holds,
// and the batch forms aggregate the same properties across every furnace on the
// network. A batch that matches nothing answers NaN under Average, so the
// temperature is tested before anything is published from it.
//
// A furnace slot answers a dozen properties and takes a write of none, so what
// the program writes back it writes to the device rather than to a slot. The
// pin says which furnace it is wired to, which is what lets the compiler hold
// the slot read and the Lock write to the same roster the batch forms below are
// held to.
//
// Two spellings of a slot property appear below and the difference is not
// arbitrary. Every machine name lives in one namespace, as C requires, so where
// two families carry the same name the first one to claim it keeps the bare
// spelling and the later one takes a prefix. Quantity is a logic type, so the
// slot property is SlotType_Quantity; Occupied is carried by no other family,
// so it is spelled bare and SlotType_Occupied names nothing at all. The
// diagnostic names the spelling a position wants when the wrong one is written.

[[ic11c::prefab("StructureFurnace")]] const dev furnace = d0;
const dev display = d1;

constexpr long kInputSlot = 0;
constexpr long kOutputSlot = 1;

long stock;

void main(void) {
    long furnaces = __ic_hash("StructureFurnace");
    long north = __ic_hash("north");

    while (true) {
        double onHand = __ic_load_slot(furnace, kInputSlot, SlotType_Quantity);
        double bankLow = __ic_load_batch_slot(furnaces, kInputSlot, SlotType_Quantity, BatchMode_Minimum);
        double heat = __ic_load_batch_named(furnaces, north, Temperature, Average);
        double loaded = __ic_load_batch_named_slot(furnaces, north, kOutputSlot, Occupied, Sum);

        __ic_store(furnace, Lock, loaded > 0.0);
        __ic_store_batch(furnaces, Activate, onHand > 0.0);

        stock = (long)__ic_min(onHand, bankLow);
        __ic_store(display, Setting, __ic_isnan(heat) ? 0 : stock);
        __ic_yield();
    }
}
