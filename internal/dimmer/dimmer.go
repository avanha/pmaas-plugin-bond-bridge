// Package dimmer works out how to set a brightness on a light that can't be told one. A ceiling fan's remote dims by holding a
// button, and the Bond Bridge copies that: start the dimmer, and stop it. The direction can't be chosen. The light sweeps up
// and down while it is held, turning round at the ends, and a new hold is assumed to start the other way from where the last
// left off (see Model.Alternates). So a level is reached by timing a hold, from a position the model has to be told once
// (Calibrate) and then follows. It drifts with every hold, and so has to be calibrated again now and then.
package dimmer

import (
	"math"
	"time"
)

// Min and Max are the dimmest and brightest levels.
const (
	Min = 1
	Max = 100
)

// span is how many levels the dimmer travels from end to end.
const span = Max - Min

// minHold is the shortest hold worth making: shorter is lost in the lag of the network and the radio.
const minHold = 80 * time.Millisecond

// Move is running the dimmer for a time, in the direction it goes (Up is only for the log: it can't be chosen).
type Move struct {
	Hold time.Duration
	Up   bool
}

// Model is the estimate of where a dimmer is and which way its next hold goes. Not safe for concurrent use.
type Model struct {
	ramp  time.Duration
	level float64
	up    bool // the next hold goes up
	known bool

	// Alternates is that each hold starts the other way from the one before. If it is false a hold carries on the same way. The
	// remotes that hold to dim mostly alternate.
	Alternates bool
}

// New is a model for a dimmer that takes ramp to travel from the dimmest to the brightest. The level is not known yet.
func New(ramp time.Duration) *Model {
	return &Model{ramp: ramp, Alternates: true}
}

// Known is whether there is an estimate. There is none until Calibrate.
func (m *Model) Known() bool {
	return m.known
}

// Level is the estimated level from Min to Max, or 0 if it is not known.
func (m *Model) Level() int {
	if !m.known {
		return 0
	}

	return int(math.Round(m.level))
}

// NextUp is whether the next hold is expected to go up.
func (m *Model) NextUp() bool {
	return m.up
}

// Calibrate says where the dimmer is and which way the next hold goes, such as when it has been seen at the dimmest, after which it
// can only go up.
func (m *Model) Calibrate(level int, nextUp bool) {
	m.level, m.up, m.known = float64(min(max(level, Min), Max)), nextUp, true
}

// Forget drops the estimate, such as when someone is known to have used the remote or a stop went missing.
func (m *Model) Forget() {
	m.known = false
}

// Plan is the holds that take the dimmer to the level (clamped to Min..Max), one or two. There are none if it is there already or
// the level is not known. Because the direction can't be chosen, a level the wrong way from the next hold is reached either by
// going on to the end and back, or by a very short hold first, which turns the next one round. The shorter is chosen.
func (m *Model) Plan(target int) []Move {
	if !m.known {
		return nil
	}

	goal := float64(min(max(target, Min), Max))
	if math.Abs(goal-m.level) < 1 {
		return nil
	}

	direct := m.path(m.level, m.up, goal)
	best := []Move{{Hold: m.hold(direct), Up: m.up}}
	bestCost := direct

	if m.Alternates {
		// A tap in the direction it goes, then the other way.
		tap := m.levels(minHold)
		afterTap, _ := advance(m.level, m.up, tap)
		cost := tap + m.path(afterTap, !m.up, goal)

		if cost < bestCost {
			best = []Move{{Hold: minHold, Up: m.up}, {Hold: m.hold(m.path(afterTap, !m.up, goal)), Up: !m.up}}
		}
	}

	var moves []Move

	for _, move := range best {
		if move.Hold >= minHold {
			moves = append(moves, move)
		}
	}

	return moves
}

// PlanStep is the holds that change the level by delta percent from where it is. There are none if the level is not known.
func (m *Model) PlanStep(delta int) []Move {
	return m.Plan(m.Level() + delta)
}

// Apply updates the estimate for a hold that has been made, and for how long it was in fact held.
func (m *Model) Apply(held time.Duration) {
	if !m.known {
		return
	}

	level, up := advance(m.level, m.up, m.levels(held))
	m.level = level

	if m.Alternates {
		m.up = !up
	} else {
		m.up = up
	}
}

// path is how many levels a hold from the level, going the way given, has to travel to be at the goal.
func (m *Model) path(level float64, up bool, goal float64) float64 {
	if up {
		if goal >= level {
			return goal - level
		}

		return (Max - level) + (Max - goal)
	}

	if goal <= level {
		return level - goal
	}

	return (level - Min) + (goal - Min)
}

// levels is how many levels the dimmer moves in the time.
func (m *Model) levels(held time.Duration) float64 {
	return float64(held) / float64(m.ramp) * span
}

// hold is the time to travel that many levels.
func (m *Model) hold(levels float64) time.Duration {
	return time.Duration(levels / span * float64(m.ramp))
}

// advance moves the level the distance in the direction, turning round at the ends. It returns where it ends and which way it was
// going.
func advance(level float64, up bool, distance float64) (float64, bool) {
	for distance > 0 {
		if up {
			room := Max - level
			if distance <= room {
				return level + distance, true
			}

			level, distance, up = Max, distance-room, false
		} else {
			room := level - Min
			if distance <= room {
				return level - distance, false
			}

			level, distance, up = Min, distance-room, true
		}
	}

	return level, up
}
