// Package scoring holds the raw score state of a match, the audited list of changes that produced it, and the
// computation of points, ranking points and rankings from that raw state using a game definition.
//
// The rule that shapes this package: raw state in, score out. Nothing here stores a total. A State holds only what
// happened on the field (counts, flags, selections, times, fouls, cards), and every number shown to a human is
// recomputed from it by Compute.
package scoring

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"sort"
	"time"
)

// Operations a Change can perform.
const (
	OpIncrement  = "increment"
	OpDecrement  = "decrement"
	OpSet        = "set"
	OpToggle     = "toggle"
	OpTimerStart = "timer_start"
	OpTimerStop  = "timer_stop"
	OpAdd        = "add"
	OpRemove     = "remove"
)

// Pseudo-elements that a Change can target besides the game's own scoring elements.
const (
	TargetFoul         = "foul"
	TargetCard         = "card"
	TargetDisqualified = "disqualified"
)

// Kinds of source that can make a change.
const (
	SourceDevice  = "device"
	SourceScorer  = "scorer"
	SourceReferee = "referee"
	SourceAdmin   = "admin"
	SourceRule    = "rule"
	SourceAPI     = "api"
)

// PhasePost is the phase recorded on changes made after the match has ended.
const PhasePost = "post"

// Value is the raw value of one scoring element. Which fields are used depends on the element's type.
type Value struct {
	Counts  map[string]int `json:"counts,omitempty"`  // counter: count per phase
	Bool    bool           `json:"bool,omitempty"`    // boolean
	Option  string         `json:"option,omitempty"`  // enum: selected option id
	Phase   string         `json:"phase,omitempty"`   // boolean, enum: phase in which it was last set
	Seconds float64        `json:"seconds,omitempty"` // timer: accumulated time
	Running bool           `json:"running,omitempty"` // timer: currently counting
	Since   float64        `json:"since,omitempty"`   // timer: match time at which it was started
	Number  float64        `json:"number,omitempty"`  // number
}

// Total returns the sum of a counter across all phases.
func (v *Value) Total() int {
	total := 0
	for _, count := range v.Counts {
		total += count
	}
	return total
}

// Foul is one foul called against an alliance.
type Foul struct {
	ID    int    `json:"id"` // The sequence number of the change that added it.
	Type  string `json:"type"`
	Rule  string `json:"rule,omitempty"`
	Robot int    `json:"robot,omitempty"` // Position 1..n of the offending robot, or 0 if not attributed.
	Phase string `json:"phase,omitempty"`
}

// AllianceState is the raw state belonging to one alliance.
type AllianceState struct {
	Elements     map[string]*Value   `json:"elements"`
	Robots       []map[string]*Value `json:"robots"` // Indexed by position-1.
	Fouls        []Foul              `json:"fouls"`
	Cards        map[int]string      `json:"cards"` // Robot position -> card id.
	Disqualified bool                `json:"disqualified"`
}

// State is the complete raw score state of a match.
type State struct {
	Alliances map[string]*AllianceState `json:"alliances"`
	Field     map[string]*Value         `json:"field"`
}

// Source says who or what made a change.
type Source struct {
	Kind       string   `json:"kind"`
	ID         string   `json:"id,omitempty"`
	Name       string   `json:"name,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"` // For devices that estimate, such as vision.
}

// Change is one audited modification of the raw state.
type Change struct {
	Seq       int       `json:"seq"`
	Time      time.Time `json:"time"`
	Phase     string    `json:"phase"`      // Phase when it happened; PhasePost after the match; "" before it.
	MatchTime float64   `json:"match_time"` // Seconds since the match started.
	Alliance  string    `json:"alliance,omitempty"`
	Robot     int       `json:"robot,omitempty"` // Position 1..n for robot elements, cards and attributed fouls.
	Element   string    `json:"element"`         // An element id, or one of the Target constants.
	Op        string    `json:"op"`
	Value     any       `json:"value,omitempty"`
	Rule      string    `json:"rule,omitempty"`    // For fouls: the rule cited.
	Bucket    string    `json:"bucket,omitempty"`  // Phase the change is credited to, when it differs from Phase.
	Reverts   int       `json:"reverts,omitempty"` // Seq of the change this one undoes.
	Source    Source    `json:"source"`
}

func (c *Change) bucket() string {
	if c.Bucket != "" {
		return c.Bucket
	}
	return c.Phase
}

// NewState returns the starting state for a match of the given game.
func NewState(g *gamedef.Game) *State {
	state := new(State)
	state.Normalize(g)
	return state
}

// Normalize fills in whatever a state is missing for the given game: alliances, robot positions, element values at
// their starting value, and the maps that hold them. A state that has been stored and loaded again has lost its
// empty maps and unset values, and one stored before an element was added to the game never had it; after
// Normalize both can be changed and scored like a fresh state. It never alters a value that is present.
func (s *State) Normalize(g *gamedef.Game) {
	if s.Alliances == nil {
		s.Alliances = make(map[string]*AllianceState)
	}
	if s.Field == nil {
		s.Field = make(map[string]*Value)
	}
	for _, alliance := range g.Alliances.List {
		allianceState := s.Alliances[alliance.ID]
		if allianceState == nil {
			allianceState = new(AllianceState)
			s.Alliances[alliance.ID] = allianceState
		}
		if allianceState.Elements == nil {
			allianceState.Elements = make(map[string]*Value)
		}
		if allianceState.Cards == nil {
			allianceState.Cards = make(map[int]string)
		}
		for len(allianceState.Robots) < g.Alliances.Size {
			allianceState.Robots = append(allianceState.Robots, nil)
		}
		for i, robot := range allianceState.Robots {
			if robot == nil {
				allianceState.Robots[i] = make(map[string]*Value)
			}
		}
	}
	fill := func(values map[string]*Value, element *gamedef.Element) {
		value := values[element.ID]
		if value == nil {
			values[element.ID] = initialValue(element)
		} else if element.Type == gamedef.TypeCounter && value.Counts == nil {
			value.Counts = make(map[string]int)
		}
	}
	for _, element := range g.Elements {
		switch element.Scope {
		case gamedef.ScopeField:
			fill(s.Field, element)
		case gamedef.ScopeAlliance:
			for _, alliance := range g.Alliances.List {
				fill(s.Alliances[alliance.ID].Elements, element)
			}
		case gamedef.ScopeRobot:
			for _, alliance := range g.Alliances.List {
				for _, robot := range s.Alliances[alliance.ID].Robots {
					fill(robot, element)
				}
			}
		}
	}
}

func initialValue(element *gamedef.Element) *Value {
	value := new(Value)
	switch element.Type {
	case gamedef.TypeCounter:
		value.Counts = make(map[string]int)
	case gamedef.TypeBoolean:
		value.Bool, _ = element.Initial.(bool)
	case gamedef.TypeEnum:
		value.Option = element.Options[0].ID
		if initial, ok := element.Initial.(string); ok {
			value.Option = initial
		}
	case gamedef.TypeNumber:
		value.Number, _ = rules.ToNumber(element.Initial)
		if element.Min != nil && value.Number < *element.Min {
			value.Number = *element.Min
		}
	}
	return value
}

// value returns the raw value a change targets, checking that the target exists.
func (s *State) value(g *gamedef.Game, element *gamedef.Element, alliance string, robot int) (*Value, error) {
	if element.Scope == gamedef.ScopeField {
		return s.Field[element.ID], nil
	}
	allianceState := s.Alliances[alliance]
	if allianceState == nil || g.Alliance(alliance) == nil {
		return nil, fmt.Errorf("there is no alliance %q", alliance)
	}
	if element.Scope == gamedef.ScopeAlliance {
		return allianceState.Elements[element.ID], nil
	}
	if robot < 1 || robot > g.Alliances.Size {
		return nil, fmt.Errorf("%s is scored per robot; robot position %d is out of range", element.ID, robot)
	}
	return allianceState.Robots[robot-1][element.ID], nil
}

// Apply performs one change on the state. It returns an error, leaving the state untouched, if the change does not
// make sense for the game: an unknown element, the wrong kind of operation, or a value out of range.
func (s *State) Apply(g *gamedef.Game, change *Change) error {
	s.Normalize(g)
	switch change.Element {
	case TargetFoul:
		return s.applyFoul(g, change)
	case TargetCard:
		return s.applyCard(g, change)
	case TargetDisqualified:
		allianceState := s.Alliances[change.Alliance]
		if allianceState == nil {
			return fmt.Errorf("there is no alliance %q", change.Alliance)
		}
		disqualified, ok := change.Value.(bool)
		if !ok || change.Op != OpSet {
			return fmt.Errorf("disqualified takes op set with true or false")
		}
		allianceState.Disqualified = disqualified
		return nil
	}

	element := g.Element(change.Element)
	if element == nil {
		return fmt.Errorf("there is no element %q", change.Element)
	}
	value, err := s.value(g, element, change.Alliance, change.Robot)
	if err != nil {
		return err
	}
	bucket := change.bucket()

	switch element.Type {
	case gamedef.TypeCounter:
		amount := 1
		if change.Value != nil {
			n, ok := rules.ToNumber(change.Value)
			if !ok || n != float64(int(n)) || n < 0 {
				return fmt.Errorf("%s needs a whole number that is not negative, got %v", element.ID, change.Value)
			}
			amount = int(n)
		}
		if g.Phase(bucket) == nil {
			return fmt.Errorf("%s is counted per phase; %q is not a phase of this game", element.ID, bucket)
		}
		next := value.Counts[bucket]
		switch change.Op {
		case OpIncrement:
			next += amount
		case OpDecrement:
			next -= amount
		case OpSet:
			next = amount
		default:
			return fmt.Errorf("%s is a counter; it does not support %q", element.ID, change.Op)
		}
		if next < 0 {
			return fmt.Errorf("%s has only %d in phase %s", element.ID, value.Counts[bucket], bucket)
		}
		total := value.Total() - value.Counts[bucket] + next
		if element.Max != nil && float64(total) > *element.Max {
			return fmt.Errorf("%s cannot go above %g", element.ID, *element.Max)
		}
		if next == 0 {
			delete(value.Counts, bucket)
		} else {
			value.Counts[bucket] = next
		}

	case gamedef.TypeBoolean:
		next := value.Bool
		switch change.Op {
		case OpToggle:
			next = !next
		case OpSet:
			b, ok := change.Value.(bool)
			if !ok {
				return fmt.Errorf("%s needs true or false, got %v", element.ID, change.Value)
			}
			next = b
		default:
			return fmt.Errorf("%s is a boolean; it does not support %q", element.ID, change.Op)
		}
		value.Bool = next
		value.Phase = ""
		if next {
			value.Phase = bucket
		}

	case gamedef.TypeEnum:
		option, ok := change.Value.(string)
		if change.Op != OpSet || !ok {
			return fmt.Errorf("%s is an enum; set it to one of its option ids", element.ID)
		}
		if element.Option(option) == nil {
			return fmt.Errorf("%s has no option %q", element.ID, option)
		}
		value.Option, value.Phase = option, bucket

	case gamedef.TypeTimer:
		switch change.Op {
		case OpTimerStart:
			if !value.Running {
				value.Running, value.Since = true, change.MatchTime
			}
		case OpTimerStop:
			if value.Running {
				value.Seconds += max(0, change.MatchTime-value.Since)
				value.Running, value.Since = false, 0
			}
		case OpSet:
			n, ok := rules.ToNumber(change.Value)
			if !ok || n < 0 {
				return fmt.Errorf("%s needs a number of seconds, got %v", element.ID, change.Value)
			}
			value.Seconds, value.Running, value.Since = n, false, 0
		default:
			return fmt.Errorf("%s is a timer; it does not support %q", element.ID, change.Op)
		}
		if element.Max != nil && value.Seconds > *element.Max {
			value.Seconds = *element.Max
		}

	case gamedef.TypeNumber:
		n, ok := rules.ToNumber(change.Value)
		if !ok {
			return fmt.Errorf("%s needs a number, got %v", element.ID, change.Value)
		}
		next := value.Number
		switch change.Op {
		case OpSet:
			next = n
		case OpIncrement:
			next += n
		case OpDecrement:
			next -= n
		default:
			return fmt.Errorf("%s is a number; it does not support %q", element.ID, change.Op)
		}
		if element.Min != nil && next < *element.Min {
			return fmt.Errorf("%s cannot go below %g", element.ID, *element.Min)
		}
		if element.Max != nil && next > *element.Max {
			return fmt.Errorf("%s cannot go above %g", element.ID, *element.Max)
		}
		value.Number = next
	}
	return nil
}

func (s *State) applyFoul(g *gamedef.Game, change *Change) error {
	allianceState := s.Alliances[change.Alliance]
	if allianceState == nil {
		return fmt.Errorf("there is no alliance %q", change.Alliance)
	}
	switch change.Op {
	case OpAdd:
		foulType, _ := change.Value.(string)
		if g.FoulType(foulType) == nil {
			return fmt.Errorf("there is no foul type %q", foulType)
		}
		if change.Robot < 0 || change.Robot > len(allianceState.Robots) {
			return fmt.Errorf("robot position %d is out of range", change.Robot)
		}
		allianceState.Fouls = append(allianceState.Fouls, Foul{
			ID: change.Seq, Type: foulType, Rule: change.Rule, Robot: change.Robot, Phase: change.bucket(),
		})
		return nil
	case OpRemove:
		id, ok := rules.ToNumber(change.Value)
		if ok {
			for i, foul := range allianceState.Fouls {
				if foul.ID == int(id) {
					allianceState.Fouls = append(allianceState.Fouls[:i:i], allianceState.Fouls[i+1:]...)
					return nil
				}
			}
		}
		return fmt.Errorf("there is no foul %v on alliance %s", change.Value, change.Alliance)
	}
	return fmt.Errorf("a foul can be added or removed, not %q", change.Op)
}

func (s *State) applyCard(g *gamedef.Game, change *Change) error {
	allianceState := s.Alliances[change.Alliance]
	if allianceState == nil {
		return fmt.Errorf("there is no alliance %q", change.Alliance)
	}
	if change.Robot < 1 || change.Robot > len(allianceState.Robots) {
		return fmt.Errorf("a card is given to a robot position; %d is out of range", change.Robot)
	}
	card, _ := change.Value.(string)
	if change.Op != OpSet || (card != "" && g.Card(card) == nil) {
		return fmt.Errorf("set a card to one of the game's card ids, or to \"\" to clear it")
	}
	if card == "" {
		delete(allianceState.Cards, change.Robot)
	} else {
		allianceState.Cards[change.Robot] = card
	}
	return nil
}

// Live returns the changes that are in effect: those that have not been undone, in order, without the undo
// records themselves. Undoing an undo brings the original change back.
func Live(changes []Change) []Change {
	dead := make(map[int]bool)
	for i := len(changes) - 1; i >= 0; i-- {
		change := &changes[i]
		if !dead[change.Seq] && change.Reverts != 0 {
			dead[change.Reverts] = true
		}
	}
	live := make([]Change, 0, len(changes))
	for _, change := range changes {
		if !dead[change.Seq] && change.Reverts == 0 {
			live = append(live, change)
		}
	}
	return live
}

// Fold rebuilds the raw state from a change list. Changes that no longer apply cleanly (for example a decrement
// whose matching increment was undone) are skipped and returned, so that one bad record can never make a match
// unscoreable.
func Fold(g *gamedef.Game, changes []Change) (*State, []Change) {
	state := NewState(g)
	var skipped []Change
	for _, change := range Live(changes) {
		change := change
		if err := state.Apply(g, &change); err != nil {
			skipped = append(skipped, change)
		}
	}
	return state, skipped
}

// sortedKeys returns the keys of a string-keyed map in order, for deterministic iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
