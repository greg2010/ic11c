package regalloc

import (
	"fmt"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// held is an interval that currently owns a register.
type held struct {
	iv  *interval
	reg ic10.Register
}

// spillScore ranks an interval as a spill candidate: lower is a better victim.
//
// The budget that binds is 128 lines, so the quantity to minimise is emitted
// instructions. Each touch of a spilled value becomes one more of them, a poke
// or a get db, so touches is the cost of spilling. Span is what spilling buys,
// since that is how much register pressure it removes.
// Cost over benefit therefore prefers a long, cold interval to a short, hot
// one: an induction variable read on every line of a loop stays in a register
// while a value computed once and read once at the far end of the function
// goes to memory.
func spillScore(iv *interval) float64 {
	return float64(iv.touches) / float64(iv.span())
}

// scan is the linear pass: intervals are considered in order of their first
// live point and each is given a register free for its whole lifetime or sent
// to memory. There is no interval splitting. Splitting trades a spill for a
// move, which is one instruction against two, but it also multiplies the
// bookkeeping, and on a program that fits in three hundred instructions the
// register file runs out rarely enough that the simpler policy is the one worth
// having.
func scan(intervals []*interval, constrained map[mir.VirtReg]bool, allocatable []ic10.Register, cfg Config) (map[mir.VirtReg]ic10.Register, []*interval, error) {
	assigned := make(map[mir.VirtReg]ic10.Register)
	var spilled []*interval
	var live []held

	for _, cur := range intervals {
		point := cur.start()
		live = slices.DeleteFunc(live, func(h held) bool { return h.iv.end() <= point })

		candidates := allocatable
		if constrained[cur.vreg] {
			// A register holding a device reference resolves within r0 through
			// r15 only, so sp and ra are out even when nothing reserves them.
			candidates = slices.DeleteFunc(slices.Clone(allocatable), func(r ic10.Register) bool {
				return r >= ic10.NumGeneralRegisters
			})
		}

		occupied := make(map[ic10.Register][]*interval, len(live))
		for _, h := range live {
			if h.iv.intersects(cur) {
				occupied[h.reg] = append(occupied[h.reg], h.iv)
			}
		}

		if reg, ok := firstFree(candidates, occupied); ok {
			assigned[cur.vreg] = reg
			live = append(live, held{iv: cur, reg: reg})
			continue
		}

		if len(cfg.Scratch) == 0 {
			return nil, nil, fmt.Errorf("no register is free for %s and no scratch register is configured to spill it into", cur.vreg)
		}

		// Weighing one interval against the sum for a register is not an exact
		// instruction count, but it keeps the choice on the right side: taking a
		// register two live values share has to be worth spilling both.
		victimReg, victimScore, ok := cheapestVictim(candidates, occupied)
		if !ok || spillScore(cur) <= victimScore {
			spilled = append(spilled, cur)
			continue
		}
		for _, victim := range occupied[victimReg] {
			delete(assigned, victim.vreg)
			spilled = append(spilled, victim)
			live = slices.DeleteFunc(live, func(h held) bool { return h.iv == victim })
		}
		assigned[cur.vreg] = victimReg
		live = append(live, held{iv: cur, reg: victimReg})
	}

	slices.SortFunc(spilled, byStart)
	return assigned, spilled, nil
}

// firstFree returns the earliest candidate no live interval intersecting cur
// holds. Candidates arrive ordered by the bytes their name costs to render.
func firstFree(candidates []ic10.Register, occupied map[ic10.Register][]*interval) (ic10.Register, bool) {
	for _, reg := range candidates {
		if len(occupied[reg]) == 0 {
			return reg, true
		}
	}
	return 0, false
}

// cheapestVictim picks the register whose occupants are together the cheapest
// to move to memory. Freeing a register means spilling every interval on it
// that overlaps the one being placed, so the scores add.
func cheapestVictim(candidates []ic10.Register, occupied map[ic10.Register][]*interval) (ic10.Register, float64, bool) {
	var best ic10.Register
	var bestScore float64
	found := false
	for _, reg := range candidates {
		ivs := occupied[reg]
		if len(ivs) == 0 {
			continue
		}
		score := 0.0
		for _, iv := range ivs {
			score += spillScore(iv)
		}
		if !found || score < bestScore {
			best, bestScore, found = reg, score, true
		}
	}
	return best, bestScore, found
}

// assignSlots hands every spilled interval a data region slot, reusing a slot
// across intervals that are never live at the same point. Reuse is what keeps
// the boundary between the data region and the call frames low, and the two
// share one unprotected array.
func assignSlots(spilled []*interval, base int) (map[mir.VirtReg]int, int, error) {
	type occupant struct {
		iv   *interval
		slot int
	}
	slots := make(map[mir.VirtReg]int, len(spilled))
	var live []occupant
	count := 0

	for _, iv := range spilled {
		point := iv.start()
		live = slices.DeleteFunc(live, func(o occupant) bool { return o.iv.end() <= point })
		taken := make(map[int]bool, len(live))
		for _, o := range live {
			if o.iv.intersects(iv) {
				taken[o.slot] = true
			}
		}
		slot := 0
		for taken[slot] {
			slot++
		}
		count = max(count, slot+1)
		slots[iv.vreg] = base + slot
		live = append(live, occupant{iv: iv, slot: slot})
	}

	if base+count > ic10.NumMemorySlots {
		return nil, 0, fmt.Errorf("spill slots reach %d, past the %d slot memory array", base+count, ic10.NumMemorySlots)
	}
	return slots, count, nil
}
