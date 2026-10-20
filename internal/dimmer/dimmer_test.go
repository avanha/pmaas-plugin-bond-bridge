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

func TestModel_EveryHoldIsLongerByTheDelayBecauseTheLightDoesNotMoveDuringIt(t *testing.T) {
	m := New(6 * time.Second)
	m.Delay = time.Second
	m.Calibrate(100, false)

	moves := m.Plan(50) // 50 of the 99 levels, a little over 3s of movement
	if len(moves) != 1 || moves[0].Hold < 4*time.Second || moves[0].Hold > 4100*time.Millisecond {
		t.Fatalf("%+v", moves)
	}

	m.Apply(moves[0].Hold)

	if got := m.Level(); got != 50 {
		t.Errorf("level %d", got)
	}

	// A hold no longer than the delay doesn't move it: the fan takes it for a button press.
	before := m.Level()
	m.Apply(time.Second)

	if m.Level() != before {
		t.Errorf("a hold of the delay moved the level from %d to %d", before, m.Level())
	}
}

func TestModel_NoHoldIsShorterThanTheDelay(t *testing.T) {
	m := New(6 * time.Second)
	m.Delay = time.Second
	m.Calibrate(100, true) // the next hold goes up, so 90 is the wrong way

	for _, target := range []int{99, 95, 90, 60, 1} {
		for _, move := range m.Plan(target) {
			if move.Hold <= m.Delay {
				t.Errorf("target %d: a hold of %v would switch the light", target, move.Hold)
			}
		}
	}
}

func TestModel_ASmallChangeIsRoundedToNothingOrToTheShortestHold(t *testing.T) {
	m := New(6 * time.Second)
	m.Delay = 800 * time.Millisecond
	m.MinHold = 1400 * time.Millisecond // moves for 600ms: about 10 levels
	m.Alternates = false
	m.Calibrate(50, true)

	if moves := m.Plan(53); len(moves) != 0 {
		t.Errorf("3 levels is under half a step, but %+v", moves)
	}

	moves := m.Plan(58) // 8 levels: nearer a step than nothing
	if len(moves) != 1 || moves[0].Hold != 1400*time.Millisecond {
		t.Errorf("%+v", moves)
	}

	for _, target := range []int{51, 52, 55, 60, 70, 99} {
		for _, move := range m.Plan(target) {
			if move.Hold < m.MinHold {
				t.Errorf("target %d: a hold of %v is shorter than the minimum", target, move.Hold)
			}
		}
	}
}

func TestPlanHold_IsTheTimeItMovesAndTheDelayBeforeItDoesWhereverTheLevelIs(t *testing.T) {
	m := New(ramp)
	m.Delay = 800 * time.Millisecond
	m.MinHold = 1400 * time.Millisecond

	// No level is known, and it still plans: a person judges how far it went.
	moves := m.PlanHold(2 * time.Second)

	if len(moves) != 1 || moves[0].Hold != 2800*time.Millisecond {
		t.Fatalf("%+v", moves)
	}

	if len(m.PlanHold(0)) != 0 {
		t.Error("a hold for no time")
	}

	// Never shorter than a hold the fan takes for a press of the button.
	if moves := m.PlanHold(200 * time.Millisecond); moves[0].Hold != 1400*time.Millisecond {
		t.Errorf("%+v", moves)
	}
}

func TestPlanHold_GoesTheWayTheNextHoldGoesAndAppliesToTheLevel(t *testing.T) {
	m := New(ramp)
	m.Delay = 800 * time.Millisecond
	m.Alternates = false
	m.Calibrate(50, true)

	moves := m.PlanHold(6 * time.Second)

	if !moves[0].Up {
		t.Fatal("the next hold goes up")
	}

	run(m, moves)
	near(t, m, 90) // 6 s is 60 levels at 100 ms a level: 50 to the top, and 10 back down

	// It turned at the top, so it is now going down.
	if m.NextUp() {
		t.Error("should be going down")
	}

	m.SetNextUp(true)

	if !m.NextUp() || m.Level() != 90 {
		t.Errorf("direction %v level %d", m.NextUp(), m.Level())
	}
}
