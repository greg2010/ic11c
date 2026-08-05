// A bay of suit racks driven as one device. Every rack on the network answers
// the batch forms together, so the bay is locked, lit and opened one
// instruction at a time rather than by walking the pins, and a rack built into
// the bay later joins without the chip being rewired.
//
// The helmet slot is the one a rack takes a write of, and a batch slot write
// reaches that slot on every rack at once. What the bay reports is read the
// same way. The census is a batch count, which answers how many devices matched
// rather than an aggregate over what they hold, so it is the one reading here
// that says nothing about the suits. The worst helmet is a batch maximum, which
// answers negative infinity where the network holds no rack at all, so it is
// bounded before the display is driven from it.
//
// Lock, On and Open are logic types before they are slot properties, so the
// slot position spells them SlotType_Lock, SlotType_On and SlotType_Open;
// Damage belongs to no other family and stays bare. smelter.c says why.

const dev hazard = d0;
const dev display = d1;

constexpr long kHelmet = 0;
constexpr double kServiceDamage = 0.25;

long served;

void main(void) {
    long racks = __ic_hash("StructureSuitStorage");

    while (true) {
        bool alarm = __ic_load(hazard, Activate) != 0;

        // Locked while the bay is quiet, so a suit cannot leave the rotation,
        // and released with the lamp lit the moment the hazard line trips.
        __ic_store_batch_slot(racks, kHelmet, SlotType_Lock, !alarm);
        __ic_store_batch_slot(racks, kHelmet, SlotType_On, alarm);

        double worst = __ic_load_batch_slot(racks, kHelmet, Damage, BatchMode_Maximum);
        if (worst > kServiceDamage) {
            served++;
            __ic_store_batch_slot(racks, kHelmet, SlotType_Open, 1);
        }

        __ic_store(display, Setting, __ic_max(worst, 0.0));
        __ic_store(display, Mode, __ic_load_batch(racks, Setting, Count));
        __ic_store(display, Quantity, served);
        __ic_yield();
    }
}
