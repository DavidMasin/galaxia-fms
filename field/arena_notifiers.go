// Copyright 2018 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Contains configuration of the publish-subscribe notifiers that allow the arena to push updates to websocket clients.

package field

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/playoff"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"log"
	"math"
	"strconv"
)

type ArenaNotifiers struct {
	AllianceSelectionNotifier          *websocket.Notifier
	AllianceStationDisplayModeNotifier *websocket.Notifier
	ArenaStatusNotifier                *websocket.Notifier
	AudienceDisplayModeNotifier        *websocket.Notifier
	DisplayConfigurationNotifier       *websocket.Notifier
	EventStatusNotifier                *websocket.Notifier
	GameNotifier                       *websocket.Notifier
	LowerThirdNotifier                 *websocket.Notifier
	MatchLoadNotifier                  *websocket.Notifier
	MatchTimeNotifier                  *websocket.Notifier
	PlaySoundNotifier                  *websocket.Notifier
	RealtimeScoreNotifier              *websocket.Notifier
	ReloadDisplaysNotifier             *websocket.Notifier
	ScoreChangeNotifier                *websocket.Notifier
	ScorePostedNotifier                *websocket.Notifier
	ScoringStatusNotifier              *websocket.Notifier
}

// MatchTimeMessage is the clock as browsers receive it. PhaseIndex is -1 and PhaseId is empty outside InMatch.
// TimeoutDurationSec is the length of the timeout or break that is counting down, or of the last one; during
// TimeoutActive MatchTimeSec counts up towards it.
type MatchTimeMessage struct {
	MatchState
	MatchTimeSec       int
	PhaseIndex         int
	PhaseId            string
	PhaseTimeSec       int
	PhaseRemainingSec  int
	TimeoutDurationSec int
}

// RealtimeScoreMessage is the live score as browsers receive it. State is raw; everything else is computed.
type RealtimeScoreMessage struct {
	GameHash  string                            `json:"gameHash"`
	Seq       int                               `json:"seq"` // Sequence number of the last change.
	Winner    string                            `json:"winner"`
	Tiebreak  string                            `json:"tiebreak"`
	Field     RealtimeFieldScore                `json:"field"`
	Alliances map[string]*RealtimeAllianceScore `json:"alliances"`
}

type RealtimeFieldScore struct {
	State  map[string]*scoring.Value `json:"state"`
	Values map[string]any            `json:"values"`
	Live   map[string]any            `json:"live"`
}

type RealtimeAllianceScore struct {
	Score       float64                       `json:"score"`
	FoulPoints  float64                       `json:"foulPoints"`
	Points      map[string]float64            `json:"points"`
	PhasePoints map[string]map[string]float64 `json:"phasePoints"`
	Values      map[string]any                `json:"values"`
	Breakdown   []scoring.Row                 `json:"breakdown"`
	Result      string                        `json:"result"`
	RP          map[string]float64            `json:"rp"`
	RPTotal     float64                       `json:"rpTotal"`
	Live        map[string]any                `json:"live"`
	State       *scoring.AllianceState        `json:"state"`
}

// ScorePostedMessage is the committed match as browsers receive it for the final score screen.
type ScorePostedMessage struct {
	Match     *model.Match                    `json:"match"`
	GameId    string                          `json:"gameId"`
	GameHash  string                          `json:"gameHash"`
	Winner    string                          `json:"winner"`   // Alliance id, or empty for a tie.
	Tiebreak  string                          `json:"tiebreak"` // Name of the playoff tiebreaker that decided it.
	Alliances map[string]*ScorePostedAlliance `json:"alliances"`
}

type ScorePostedAlliance struct {
	Teams         []int                       `json:"teams"`         // By robot position; zero where there is none.
	OffFieldTeams []int                       `json:"offFieldTeams"` // Playoff alliance members not on the field.
	Score         float64                     `json:"score"`
	Breakdown     []scoring.Row               `json:"breakdown"`
	RankingPoints []ScorePostedRankingPoint   `json:"rankingPoints"`
	RPTotal       float64                     `json:"rpTotal"`
	Cards         map[int]string              `json:"cards"` // Robot position -> card id.
	Fouls         []ScorePostedFoul           `json:"fouls"`
	Result        string                      `json:"result"`
	Disqualified  bool                        `json:"disqualified"`
	Rankings      map[int]*ScorePostedRanking `json:"rankings"`    // Team number -> rank; qualification only.
	Wins          int                         `json:"wins"`        // Playoff series wins so far.
	Destination   string                      `json:"destination"` // Where the playoff alliance goes next.
}

type ScorePostedRankingPoint struct {
	Id    string  `json:"id"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// ScorePostedFoul is a foul with the names a person needs to read it.
type ScorePostedFoul struct {
	scoring.Foul
	TypeName string  `json:"typeName"`
	Points   float64 `json:"points"`
	Team     int     `json:"team"` // Team number of the offending robot, or zero if not attributed.
	RuleName string  `json:"ruleName"`
	RuleText string  `json:"ruleText"`
}

type ScorePostedRanking struct {
	Rank         int `json:"rank"`
	PreviousRank int `json:"previousRank"`
}

// Instantiates notifiers and configures their message producing methods.
func (arena *Arena) configureNotifiers() {
	arena.AllianceSelectionNotifier = websocket.NewNotifier("allianceSelection", arena.generateAllianceSelectionMessage)
	arena.AllianceStationDisplayModeNotifier = websocket.NewNotifier(
		"allianceStationDisplayMode", arena.generateAllianceStationDisplayModeMessage,
	)
	arena.ArenaStatusNotifier = websocket.NewNotifier("arenaStatus", arena.generateArenaStatusMessage)
	arena.AudienceDisplayModeNotifier = websocket.NewNotifier(
		"audienceDisplayMode", arena.generateAudienceDisplayModeMessage,
	)
	arena.DisplayConfigurationNotifier = websocket.NewNotifier(
		"displayConfiguration", arena.generateDisplayConfigurationMessage,
	)
	arena.EventStatusNotifier = websocket.NewNotifier("eventStatus", arena.generateEventStatusMessage)
	arena.GameNotifier = websocket.NewNotifier("game", arena.generateGameMessage)
	arena.LowerThirdNotifier = websocket.NewNotifier("lowerThird", arena.generateLowerThirdMessage)
	arena.MatchLoadNotifier = websocket.NewNotifier("matchLoad", arena.GenerateMatchLoadMessage)
	arena.MatchTimeNotifier = websocket.NewNotifier("matchTime", arena.generateMatchTimeMessage)
	arena.PlaySoundNotifier = websocket.NewNotifier("playSound", nil)
	arena.RealtimeScoreNotifier = websocket.NewNotifier("realtimeScore", arena.generateRealtimeScoreMessage)
	arena.ReloadDisplaysNotifier = websocket.NewNotifier("reload", nil)
	arena.ScoreChangeNotifier = websocket.NewNotifier("scoreChange", nil)
	arena.ScorePostedNotifier = websocket.NewNotifier("scorePosted", arena.GenerateScorePostedMessage)
	arena.ScoringStatusNotifier = websocket.NewNotifier("scoringStatus", arena.generateScoringStatusMessage)
}

func (arena *Arena) generateAllianceSelectionMessage() any {
	return &struct {
		Alliances        []model.Alliance
		ShowTimer        bool
		TimeRemainingSec int
		RankedTeams      []model.AllianceSelectionRankedTeam
	}{
		arena.AllianceSelectionAlliances,
		arena.AllianceSelectionShowTimer,
		arena.AllianceSelectionTimeRemainingSec,
		arena.AllianceSelectionRankedTeams,
	}
}

func (arena *Arena) generateAllianceStationDisplayModeMessage() any {
	return arena.AllianceStationDisplayMode
}

func (arena *Arena) generateArenaStatusMessage() any {
	startMatchConditions := arena.getStartMatchConditions()
	return &struct {
		MatchId          int
		AllianceStations map[string]*AllianceStation
		MatchState
		CanStartMatch         bool
		StartMatchConditions  []string
		AccessPointStatus     string
		SwitchStatus          string
		RedSCCStatus          string
		BlueSCCStatus         string
		PlcIsHealthy          bool
		FieldEStop            bool
		IsFtaReady            bool
		PlcArmorBlockStatuses map[string]bool
	}{
		arena.CurrentMatch.Id,
		arena.AllianceStations,
		arena.MatchState,
		len(startMatchConditions) == 0,
		startMatchConditions,
		arena.accessPoint.Status,
		arena.networkSwitch.Status,
		arena.redSCC.Status,
		arena.blueSCC.Status,
		arena.Plc.IsHealthy(),
		arena.Plc.GetFieldEStop(),
		arena.Plc.IsFtaReady(),
		arena.Plc.GetArmorBlockStatuses(),
	}
}

func (arena *Arena) generateAudienceDisplayModeMessage() any {
	return arena.AudienceDisplayMode
}

func (arena *Arena) generateDisplayConfigurationMessage() any {
	// Notify() for this notifier must always called from a method that has a lock on the display mutex.
	// Make a copy of the map to avoid potential data races; otherwise the same map would get iterated through as it is
	// serialized to JSON, outside the mutex lock.
	displaysCopy := make(map[string]Display)
	for displayId, display := range arena.Displays {
		displaysCopy[displayId] = *display
	}
	return displaysCopy
}

func (arena *Arena) generateEventStatusMessage() any {
	return arena.EventStatus
}

func (arena *Arena) generateGameMessage() any {
	return arena.Game.View()
}

func (arena *Arena) generateLowerThirdMessage() any {
	return &struct {
		LowerThird     *model.LowerThird
		ShowLowerThird bool
	}{arena.LowerThird, arena.ShowLowerThird}
}

func (arena *Arena) GenerateMatchLoadMessage() any {
	teams := make(map[string]*model.Team)
	var allTeamIds []int
	for station, allianceStation := range arena.AllianceStations {
		teams[station] = allianceStation.Team
		if allianceStation.Team != nil {
			allTeamIds = append(allTeamIds, allianceStation.Team.Id)
		}
	}

	matchResult, err := arena.Database.GetMatchResultForMatch(arena.CurrentMatch.Id)
	if err != nil {
		log.Printf("Failed to get match result for match %d while generating match load message: %v",
			arena.CurrentMatch.Id, err)
	}
	isReplay := matchResult != nil

	var playoffRedAllianceYellowCard, playoffBlueAllianceYellowCard bool
	var matchup *playoff.Matchup
	redOffFieldTeams := []*model.Team{}
	blueOffFieldTeams := []*model.Team{}
	if arena.CurrentMatch.Type == model.Playoff {
		playoffRedAllianceYellowCard = arena.getAllianceYellowCard(arena.CurrentMatch.PlayoffRedAlliance)
		playoffBlueAllianceYellowCard = arena.getAllianceYellowCard(arena.CurrentMatch.PlayoffBlueAlliance)
		matchGroup := arena.PlayoffTournament.MatchGroups()[arena.CurrentMatch.PlayoffMatchGroupId]
		matchup, _ = matchGroup.(*playoff.Matchup)
		redOffFieldTeamIds, blueOffFieldTeamIds, err := arena.Database.GetOffFieldTeamIds(arena.CurrentMatch)
		if err != nil {
			log.Printf("Failed to get off-field teams for match %d while generating match load message: %v",
				arena.CurrentMatch.Id, err)
		}
		for _, teamId := range redOffFieldTeamIds {
			team, err := arena.Database.GetTeamById(teamId)
			if err != nil {
				log.Printf("Failed to get red off-field team %d while generating match load message: %v", teamId, err)
			}
			redOffFieldTeams = append(redOffFieldTeams, team)
			allTeamIds = append(allTeamIds, teamId)
		}
		for _, teamId := range blueOffFieldTeamIds {
			team, err := arena.Database.GetTeamById(teamId)
			if err != nil {
				log.Printf("Failed to get blue off-field team %d while generating match load message: %v", teamId, err)
			}
			blueOffFieldTeams = append(blueOffFieldTeams, team)
			allTeamIds = append(allTeamIds, teamId)
		}
	}

	rankings := make(map[string]int)
	for _, teamId := range allTeamIds {
		ranking, err := arena.Database.GetRankingForTeam(teamId)
		if err != nil {
			log.Printf("Failed to get ranking for team %d while generating match load message: %v", teamId, err)
		}
		if ranking != nil {
			rankings[strconv.Itoa(teamId)] = ranking.Rank
		}
	}

	// Don't allow manual substitution of practice/playoff teams when they would be automatically overwritten by Nexus.
	var allowManualSubstitution = arena.CurrentMatch.ShouldAllowSubstitution() &&
		!(arena.EventSettings.NexusEnabled && arena.CurrentMatch.ShouldAllowNexusSubstitution())

	return &struct {
		PlayoffRedAllianceYellowCard  bool
		PlayoffBlueAllianceYellowCard bool
		Match                         *model.Match
		AllowSubstitution             bool
		IsReplay                      bool
		Teams                         map[string]*model.Team
		Rankings                      map[string]int
		Matchup                       *playoff.Matchup
		RedOffFieldTeams              []*model.Team
		BlueOffFieldTeams             []*model.Team
		BreakDescription              string
		BreakNextMatchName            string
	}{
		playoffRedAllianceYellowCard,
		playoffBlueAllianceYellowCard,
		arena.CurrentMatch,
		allowManualSubstitution,
		isReplay,
		teams,
		rankings,
		matchup,
		redOffFieldTeams,
		blueOffFieldTeams,
		arena.breakDescription,
		arena.breakNextMatchName,
	}
}

// Read fresh alliance state so result corrections and substitutions are reflected immediately.
func (arena *Arena) getAllianceYellowCard(allianceId int) bool {
	alliance, err := arena.Database.GetAllianceById(allianceId)
	if err != nil {
		log.Printf("Failed to get yellow card for alliance %d: %v", allianceId, err)
		return false
	}
	return alliance != nil && alliance.YellowCard
}

func (arena *Arena) generateMatchTimeMessage() any {
	message := MatchTimeMessage{
		MatchState: arena.MatchState, MatchTimeSec: int(arena.MatchTimeSec()), PhaseIndex: -1,
		TimeoutDurationSec: arena.TimeoutDurationSec,
	}
	if phase := arena.CurrentPhase(); phase != nil {
		phaseTimeSec := arena.PhaseTimeSec()
		message.PhaseIndex = arena.PhaseIndex
		message.PhaseId = phase.ID
		message.PhaseTimeSec = int(phaseTimeSec)
		message.PhaseRemainingSec = int(math.Ceil(math.Max(0, phase.Duration-phaseTimeSec)))
	}
	return message
}

func (arena *Arena) generateRealtimeScoreMessage() any {
	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	score := arena.Score
	message := &RealtimeScoreMessage{
		GameHash: arena.Game.Hash,
		Field: RealtimeFieldScore{
			State: copyValues(score.State.Field), Values: map[string]any{}, Live: map[string]any{},
		},
		Alliances: make(map[string]*RealtimeAllianceScore, len(arena.Game.Alliances.List)),
	}
	if len(score.Changes) > 0 {
		message.Seq = score.Changes[len(score.Changes)-1].Seq
	}
	if score.Result != nil {
		message.Winner, message.Tiebreak = score.Result.Winner, score.Result.Tiebreak
		message.Field.Values = score.Result.Field
	}
	if score.Live != nil {
		message.Field.Live = score.Live.Field
	}
	for _, alliance := range arena.Game.Alliances.List {
		// The raw state is copied because the message is turned into JSON later, on the goroutine of each browser
		// connection, by which time the arena may be changing it again. Everything computed is replaced, never
		// modified, so it can be shared.
		allianceScore := &RealtimeAllianceScore{
			Points: map[string]float64{}, PhasePoints: map[string]map[string]float64{}, Values: map[string]any{},
			Breakdown: []scoring.Row{}, RP: map[string]float64{}, Live: map[string]any{},
			State: copyAllianceState(score.State.Alliances[alliance.ID]),
		}
		if score.Result != nil {
			result := score.Result.Alliances[alliance.ID]
			allianceScore.Score, allianceScore.FoulPoints = result.Score, result.FoulPoints
			allianceScore.Points, allianceScore.PhasePoints = result.Points, result.PhasePoints
			allianceScore.Values = result.Values
			allianceScore.Result, allianceScore.RP, allianceScore.RPTotal = result.Result, result.RP, result.RPTotal
			if result.Breakdown != nil {
				allianceScore.Breakdown = result.Breakdown
			}
		}
		if score.Live != nil {
			allianceScore.Live = score.Live.Alliances[alliance.ID]
		}
		message.Alliances[alliance.ID] = allianceScore
	}
	return message
}

func copyValues(values map[string]*scoring.Value) map[string]*scoring.Value {
	copied := make(map[string]*scoring.Value, len(values))
	for id, value := range values {
		valueCopy := *value
		if value.Counts != nil {
			valueCopy.Counts = make(map[string]int, len(value.Counts))
			for phase, count := range value.Counts {
				valueCopy.Counts[phase] = count
			}
		}
		copied[id] = &valueCopy
	}
	return copied
}

func copyAllianceState(state *scoring.AllianceState) *scoring.AllianceState {
	copied := &scoring.AllianceState{
		Elements: copyValues(state.Elements), Robots: make([]map[string]*scoring.Value, len(state.Robots)),
		Fouls: append([]scoring.Foul{}, state.Fouls...), Cards: make(map[int]string, len(state.Cards)),
		Disqualified: state.Disqualified,
	}
	for i, robot := range state.Robots {
		copied.Robots[i] = copyValues(robot)
	}
	for position, card := range state.Cards {
		copied.Cards[position] = card
	}
	return copied
}

// GenerateScorePostedMessage describes the last committed match for the final score screen. The result is
// recomputed from what was stored, with the game it was played with.
func (arena *Arena) GenerateScorePostedMessage() any {
	match, matchResult := arena.SavedMatch, arena.SavedMatchResult
	message := &ScorePostedMessage{
		Match: match, GameId: matchResult.GameId, GameHash: matchResult.GameHash,
		Alliances: map[string]*ScorePostedAlliance{},
	}
	result, g, err := matchResult.Compute(arena.Database, match.UseTiebreakCriteria)
	if err != nil {
		log.Printf("Failed to compute the result of match %d for the score posted message: %v", match.Id, err)
		return message
	}
	message.Winner, message.Tiebreak = result.Winner, result.Tiebreak
	state := matchResult.State
	if state == nil {
		state, _ = scoring.Fold(g, matchResult.Changes)
	}

	// The match record, the bracket and the off-field lists have a red and a blue side: the first and second
	// alliance of the game.
	sideTeams := [][]int{{match.Red1, match.Red2, match.Red3}, {match.Blue1, match.Blue2, match.Blue3}}
	sideWins := []int{0, 0}
	sideDestinations := []string{"", ""}
	sideOffFieldTeams := [][]int{{}, {}}
	if match.Type == model.Playoff {
		matchGroup := arena.PlayoffTournament.MatchGroups()[match.PlayoffMatchGroupId]
		if matchup, ok := matchGroup.(*playoff.Matchup); ok {
			sideWins = []int{matchup.RedAllianceWins, matchup.BlueAllianceWins}
			sideDestinations = []string{matchup.RedAllianceDestination(), matchup.BlueAllianceDestination()}
		}
		redOffFieldTeamIds, blueOffFieldTeamIds, err := arena.Database.GetOffFieldTeamIds(match)
		if err != nil {
			log.Printf("Failed to get off-field teams for match %d while generating score posted message: %v",
				match.Id, err)
		} else {
			sideOffFieldTeams = [][]int{redOffFieldTeamIds, blueOffFieldTeamIds}
		}
	}
	rankings := make(map[int]*ScorePostedRanking, len(arena.SavedRankings))
	if match.Type == model.Qualification {
		for _, ranking := range arena.SavedRankings {
			rankings[ranking.TeamId] = &ScorePostedRanking{Rank: ranking.Rank, PreviousRank: ranking.PreviousRank}
		}
	}

	for index, alliance := range g.Alliances.List {
		allianceResult, allianceState := result.Alliances[alliance.ID], state.Alliances[alliance.ID]
		posted := &ScorePostedAlliance{
			Teams: make([]int, g.Alliances.Size), OffFieldTeams: []int{}, Score: allianceResult.Score,
			Breakdown: allianceResult.Breakdown, RankingPoints: []ScorePostedRankingPoint{},
			RPTotal: allianceResult.RPTotal, Cards: map[int]string{}, Fouls: []ScorePostedFoul{},
			Result: allianceResult.Result, Disqualified: allianceResult.Disqualified,
			Rankings: map[int]*ScorePostedRanking{},
		}
		if posted.Breakdown == nil {
			posted.Breakdown = []scoring.Row{}
		}
		if index < len(sideTeams) {
			copy(posted.Teams, sideTeams[index])
			posted.Wins, posted.Destination = sideWins[index], sideDestinations[index]
			if sideOffFieldTeams[index] != nil {
				posted.OffFieldTeams = sideOffFieldTeams[index]
			}
		}
		for _, teamId := range posted.Teams {
			if ranking, ok := rankings[teamId]; ok {
				posted.Rankings[teamId] = ranking
			}
		}
		for _, rankingPoint := range g.Ranking.Points {
			posted.RankingPoints = append(
				posted.RankingPoints,
				ScorePostedRankingPoint{
					Id: rankingPoint.ID, Name: rankingPoint.Name, Value: allianceResult.RP[rankingPoint.ID],
				},
			)
		}
		for position, card := range allianceState.Cards {
			posted.Cards[position] = card
		}
		for _, foul := range allianceState.Fouls {
			posted.Fouls = append(posted.Fouls, describeFoul(g, foul, posted.Teams))
		}
		message.Alliances[alliance.ID] = posted
	}
	return message
}

// describeFoul adds to a foul the names of its type and of the rule cited, and the team that committed it.
func describeFoul(g *gamedef.Game, foul scoring.Foul, teams []int) ScorePostedFoul {
	described := ScorePostedFoul{Foul: foul}
	if foulType := g.FoulType(foul.Type); foulType != nil {
		described.TypeName, described.Points = foulType.Name, foulType.Points
	}
	if foul.Robot >= 1 && foul.Robot <= len(teams) {
		described.Team = teams[foul.Robot-1]
	}
	// The same rule id may be listed once per foul type, with a different description for each; the entry for this
	// type of foul is the one that was cited.
	found := false
	for _, rule := range g.Fouls.Rules {
		if rule.ID == foul.Rule && (rule.Foul == foul.Type || !found) {
			described.RuleName, described.RuleText = rule.Name, rule.Text
			found = true
			if rule.Foul == foul.Type {
				break
			}
		}
	}
	return described
}

func (arena *Arena) generateScoringStatusMessage() any {
	type positionStatus struct {
		Ready          bool
		NumPanels      int
		NumPanelsReady int
	}
	positionStatuses := make(map[string]positionStatus)
	for _, panel := range arena.ScoringPanels() {
		positionStatuses[panel.ID] = positionStatus{
			Ready:          arena.positionPostMatchScoreReady(panel.ID),
			NumPanels:      arena.ScoringPanelRegistry.GetNumPanels(panel.ID),
			NumPanelsReady: arena.GetNumScoreCommitted(panel.ID),
		}
	}

	return &struct {
		RefereeScoreReady bool
		PositionStatuses  map[string]positionStatus
	}{
		arena.RefereeScoreReady(),
		positionStatuses,
	}
}
