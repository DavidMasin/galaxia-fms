package gamedef

// View is the description of a game that browsers receive in order to build scoring panels, displays and the
// editor's sandbox. It contains names, shapes and layout hints, and no expressions.
type View struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	Description   string         `json:"description"`
	Hash          string         `json:"hash"`
	AllianceSize  int            `json:"allianceSize"`
	Alliances     []*Alliance    `json:"alliances"`
	Stations      []string       `json:"stations"`
	Params        []ParamView    `json:"params"`
	Countdown     Countdown      `json:"countdown"`
	Phases        []PhaseView    `json:"phases"`
	Duration      float64        `json:"duration"`
	ScoreGrace    float64        `json:"scoreGrace"`
	Elements      []ElementView  `json:"elements"`
	FoulTypes     []*FoulType    `json:"foulTypes"`
	Rules         []*Rule        `json:"rules"`
	Cards         []*Card        `json:"cards"`
	Derived       []ValueView    `json:"derived"`
	Live          []ValueView    `json:"live"`
	Breakdown     []NameView     `json:"breakdown"`
	RankingPoints []NameView     `json:"rankingPoints"`
	RankingStats  []NameView     `json:"rankingStats"`
	RankingOrder  []OrderView    `json:"rankingOrder"`
	Tiebreakers   []string       `json:"tiebreakers"`
	Panels        []*Panel       `json:"panels"`
	AudienceLive  []string       `json:"audienceLive"`
	Indicators    []string       `json:"indicators"`
	Features      map[string]any `json:"features"`
}

type ParamView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Default any      `json:"default"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
}

type PhaseView struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Duration float64   `json:"duration"`
	Robots   string    `json:"robots"`
	Start    float64   `json:"start"` // Seconds from the start of the match.
	Cues     []CueView `json:"cues"`
}

type CueView struct {
	ID     string  `json:"id,omitempty"`
	Offset float64 `json:"offset"` // Seconds from the start of the phase.
	Sound  string  `json:"sound,omitempty"`
}

type ElementView struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Type    string       `json:"type"`
	Scope   string       `json:"scope"`
	Min     *float64     `json:"min,omitempty"`
	Max     *float64     `json:"max,omitempty"`
	Step    *float64     `json:"step,omitempty"`
	Unit    string       `json:"unit,omitempty"`
	Initial any          `json:"initial,omitempty"`
	Options []OptionView `json:"options,omitempty"`
	Edit    []string     `json:"edit"`
	Phases  []string     `json:"phases"` // Phases in which live changes are accepted.
	Cycle   bool         `json:"cycle,omitempty"`
	Group   string       `json:"group"`
	Widget  string       `json:"widget"`
	Order   float64      `json:"order"`
	Hidden  bool         `json:"hidden,omitempty"`
	// PhasePoints is true when what the element earns depends on the phase it was set in, so an editor needs to
	// ask for the phase.
	PhasePoints bool `json:"phasePoints,omitempty"`
}

type OptionView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ValueView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Points bool   `json:"points,omitempty"`
}

type NameView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type OrderView struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Decimals int    `json:"decimals"`
	Random   bool   `json:"random,omitempty"`
}

// defaultWidget picks the control used for an element when the game does not say.
func defaultWidget(element *Element) string {
	switch element.Type {
	case TypeCounter:
		return "stepper"
	case TypeBoolean:
		return "toggle"
	case TypeEnum:
		if len(element.Options) <= 4 {
			return "buttons"
		}
		return "select"
	case TypeTimer:
		return "timer"
	}
	return "input"
}

// View builds the browser-facing description of the game.
func (g *Game) View() *View {
	view := &View{
		ID: g.ID, Name: g.Name, Version: g.Version, Description: g.Description, Hash: g.Hash,
		AllianceSize: g.Alliances.Size, Alliances: g.Alliances.List, Stations: g.StationIDs(),
		Countdown: g.Match.Countdown, Duration: g.Duration(), ScoreGrace: g.Match.ScoreGrace,
		FoulTypes: g.Fouls.Types, Rules: g.Fouls.Rules, Cards: g.Cards, Panels: g.Layout.ScoringPanels,
		AudienceLive: g.Layout.Audience.Live, Indicators: g.Layout.Audience.Indicators,
		Params: []ParamView{}, Elements: []ElementView{}, Derived: []ValueView{}, Live: []ValueView{},
		Breakdown: []NameView{}, RankingPoints: []NameView{}, RankingStats: []NameView{},
		RankingOrder: []OrderView{}, Tiebreakers: []string{},
		Features: map[string]any{
			// Bracket playoffs and alliance selection need exactly two alliances.
			"brackets": len(g.Alliances.List) == 2,
			"gameData": g.Match.DSGameData != nil,
		},
	}
	// Browsers iterate over these lists, so none of them may be sent as null.
	if view.FoulTypes == nil {
		view.FoulTypes = []*FoulType{}
	}
	if view.Rules == nil {
		view.Rules = []*Rule{}
	}
	if view.Cards == nil {
		view.Cards = []*Card{}
	}
	if view.Panels == nil {
		view.Panels = []*Panel{}
	}
	if view.AudienceLive == nil {
		view.AudienceLive = []string{}
	}
	if view.Indicators == nil {
		view.Indicators = []string{}
	}
	for _, param := range g.Params {
		view.Params = append(view.Params, ParamView{
			ID: param.ID, Name: param.Name, Type: param.Type, Default: param.Default, Min: param.Min, Max: param.Max,
		})
	}
	start := 0.0
	for _, phase := range g.Match.Phases {
		phaseView := PhaseView{
			ID: phase.ID, Name: phase.Name, Duration: phase.Duration, Robots: phase.Robots, Start: start,
			Cues: []CueView{},
		}
		for _, cue := range phase.Cues {
			phaseView.Cues = append(
				phaseView.Cues, CueView{ID: cue.ID, Offset: cue.Offset(phase.Duration), Sound: cue.Sound},
			)
		}
		view.Phases = append(view.Phases, phaseView)
		start += phase.Duration
	}
	for _, element := range g.Elements {
		elementView := ElementView{
			ID: element.ID, Name: element.Name, Type: element.Type, Scope: element.Scope, Min: element.Min,
			Max: element.Max, Step: element.Step, Unit: element.Unit, Initial: element.Initial, Edit: element.Edit,
			Phases: element.Phases, Cycle: element.Cycle, Group: element.UI.Group, Widget: element.UI.Widget,
			Order: element.UI.Order, Hidden: element.UI.Hidden,
			PhasePoints: element.Points != nil && element.Points.ByPhase != nil,
		}
		if elementView.Widget == "" {
			elementView.Widget = defaultWidget(element)
		}
		if elementView.Group == "" {
			elementView.Group = "Scoring"
		}
		if len(elementView.Edit) == 0 {
			elementView.Edit = []string{"device", "scorer", "referee"}
		}
		if len(elementView.Phases) == 0 {
			for _, phase := range g.Match.Phases {
				elementView.Phases = append(elementView.Phases, phase.ID)
			}
		}
		for _, option := range element.Options {
			elementView.Options = append(elementView.Options, OptionView{ID: option.ID, Name: option.Name})
			if option.Points != nil && option.Points.ByPhase != nil {
				elementView.PhasePoints = true
			}
		}
		view.Elements = append(view.Elements, elementView)
	}
	for _, derived := range g.Derived {
		view.Derived = append(view.Derived, ValueView{
			ID: derived.ID, Name: derived.Name, Scope: derived.Scope, Points: derived.Points != nil,
		})
	}
	for _, live := range g.Live {
		view.Live = append(view.Live, ValueView{ID: live.ID, Name: live.Name, Scope: live.Scope})
	}
	for _, row := range g.Breakdown {
		view.Breakdown = append(view.Breakdown, NameView{ID: row.ID, Name: row.Name})
	}
	for _, rankingPoint := range g.Ranking.Points {
		view.RankingPoints = append(view.RankingPoints, NameView{ID: rankingPoint.ID, Name: rankingPoint.Name})
	}
	for _, stat := range g.Ranking.Stats {
		view.RankingStats = append(view.RankingStats, NameView{ID: stat.ID, Name: stat.Name})
	}
	for _, key := range g.Ranking.Order {
		view.RankingOrder = append(
			view.RankingOrder, OrderView{Name: key.Name, Dir: key.Dir, Decimals: key.Decimals, Random: key.Random},
		)
	}
	for _, tiebreaker := range g.Playoff.Tiebreakers {
		view.Tiebreakers = append(view.Tiebreakers, tiebreaker.Name)
	}
	return view
}
