package dimmer

import (
	"math"
	"testing"
	"time"
)

const ramp = 9900 * time.Millisecond // 100 ms a level

func run(m *Model, moves []Move) {
	for _, mv := range moves {
		m.Apply(mv.Hold)
	}
}

func near(t *testing.T, m *Model, want int) {
	t.Helper()

	if got := m.Level(); math.Abs(float64(got-want)) > 1 {
		t.Errorf("level %d, want %d", got, want)
	}
}

func TestNothingCanBePlannedUntilItHasBeenCalibrated(t *testing.T) {
	m := New(ramp)

	if m.Known() || m.Level() != 0 || m.Plan(50) != nil || m.PlanStep(10) != nil {
		t.Error("planned from nowhere")
	}

	m.Apply(time.Second)

	if m.Known() {
		t.Error("a hold from nowhere found it")
	}
}

func TestPlan_FromTheDimmestItGoesUpInOneHold(t *testing.T) {
	m := New(ramp)
	m.Calibrate(1, true)

	moves := m.Plan(51)
	if len(moves) != 1 || !moves[0].Up || moves[0].Hold != 5*time.Second {
		t.Fatalf("%+v", moves)
	}

	run(m, moves)
	near(t, m, 51)

	// It alternates: the next hold goes down.
	if m.NextUp() {
		t.Error("the next hold goes the same way")
	}
}

func TestPlan_TheOtherWayIsReachedByATapThatTurnsTheNextHoldRound(t *testing.T) {
	m := New(ramp)
	m.Calibrate(51, true) // the next hold goes up, and the goal is below

	moves := m.Plan(41)
	if len(moves) != 2 || !moves[0].Up || moves[0].Hold != minHold || moves[1].Up {
		t.Fatalf("%+v", moves)
	}

	run(m, moves)
	near(t, m, 41)
}

func TestPlan_WhenAHoldDoesntAlternateTheOtherWayIsToTheEndAndBack(t *testing.T) {
	m := New(ramp)
	m.Alternates = false
	m.Calibrate(95, true)

	moves := m.Plan(90)
	if len(moves) != 1 || !moves[0].Up {
		t.Fatalf("%+v", moves)
	}

	run(m, moves)
	near(t, m, 90)
}

func TestPlan_APathThatTurnsAtTheEnd(t *testing.T) {
	m := New(ramp)
	m.Calibrate(80, true)

	// Up to 100, back down to 70: 20 + 30 levels.
	moves := m.Plan(70)
	run(m, moves)
	near(t, m, 70)
}

func TestPlan_WithoutAlternatingAHoldCarriesOnTheSameWay(t *testing.T) {
	m := New(ramp)
	m.Alternates = false
	m.Calibrate(1, true)

	run(m, m.Plan(50))

	if !m.NextUp() {
		t.Error("it turned round")
	}

	near(t, m, 50)
}

func TestPlan_ADistanceTooSmallToTimeIsNoMove(t *testing.T) {
	m := New(ramp)
	m.Calibrate(50, true)

	if moves := m.Plan(50); len(moves) != 0 {
		t.Errorf("%+v", moves)
	}
}

func TestPlanStep_IsFromWhereItIsAndClampsAtTheEnds(t *testing.T) {
	m := New(ramp)
	m.Calibrate(30, true)

	run(m, m.PlanStep(20))
	near(t, m, 50)

	run(m, m.PlanStep(-10))
	near(t, m, 40)

	run(m, m.PlanStep(500)) // clamped to the brightest
	near(t, m, 100)
}

func TestRepeatedPlansStayOnTarget(t *testing.T) {
	m := New(ramp)
	m.Calibrate(1, true)

	for _, target := range []int{75, 20, 90, 15, 15, 60, 100, 1, 33} {
		run(m, m.Plan(target))

		if got := m.Level(); math.Abs(float64(got-target)) > 1 {
			t.Fatalf("target %d, level %d", target, got)
		}
	}
}

func TestForgetDropsTheEstimate(t *testing.T) {
	m := New(ramp)
	m.Calibrate(50, true)
	m.Forget()

	if m.Known() || m.Plan(10) != nil {
		t.Error("still known")
	}
}
