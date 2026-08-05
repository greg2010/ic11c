// Two solar panel banks aimed from one daylight sensor. Both banks are driven
// through the same helper, which takes the pin as a dev parameter, so the aim
// is written once and spliced into each call.
//
// The vertical aim is eased toward the reading rather than snapped to it, so a
// noisy sensor does not make the panels chatter, and a reading the sensor could
// not take is dropped rather than eased toward.

const dev sensor = d0;
const dev eastBank = d1;
const dev westBank = d2;

constexpr double kEase = 0.35;
constexpr double kMaxElevation = 90.0;

double heldVertical;

void aim(dev bank, double horizontal, double vertical) {
    __ic_store(bank, Horizontal, horizontal);
    __ic_store(bank, Vertical, __ic_clamp(vertical, 0.0, kMaxElevation));
}

void main(void) {
    while (true) {
        double horizontal = __ic_load(sensor, SolarAngle);
        double vertical = __ic_load(sensor, Vertical);

        if (!__ic_isnan(vertical)) {
            heldVertical = __ic_lerp(heldVertical, vertical, kEase);
        }

        aim(eastBank, horizontal, heldVertical);
        aim(westBank, horizontal + 180.0, heldVertical);
        __ic_yield();
    }
}
