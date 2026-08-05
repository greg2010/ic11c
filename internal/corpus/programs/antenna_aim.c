/*
 * Solar panels aimed from a daylight sensor, with the incidence angle carried
 * through as a direction vector rather than as two independent angles.
 *
 * Every device this touches is one the game has and every access is one the
 * game admits: a daylight sensor answers SolarAngle and Vertical, a solar panel
 * takes Horizontal and Vertical, and the derived figures go to a console and a
 * logic memory. Pasted into a housing wired that way it aims panels.
 *
 * The mathematics, though, is this program's own. No script in the surveyed
 * corpus calls a trigonometric intrinsic, because the sensor reports the two
 * angles a panel wants and nothing has to derive them -- the corpus solar
 * tracker simply copies them across. Going through direction cosines to arrive
 * back at the same aim is a longer road to the same place, taken here because
 * the intrinsics are declared and have to lower. Read it as an exercise of the
 * machine's mathematics, not as advice on tracking the sun.
 *
 * The chip's own deg2rad and rad2deg are used rather than a folded pi/180: they
 * are float literals widened to double, so a constant computed at double
 * precision would be a different number from the one the machine holds.
 */

const dev sensor = d0;
const dev display = d1;
const dev memory = d2;
const dev probe = d3;

constexpr double kMinCosine = 0.05;
constexpr double kHazeDepth = 0.0001;
constexpr double kPanelExponent = 1.2;
constexpr double kDecibelScale = 10.0;
constexpr long kModePercent = 1;
constexpr long kModeDefault = 0;

double heldBearing;

void main(void) {
    long panels = __ic_hash("StructureSolarPanel");

    while (true) {
        double azimuth = __ic_load(sensor, SolarAngle) * deg2rad;
        double elevation = __ic_load(sensor, Vertical) * deg2rad;

        // Direction cosines of the sun, which is the vector the rest derives
        // from rather than the two angles it came in as.
        double east = __ic_cos(elevation) * __ic_sin(azimuth);
        double north = __ic_cos(elevation) * __ic_cos(azimuth);
        double up = __ic_sin(elevation);

        double norm = __ic_sqrt(east * east + north * north + up * up);
        double bearing = __ic_atan2(east, north) * rad2deg;
        double tilt = __ic_acos(up / norm) * rad2deg;
        double climb = __ic_asin(up / norm) * rad2deg;
        double skew = __ic_atan(east / north) * rad2deg;
        double slope = __ic_tan(elevation);

        // Air mass grows as the sun drops, and the panel's own response to a
        // glancing angle is not linear in the cosine.
        double airMass = 1.0 / __ic_max(up, kMinCosine);
        double haze = __ic_exp(-kHazeDepth * airMass);
        double gain = __ic_pow(__ic_max(up, 0.0), kPanelExponent);
        double decibels = kDecibelScale * __ic_log(gain + 1.0);

        heldBearing = __ic_lerp(heldBearing, bearing, 0.25);

        __ic_store_batch(panels, Horizontal, heldBearing);
        __ic_store_batch(panels, Vertical, __ic_clamp(90.0 - tilt, 0.0, 90.0));

        __ic_store(display, Setting, haze * gain);
        __ic_store(display, Mode, kModePercent);
        __ic_store(memory, Setting, decibels);
        __ic_store(probe, Setting, climb + skew + slope + norm);
        __ic_store(probe, Mode, kModeDefault);
        __ic_yield();
    }
}
