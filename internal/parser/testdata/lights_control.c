// Habitat lighting slaved to a bank of solar diodes, written by a player and
// adopted unchanged in shape. The diode sum is the daylight reading: when it
// falls the lamps come on, and when it rises they go off again.
//
// Every device it touches is reached by prefab hash rather than through a pin,
// and it keeps no array and no global, so it is the one program in the corpus
// that uses no data region at all.

void main(void) {
    double diodeSumPrev;
    double diodeSum;

    __ic_store_batch(__ic_hash("StructureDiodeSlide"), On, 0);
    __ic_store_batch(__ic_hash("StructureLightRound"), On, 1);
    __ic_store_batch(__ic_hash("StructureLightLongWide"), On, 1);
    __ic_store_batch(__ic_hash("StructureLightLongAngled"), On, 1);
    diodeSumPrev = __ic_load_batch(__ic_hash("StructureDiodeSlide"), On, Sum);
    __ic_yield();

    while (1) {
        diodeSum = __ic_load_batch(__ic_hash("StructureDiodeSlide"), On, Sum);

        if (diodeSum < diodeSumPrev) {
            __ic_store_batch(__ic_hash("StructureDiodeSlide"), On, 0);
            __ic_store_batch(__ic_hash("StructureLightRound"), On, 1);
            __ic_store_batch(__ic_hash("StructureLightLongWide"), On, 1);
            __ic_store_batch(__ic_hash("StructureLightLongAngled"), On, 1);
            diodeSumPrev = diodeSum;
        }
        if (diodeSum > diodeSumPrev) {
            __ic_store_batch(__ic_hash("StructureDiodeSlide"), On, 1);
            __ic_store_batch(__ic_hash("StructureLightRound"), On, 0);
            __ic_store_batch(__ic_hash("StructureLightLongWide"), On, 0);
            __ic_store_batch(__ic_hash("StructureLightLongAngled"), On, 0);
            diodeSumPrev = diodeSum;
        }

        __ic_yield();
    }
}
