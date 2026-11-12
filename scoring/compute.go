package scoring

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"math"
)

// Results of a match from one alliance's point of view.
const (
	ResultWin  = "win"
	ResultLoss = "loss"
	ResultTie  = "tie"
)

// Options adjusts how a score is computed.
type Options struct {
	// Params overrides the game's parameter defaults for this event.
	Params map[string]float64
	// Playoff applies the game's playoff tiebreakers when the top scores are equal.
	Playoff bool
	// Teams gives the team number at each robot position, per alliance. It is optional and only feeds the
	// "team" property of robots in expressions.
	Teams map[string][]int
}

// Row is one line of the score breakdown.
type Row struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// AllianceResult is everything computed for one alliance.
type AllianceResult struct {
	Score        float64                       `json:"score"`
	Points       map[string]float64            `json:"points"`       // Per element and per derived value with points.
	PhasePoints  map[string]map[string]float64 `json:"phase_points"` // Per element, per phase.
	FoulPoints   float64                       `json:"foul_points"`  // Credited by other alliances' fouls.
	Values       map[string]any                `json:"values"`       // Alliance-scope derived values.
	Breakdown    []Row                         `json:"breakdown"`
	Result       string                        `json:"result"`
	RP           map[string]float64            `json:"rp"`
	RPTotal      float64                       `json:"rp_total"`
	Stats        map[string]float64            `json:"stats"`
	Disqualified bool                          `json:"disqualified"`
}

// Result is the computed outcome of a match.
type Result struct {
	Alliances map[string]*AllianceResult `json:"alliances"`
	Field     map[string]any             `json:"field"`    // Field-scope derived values.
	Winner    string                     `json:"winner"`   // Alliance id, or "" for a tie.
	Tiebreak  string                     `json:"tiebreak"` // Name of the playoff tiebreaker that decided it.

	env *environment
}

// environment is the nested map structure that expressions read. The alliance maps are shared between the "own",
// "opp", "alliances" and per-id roots, so a value written once is visible through all of them.
type environment struct {
	game      *gamedef.Game
	alliances map[string]map[string]any
	list      []any
	field     map[string]any
	params    map[string]any
}

func counterEnv(g *gamedef.Game, value *Value) map[string]any {
	env := make(map[string]any, len(g.Match.Phases)+1)
	for _, phase := range g.Match.Phases {
		env[phase.ID] = value.Counts[phase.ID]
	}
	env["total"] = value.Total()
	return env
}

func valueEnv(g *gamedef.Game, element *gamedef.Element, value *Value) any {
	switch element.Type {
	case gamedef.TypeCounter:
		return counterEnv(g, value)
	case gamedef.TypeBoolean:
		return value.Bool
	case gamedef.TypeEnum:
		return value.Option
	case gamedef.TypeTimer:
		return map[string]any{"seconds": value.Seconds, "running": value.Running}
	}
	return value.Number
}

func buildEnvironment(g *gamedef.Game, s *State, opts Options) *environment {
	env := &environment{
		game: g, alliances: make(map[string]map[string]any), field: make(map[string]any),
		params: make(map[string]any),
	}
	for _, param := range g.Params {
		value, _ := rules.ToNumber(param.Default)
		if override, ok := opts.Params[param.ID]; ok {
			value = override
		}
		if param.Type == "bool" {
			env.params[param.ID] = value != 0
		} else {
			env.params[param.ID] = value
		}
	}

	// Foul points each alliance receives from the others.
	received := make(map[string]float64)
	for _, alliance := range g.Alliances.List {
		for _, foul := range s.Alliances[alliance.ID].Fouls {
			foulType := g.FoulType(foul.Type)
			if foulType == nil || foulType.To != "opponents" {
				continue
			}
			for _, other := range g.Alliances.List {
				if other.ID != alliance.ID {
					received[other.ID] += foulType.Points
				}
			}
		}
	}

	for _, alliance := range g.Alliances.List {
		state := s.Alliances[alliance.ID]
		a := map[string]any{
			"id": alliance.ID, "name": alliance.Name, "foul_points": received[alliance.ID], "score": 0.0,
			"disqualified": state.Disqualified,
		}
		points := make(map[string]any)
		phasePoints := make(map[string]any)
		robots := make([]any, len(state.Robots))
		for i := range state.Robots {
			robot := map[string]any{"position": i + 1, "team": 0}
			if teams := opts.Teams[alliance.ID]; i < len(teams) {
				robot["team"] = teams[i]
			}
			robots[i] = robot
		}
		for _, element := range g.Elements {
			switch element.Scope {
			case gamedef.ScopeAlliance:
				a[element.ID] = valueEnv(g, element, state.Elements[element.ID])
			case gamedef.ScopeRobot:
				for i, robot := range state.Robots {
					robots[i].(map[string]any)[element.ID] = valueEnv(g, element, robot[element.ID])
				}
			}
			if element.Scope != gamedef.ScopeField {
				points[element.ID] = 0.0
				perPhase := make(map[string]any, len(g.Match.Phases))
				for _, phase := range g.Match.Phases {
					perPhase[phase.ID] = 0.0
				}
				phasePoints[element.ID] = perPhase
			}
		}
		for _, derived := range g.Derived {
			if derived.Points != nil {
				points[derived.ID] = 0.0
			} else if derived.Scope == gamedef.ScopeAlliance {
				a[derived.ID] = nil
			}
		}
		fouls := map[string]any{"count": len(state.Fouls)}
		for _, foulType := range g.Fouls.Types {
			count := 0
			for _, foul := range state.Fouls {
				if foul.Type == foulType.ID {
					count++
				}
			}
			fouls[foulType.ID] = map[string]any{"count": count, "points": float64(count) * foulType.Points}
		}
		cards := make([]any, 0, len(state.Cards))
		for position := 1; position <= len(state.Robots); position++ {
			if card := state.Cards[position]; card != "" {
				cards = append(cards, card)
			}
		}
		breakdown := make(map[string]any, len(g.Breakdown))
		for _, row := range g.Breakdown {
			breakdown[row.ID] = 0.0
		}
		rp := map[string]any{"total": 0.0}
		for _, rankingPoint := range g.Ranking.Points {
			rp[rankingPoint.ID] = 0.0
		}
		a["robots"], a["points"], a["phase_points"] = robots, points, phasePoints
		a["fouls"], a["cards"], a["breakdown"], a["rp"] = fouls, cards, breakdown, rp
		env.alliances[alliance.ID] = a
		env.list = append(env.list, a)
	}

	for _, element := range g.Elements {
		if element.Scope == gamedef.ScopeField {
			env.field[element.ID] = valueEnv(g, element, s.Field[element.ID])
		}
	}
	for _, derived := range g.Derived {
		if derived.Scope == gamedef.ScopeField {
			env.field[derived.ID] = nil
		}
	}
	return env
}

// roots returns the top-level names for an expression. own is the alliance being evaluated, or "" for a
// field-scope expression.
func (env *environment) roots(own string, extra map[string]any) map[string]any {
	roots := make(map[string]any, len(env.alliances)+6+len(extra))
	roots["alliances"], roots["field"], roots["params"] = env.list, env.field, env.params
	for id, alliance := range env.alliances {
		roots[id] = alliance
	}
	if own != "" {
		roots["own"] = env.alliances[own]
		opponents := make([]any, 0, len(env.list)-1)
		for _, alliance := range env.game.Alliances.List {
			if alliance.ID != own {
				opponents = append(opponents, env.alliances[alliance.ID])
			}
		}
		roots["opponents"] = opponents
		if len(opponents) == 1 {
			roots["opp"] = opponents[0]
		}
	}
	for name, value := range extra {
		roots[name] = value
	}
	return roots
}

func exprError(what string, e *gamedef.Expr, err error) error {
	return fmt.Errorf("%s (line %d of the game definition): %w", what, e.Line, err)
}

// elementPoints works out the points one value of an element earns, in total and per phase.
func (env *environment) elementPoints(
	element *gamedef.Element, value *Value, own string, robot map[string]any,
) (float64, map[string]float64, error) {
	g := env.game
	if element.Points != nil && element.Points.Expr != nil {
		extra := map[string]any{"value": valueEnv(g, element, value)}
		if robot != nil {
			extra["robot"] = robot
		}
		total, err := element.Points.Expr.Prog.RunNumber(env.roots(own, extra))
		if err != nil {
			return 0, nil, exprError("points of "+element.ID, element.Points.Expr, err)
		}
		return total, nil, nil
	}

	perPhase := make(map[string]float64)
	total := 0.0
	switch element.Type {
	case gamedef.TypeCounter:
		for phase, count := range value.Counts {
			if earned := float64(count) * element.Points.For(phase); earned != 0 {
				perPhase[phase] = earned
				total += earned
			}
		}
	case gamedef.TypeBoolean:
		if value.Bool {
			total = element.Points.For(value.Phase)
		}
	case gamedef.TypeEnum:
		if option := element.Option(value.Option); option != nil {
			total = option.Points.For(value.Phase)
		}
	case gamedef.TypeTimer:
		if element.Points != nil {
			total = value.Seconds * element.Points.Flat
		}
	case gamedef.TypeNumber:
		if element.Points != nil {
			total = value.Number * element.Points.Flat
		}
	}
	if (element.Type == gamedef.TypeBoolean || element.Type == gamedef.TypeEnum) && total != 0 && value.Phase != "" {
		perPhase[value.Phase] = total
	}
	return total, perPhase, nil
}

// Compute works out every alliance's points, score, breakdown, match result, ranking points and ranking stats
// from the raw state.
func Compute(g *gamedef.Game, s *State, opts Options) (*Result, error) {
	env := buildEnvironment(g, s, opts)
	result := &Result{Alliances: make(map[string]*AllianceResult), Field: make(map[string]any), env: env}
	ids := g.AllianceIDs()
	for _, id := range ids {
		result.Alliances[id] = &AllianceResult{
			Points: make(map[string]float64), PhasePoints: make(map[string]map[string]float64),
			Values: make(map[string]any), RP: make(map[string]float64), Stats: make(map[string]float64),
			FoulPoints: env.alliances[id]["foul_points"].(float64), Disqualified: s.Alliances[id].Disqualified,
		}
	}

	// Element points and derived values, in dependency order.
	derivedByID := make(map[string]*gamedef.Derived, len(g.Derived))
	for _, derived := range g.Derived {
		derivedByID[derived.ID] = derived
	}
	for _, node := range g.Order {
		if node.Kind == "points" {
			element := g.Element(node.ID)
			for _, id := range ids {
				state := s.Alliances[id]
				total := 0.0
				perPhase := make(map[string]float64)
				if element.Scope == gamedef.ScopeAlliance {
					earned, byPhase, err := env.elementPoints(element, state.Elements[element.ID], id, nil)
					if err != nil {
						return nil, err
					}
					total, perPhase = earned, byPhase
				} else {
					robots := env.alliances[id]["robots"].([]any)
					for i, robot := range state.Robots {
						earned, byPhase, err := env.elementPoints(
							element, robot[element.ID], id, robots[i].(map[string]any),
						)
						if err != nil {
							return nil, err
						}
						total += earned
						for phase, points := range byPhase {
							perPhase[phase] += points
						}
					}
				}
				result.Alliances[id].Points[element.ID] = total
				env.alliances[id]["points"].(map[string]any)[element.ID] = total
				if len(perPhase) > 0 {
					result.Alliances[id].PhasePoints[element.ID] = perPhase
					phaseEnv := env.alliances[id]["phase_points"].(map[string]any)[element.ID].(map[string]any)
					for phase, points := range perPhase {
						phaseEnv[phase] = points
					}
				}
			}
			continue
		}

		derived := derivedByID[node.ID]
		switch {
		case derived.Scope == gamedef.ScopeField:
			value, err := derived.Expr.Prog.Run(env.roots("", nil))
			if err != nil {
				return nil, exprError("derived value "+derived.ID, derived.Expr, err)
			}
			env.field[derived.ID] = value
			result.Field[derived.ID] = value
		case derived.Points != nil:
			earned := make(map[string]float64, len(ids))
			for _, id := range ids {
				points, err := derived.Points.Prog.RunNumber(env.roots(id, nil))
				if err != nil {
					return nil, exprError("points of "+derived.ID, derived.Points, err)
				}
				earned[id] = points
			}
			for _, id := range ids {
				result.Alliances[id].Points[derived.ID] = earned[id]
				env.alliances[id]["points"].(map[string]any)[derived.ID] = earned[id]
			}
		default:
			values := make(map[string]any, len(ids))
			for _, id := range ids {
				value, err := derived.Expr.Prog.Run(env.roots(id, nil))
				if err != nil {
					return nil, exprError("derived value "+derived.ID, derived.Expr, err)
				}
				values[id] = value
			}
			for _, id := range ids {
				result.Alliances[id].Values[derived.ID] = values[id]
				env.alliances[id][derived.ID] = values[id]
			}
		}
	}

	// Scores. A disqualified alliance scores nothing.
	for _, id := range ids {
		alliance := result.Alliances[id]
		if !alliance.Disqualified {
			alliance.Score = alliance.FoulPoints
			for _, points := range alliance.Points {
				alliance.Score += points
			}
			alliance.Score = roundTiny(alliance.Score)
		}
		env.alliances[id]["score"] = alliance.Score
	}

	// Breakdown rows. A row may read rows above it.
	for _, row := range g.Breakdown {
		for _, id := range ids {
			value, err := row.Expr.Prog.RunNumber(env.roots(id, nil))
			if err != nil {
				return nil, exprError("breakdown row "+row.ID, row.Expr, err)
			}
			value = roundTiny(value)
			result.Alliances[id].Breakdown = append(
				result.Alliances[id].Breakdown, Row{ID: row.ID, Name: row.Name, Value: value},
			)
			env.alliances[id]["breakdown"].(map[string]any)[row.ID] = value
		}
	}

	if err := result.decide(g, opts); err != nil {
		return nil, err
	}

	// Ranking points and stats.
	for _, id := range ids {
		alliance := result.Alliances[id]
		roots := env.roots(id, map[string]any{"result": alliance.Result})
		rpEnv := env.alliances[id]["rp"].(map[string]any)
		for _, rankingPoint := range g.Ranking.Points {
			value, err := rankingPoint.Expr.Prog.RunNumber(roots)
			if err != nil {
				return nil, exprError("ranking point "+rankingPoint.ID, rankingPoint.Expr, err)
			}
			alliance.RP[rankingPoint.ID] = value
			alliance.RPTotal += value
			rpEnv[rankingPoint.ID] = value
		}
		rpEnv["total"] = alliance.RPTotal
	}
	for _, id := range ids {
		alliance := result.Alliances[id]
		roots := env.roots(id, map[string]any{"result": alliance.Result})
		for _, stat := range g.Ranking.Stats {
			value, err := stat.Expr.Prog.RunNumber(roots)
			if err != nil {
				return nil, exprError("ranking stat "+stat.ID, stat.Expr, err)
			}
			alliance.Stats[stat.ID] = value
		}
	}
	return result, nil
}

// decide works out the winner and each alliance's result.
func (r *Result) decide(g *gamedef.Game, opts Options) error {
	ids := g.AllianceIDs()

	// Disqualified alliances cannot win. If everyone is disqualified, nobody wins.
	var contenders []string
	for _, id := range ids {
		if !r.Alliances[id].Disqualified {
			contenders = append(contenders, id)
		}
	}
	best := math.Inf(-1)
	for _, id := range contenders {
		best = math.Max(best, r.Alliances[id].Score)
	}
	var leaders []string
	for _, id := range contenders {
		if r.Alliances[id].Score == best {
			leaders = append(leaders, id)
		}
	}

	if len(leaders) > 1 && opts.Playoff {
		for _, tiebreaker := range g.Playoff.Tiebreakers {
			values := make(map[string]float64, len(leaders))
			top := math.Inf(-1)
			for _, id := range leaders {
				value, err := tiebreaker.Expr.Prog.RunNumber(r.env.roots(id, nil))
				if err != nil {
					return exprError("playoff tiebreaker "+tiebreaker.Name, tiebreaker.Expr, err)
				}
				values[id] = value
				top = math.Max(top, value)
			}
			var still []string
			for _, id := range leaders {
				if values[id] == top {
					still = append(still, id)
				}
			}
			if len(still) < len(leaders) {
				leaders = still
				r.Tiebreak = tiebreaker.Name
			}
			if len(leaders) == 1 {
				break
			}
		}
	}

	if len(leaders) == 1 {
		r.Winner = leaders[0]
	}
	for _, id := range ids {
		alliance := r.Alliances[id]
		switch {
		case len(leaders) == 1 && leaders[0] == id:
			alliance.Result = ResultWin
		case len(leaders) > 1 && contains(leaders, id):
			alliance.Result = ResultTie
		case len(ids) == 1 && !alliance.Disqualified:
			alliance.Result = ResultWin // A one-alliance game has nobody to lose to.
		default:
			alliance.Result = ResultLoss
		}
	}
	if len(ids) == 1 {
		r.Winner = ""
		if !r.Alliances[ids[0]].Disqualified {
			r.Winner = ids[0]
		}
	}
	return nil
}

func contains(list []string, item string) bool {
	for _, candidate := range list {
		if candidate == item {
			return true
		}
	}
	return false
}

// roundTiny removes floating point dust such as 0.30000000000000004 so that equal scores compare equal.
func roundTiny(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}

// Clock is the match clock as seen by live expressions.
type Clock struct {
	State          string  // idle, countdown, running, post, timeout
	Phase          string  // Current phase id, or "" outside a match.
	PhaseElapsed   float64 // Seconds since the phase began.
	PhaseDuration  float64
	MatchTime      float64 // Seconds since the match began.
	MatchRemaining float64
}

// ClockAt builds the clock for a moment elapsed seconds into the given phase of a running match. An unknown or
// empty phase gives an idle clock.
func ClockAt(g *gamedef.Game, phaseID string, elapsed float64) Clock {
	start := 0.0
	for _, phase := range g.Match.Phases {
		if phase.ID == phaseID {
			elapsed = math.Max(0, math.Min(elapsed, phase.Duration))
			return Clock{
				State: "running", Phase: phase.ID, PhaseElapsed: elapsed, PhaseDuration: phase.Duration,
				MatchTime: start + elapsed, MatchRemaining: g.Duration() - start - elapsed,
			}
		}
		start += phase.Duration
	}
	return Clock{State: "idle"}
}

func (c Clock) roots() map[string]any {
	return map[string]any{
		"match_state": c.State, "phase": c.Phase, "phase_elapsed": c.PhaseElapsed,
		"phase_duration": c.PhaseDuration, "phase_remaining": math.Max(0, c.PhaseDuration-c.PhaseElapsed),
		"match_time": c.MatchTime, "match_remaining": c.MatchRemaining,
	}
}

// LiveValues are the game's clock-dependent values at one instant.
type LiveValues struct {
	Alliances map[string]map[string]any `json:"alliances"`
	Field     map[string]any            `json:"field"`
}

// Live evaluates the game's live values against a computed result and the clock. Each live value may read the
// ones defined before it.
func (r *Result) Live(clock Clock) (*LiveValues, error) {
	g := r.env.game
	live := &LiveValues{Alliances: make(map[string]map[string]any), Field: make(map[string]any)}
	for _, id := range g.AllianceIDs() {
		live.Alliances[id] = make(map[string]any)
	}
	extra := clock.roots()
	for _, value := range g.Live {
		if value.Scope == gamedef.ScopeField {
			out, err := value.Expr.Prog.Run(r.env.roots("", extra))
			if err != nil {
				return nil, exprError("live value "+value.ID, value.Expr, err)
			}
			r.env.field[value.ID] = out
			live.Field[value.ID] = out
			continue
		}
		for _, id := range g.AllianceIDs() {
			out, err := value.Expr.Prog.Run(r.env.roots(id, extra))
			if err != nil {
				return nil, exprError("live value "+value.ID, value.Expr, err)
			}
			live.Alliances[id][value.ID] = out
		}
		for _, id := range g.AllianceIDs() {
			r.env.alliances[id][value.ID] = live.Alliances[id][value.ID]
		}
	}
	return live, nil
}

// Eval runs any expression compiled against this game's live roots (see gamedef.Game.LiveRoots), such as a device
// output binding. own is the alliance it is evaluated for, or "" for none. Call Live first if the expression
// reads live values.
func (r *Result) Eval(prog *rules.Program, own string, clock Clock, extra map[string]any) (any, error) {
	roots := clock.roots()
	for name, value := range extra {
		roots[name] = value
	}
	return prog.Run(r.env.roots(own, roots))
}

// TriggerEvent identifies a moment in the match that triggers can react to.
type TriggerEvent struct {
	PhaseStart string
	PhaseEnd   string
	Cue        string
}

// TriggerChanges evaluates the triggers that fire on the given event and returns the changes they make. The
// caller stamps and applies them like any other change; their source is the rule engine.
func TriggerChanges(g *gamedef.Game, s *State, opts Options, event TriggerEvent) ([]Change, error) {
	var changes []Change
	var result *Result
	for i, trigger := range g.Triggers {
		if trigger.On.PhaseStart != event.PhaseStart || trigger.On.PhaseEnd != event.PhaseEnd ||
			trigger.On.Cue != event.Cue {
			continue
		}
		if result == nil {
			var err error
			if result, err = Compute(g, s, opts); err != nil {
				return nil, err
			}
		}
		element := g.Element(trigger.Set.Element)
		owners := []string{""}
		if element.Scope == gamedef.ScopeAlliance {
			owners = g.AllianceIDs()
		}
		for _, owner := range owners {
			value, err := trigger.Set.Value.Prog.Run(result.env.roots(owner, nil))
			if err != nil {
				return nil, exprError(fmt.Sprintf("trigger %d", i+1), trigger.Set.Value, err)
			}
			switch element.Type {
			case gamedef.TypeBoolean:
				value = rules.Truthy(value)
			case gamedef.TypeNumber:
				number, ok := rules.ToNumber(value)
				if !ok {
					return nil, fmt.Errorf("trigger %d gave %v, but %s is a number", i+1, value, element.ID)
				}
				value = number
			case gamedef.TypeEnum:
				option, ok := value.(string)
				if !ok || element.Option(option) == nil {
					return nil, fmt.Errorf("trigger %d gave %v, which is not an option of %s", i+1, value, element.ID)
				}
			}
			changes = append(changes, Change{
				Alliance: owner, Element: element.ID, Op: OpSet, Value: value,
				Source: Source{Kind: SourceRule, ID: fmt.Sprintf("trigger-%d", i+1), Name: "Game rule"},
			})
		}
	}
	return changes, nil
}
