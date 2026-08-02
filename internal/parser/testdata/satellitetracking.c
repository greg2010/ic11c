// A satellite dish hunting for a signal, written by a player and adopted
// unchanged in shape: the horizontal sweep runs until the dish sees something,
// and a nested loop then walks the elevation up or down depending on which way
// the last step moved the signal strength. A large dish follows whatever aim
// the medium one settled on.
//
// The master switch is both the outer loop's condition and the program's stop
// button, so this is the one program in the corpus whose main can return.

const dev dishMed = d0;
const dev dishLg = d1;
const dev sw = d2;
const dev light = d4;

void main(void) {
    double hasSignal;
    double sigVert;
    double sigVertPrev = 0;
    double hori;
    double vert;
    double vertPrev = 0;

    while (__ic_load(sw, On) == 1) {
        hori = __ic_load(dishMed, Horizontal);
        __ic_store(dishMed, Power, 7500);
        __ic_sleep(4);
        hasSignal = __ic_load(dishMed, SignalStrength);

        if (hasSignal > 20 || hasSignal == -1) {
            hori += 5;
            __ic_store(dishMed, Horizontal, hori);
        }
        if (hasSignal <= 20) {
            sigVert = __ic_load(dishMed, SignalStrength);
            sigVertPrev = __ic_load(dishMed, SignalStrength);
            vert = __ic_load(dishMed, Vertical);
            vertPrev = __ic_load(dishMed, Vertical) + 5;
            __ic_store(dishLg, Horizontal, hori);
        }
        while (hasSignal < 20 && hasSignal != -1 && __ic_load(sw, On) == 1) {
            vert = __ic_load(dishMed, Vertical);
            sigVert = __ic_load(dishMed, SignalStrength);
            __ic_sleep(4);
            if (sigVert > sigVertPrev && vert > vertPrev) {
                vert -= 5;
                __ic_store(dishMed, Vertical, vert);
                sigVertPrev = sigVert;
            }
            if (sigVert < sigVertPrev && vert > vertPrev) {
                vert += 5;
                __ic_store(dishMed, Vertical, vert);
                sigVertPrev = sigVert;
            }
            if (sigVert > sigVertPrev && vert < vertPrev) {
                vert += 5;
                __ic_store(dishMed, Vertical, vert);
                sigVertPrev = sigVert;
            }
            if (sigVert < sigVertPrev && vert < vertPrev) {
                vert -= 5;
                __ic_store(dishMed, Vertical, vert);
                sigVertPrev = sigVert;
            }
            if (sigVert == sigVertPrev && sigVert < 10) {
                __ic_store(sw, On, 0);
                __ic_store(light, On, 1);
                __ic_store(dishLg, Vertical, vert);
            }
        }
    }
}
