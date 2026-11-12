// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Functions for controlling the arena and match play.

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/games"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/network"
	"github.com/DavidMasin/galaxia-fms/partner"
	"github.com/DavidMasin/galaxia-fms/playoff"
	"github.com/DavidMasin/galaxia-fms/plc"
	"log"
	"net"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	arenaLoopPeriodMs        = 10
	busRingSize              = 5000
	gamesDir                 = "games"
	gamesPollPeriodSec       = 2
	arenaLoopWarningMs       = 5
	dsPacketPeriodMs         = 500
	dsPacketWarningMs        = 550
	teamLogPeriodMs          = 500
	periodicTaskPeriodSec    = 15
	matchEndScoreDwellSec    = 3
	postTimeoutSec           = 4
	preLoadNextMatchDelaySec = 5
	scheduledBreakDelaySec   = 5
	earlyLateThresholdMin    = 2.5
	MaxMatchGapMin           = 20
)

// Progression of match states. What happens inside a match is the business of the game being played: while the
// state is InMatch, Arena.PhaseIndex says which of the game's phases is running.
type MatchState int

const (
	PreMatch      MatchState = iota // A match is loaded and has not started.
	StartMatch                      // Transitional: set by StartMatch(), consumed by the next Update().
	InMatch                         // A phase is running.
	PostMatch                       // The match ended or was aborted; scores are pending.
	TimeoutActive                   // A timeout or break is counting down.
	PostTimeout                     // Brief dwell after a timeout.
)

type Arena struct {
	Bus              *bus.Bus
	Games            *gamedef.Registry
	Game             *gamedef.Game // The active game. Only refreshGame replaces it, and only in PreMatch.
	Database         *model.Database
	EventSettings    *model.EventSettings
	accessPoint      network.AccessPoint
	networkSwitch    *network.Switch
	redSCC           *network.SCCSwitch
	blueSCC          *network.SCCSwitch
	Plc              plc.Plc
	TbaClient        *partner.TbaClient
	NexusClient      *partner.NexusClient
	BlackmagicClient *partner.BlackmagicClient
	CompanionClient  *partner.CompanionClient
	AllianceStations map[string]*AllianceStation
	Displays         map[string]*Display
	displayRevision  uint64
	TeamSigns        *TeamSigns
	ScoringPanelRegistry
	ArenaNotifiers
	MatchState
	lastMatchState                    MatchState
	PhaseIndex                        int // Index into Game.Match.Phases while InMatch; -1 otherwise.
	lastPhaseIndex                    int
	CurrentMatch                      *model.Match
	MatchStartTime                    time.Time
	LastMatchTimeSec                  float64
	TimeoutDurationSec                int
	Score                             *MatchScore // Guarded by scoreMutex.
	scoreMutex                        sync.Mutex
	foulsCommitted                    map[string]bool // By alliance id. Guarded by scoreMutex.
	gameParams                        map[string]float64
	gameRefreshPending                atomic.Bool
	cuesFired                         map[*gamedef.Cue]bool
	endPhaseIndex                     int     // The phase that was running when the match stopped.
	matchEndTimeSec                   float64 // The match time at which the match stopped.
	liveErrorLogged                   bool
	expressionErrorsLogged            map[*gamedef.Expr]bool
	lastStationStatuses               map[string]stationStatus
	lastDsPacketTime                  time.Time
	lastTeamLogTime                   time.Time
	lastPeriodicTaskTime              time.Time
	EventStatus                       EventStatus
	FieldVolunteers                   bool
	FieldReset                        bool
	AudienceDisplayMode               string
	SavedMatch                        *model.Match
	SavedMatchResult                  *model.MatchResult
	SavedRankings                     []model.Ranking
	AllianceStationDisplayMode        string
	AllianceSelectionAlliances        []model.Alliance
	AllianceSelectionRankedTeams      []model.AllianceSelectionRankedTeam
	AllianceSelectionShowTimer        bool
	AllianceSelectionTimeRemainingSec int
	PlayoffTournament                 *playoff.PlayoffTournament
	LowerThird                        *model.LowerThird
	ShowLowerThird                    bool
	MuteMatchSounds                   bool
	matchAborted                      bool
	matchStopTime                     time.Time
	breakDescription                  string
	breakNextMatchName                string
	preloadedTeams                    *[6]*model.Team
	DriverStationUdpSocket            *net.UDPConn
}

// stationStatus is what the event bus is told about a station; an event is published when any of it changes.
type stationStatus struct {
	driverStation bool
	robot         bool
	radio         bool
	eStop         bool
	aStop         bool
	bypass        bool
}

type AllianceStation struct {
	DsConn       *DriverStationConnection
	TeamMatchLog *TeamMatchLog
	Ethernet     bool
	AStop        bool
	EStop        bool
	Bypass       bool
	Team         *model.Team
	WifiStatus   network.TeamWifiStatus
	aStopReset   bool
	GameData     string
}

// Creates the arena and sets it to its initial state.
func NewArena(dbPath string) (*Arena, error) {
	arena := new(Arena)
	arena.Bus = bus.New(busRingSize)
	arena.Bus.SetContext(arena.busContext)
	arena.configureNotifiers()

	// Load the game definitions, first copying the built-in examples into place if there are none yet.
	arena.Games = gamedef.NewRegistry(filepath.Join(model.BaseDir, gamesDir))
	if err := arena.Games.Seed(games.FS); err != nil {
		return nil, fmt.Errorf("failed to create the games directory: %w", err)
	}
	if err := arena.Games.Load(); err != nil {
		return nil, fmt.Errorf("failed to load games: %w", err)
	}
	arena.Plc = new(plc.ModbusPlc)

	arena.AllianceStations = make(map[string]*AllianceStation)
	arena.lastStationStatuses = make(map[string]stationStatus)
	for _, stationIds := range fieldStationIds {
		for _, stationId := range stationIds {
			arena.AllianceStations[stationId] = new(AllianceStation)
			arena.lastStationStatuses[stationId] = stationStatus{}
		}
	}
	arena.expressionErrorsLogged = make(map[*gamedef.Expr]bool)

	arena.Displays = make(map[string]*Display)

	arena.TeamSigns = NewTeamSigns()

	var err error
	arena.Database, err = model.OpenDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	err = arena.LoadSettings()
	if err != nil {
		// Without a game to play there is no arena; do not leave the database locked behind it.
		arena.Database.Close()
		return nil, err
	}

	arena.ScoringPanelRegistry.initialize()

	// Load empty match as current.
	arena.MatchState = PreMatch
	arena.PhaseIndex = -1
	arena.endPhaseIndex = -1
	arena.LoadTestMatch()
	arena.LastMatchTimeSec = 0
	arena.lastMatchState = -1
	arena.lastPhaseIndex = -1

	// Initialize display parameters. Until a match is committed, the final score screen shows an empty result,
	// which like any result needs its game stored to be computed.
	arena.AudienceDisplayMode = "blank"
	arena.SavedMatch = &model.Match{}
	arena.SavedMatchResult = model.NewMatchResult(arena.Game)
	if err = arena.Database.SaveGameSnapshot(arena.Game); err != nil {
		arena.Database.Close()
		return nil, err
	}
	arena.AllianceStationDisplayMode = "match"

	return arena, nil
}

// Loads or reloads the event settings upon initial setup or change.
func (arena *Arena) LoadSettings() error {
	settings, err := arena.Database.GetEventSettings()
	if err != nil {
		return err
	}
	arena.EventSettings = settings

	// Initialize the components that depend on settings.
	arena.TeamSigns.Red1.SetId(settings.TeamSignRed1Id)
	arena.TeamSigns.Red2.SetId(settings.TeamSignRed2Id)
	arena.TeamSigns.Red3.SetId(settings.TeamSignRed3Id)
	arena.TeamSigns.RedTimer.SetId(settings.TeamSignRedTimerId)
	arena.TeamSigns.Blue1.SetId(settings.TeamSignBlue1Id)
	arena.TeamSigns.Blue2.SetId(settings.TeamSignBlue2Id)
	arena.TeamSigns.Blue3.SetId(settings.TeamSignBlue3Id)
	arena.TeamSigns.BlueTimer.SetId(settings.TeamSignBlueTimerId)
	accessPointWifiStatuses := [6]*network.TeamWifiStatus{
		&arena.AllianceStations["R1"].WifiStatus,
		&arena.AllianceStations["R2"].WifiStatus,
		&arena.AllianceStations["R3"].WifiStatus,
		&arena.AllianceStations["B1"].WifiStatus,
		&arena.AllianceStations["B2"].WifiStatus,
		&arena.AllianceStations["B3"].WifiStatus,
	}
	arena.accessPoint.SetSettings(
		settings.ApAddress,
		settings.ApPassword,
		settings.ApChannel,
		settings.NetworkSecurityEnabled,
		accessPointWifiStatuses,
	)
	arena.networkSwitch = network.NewSwitch(settings.SwitchAddress, settings.SwitchPassword)
	sccUpCommands := strings.Split(settings.SCCUpCommands, "\n")
	sccDownCommands := strings.Split(settings.SCCDownCommands, "\n")
	arena.redSCC = network.NewSCCSwitch(
		settings.RedSCCAddress,
		settings.SCCUsername,
		settings.SCCPassword,
		sccUpCommands,
		sccDownCommands,
	)
	arena.blueSCC = network.NewSCCSwitch(
		settings.BlueSCCAddress,
		settings.SCCUsername,
		settings.SCCPassword,
		sccUpCommands,
		sccDownCommands,
	)
	arena.Plc.SetAddress(settings.PlcAddress)
	arena.TbaClient = partner.NewTbaClient(settings.TbaEventCode, settings.TbaSecretId, settings.TbaSecret)
	arena.NexusClient = partner.NewNexusClient(settings.TbaEventCode, settings.NexusAutoQueueKey)
	arena.BlackmagicClient = partner.NewBlackmagicClient(settings.BlackmagicAddresses)

	// Initialize Companion client with event configurations
	companionEventConfigs := map[partner.CompanionEvent]partner.CompanionEventConfig{
		partner.EventMatchPreview: {
			Page:   settings.CompanionMatchPreviewPage,
			Row:    settings.CompanionMatchPreviewRow,
			Column: settings.CompanionMatchPreviewColumn,
		},
		partner.EventShowOverlay: {
			Page:   settings.CompanionSetAudiencePage,
			Row:    settings.CompanionSetAudienceRow,
			Column: settings.CompanionSetAudienceColumn,
		},
		partner.EventMatchStart: {
			Page:   settings.CompanionMatchStartPage,
			Row:    settings.CompanionMatchStartRow,
			Column: settings.CompanionMatchStartColumn,
		},
		partner.EventTeleopStart: {
			Page:   settings.CompanionTeleopStartPage,
			Row:    settings.CompanionTeleopStartRow,
			Column: settings.CompanionTeleopStartColumn,
		},
		partner.EventEndgameStart: {
			Page:   settings.CompanionEndgameStartPage,
			Row:    settings.CompanionEndgameStartRow,
			Column: settings.CompanionEndgameStartColumn,
		},
		partner.EventMatchEnd: {
			Page:   settings.CompanionMatchEndPage,
			Row:    settings.CompanionMatchEndRow,
			Column: settings.CompanionMatchEndColumn,
		},
		partner.EventShowFinalScore: {
			Page:   settings.CompanionPostResultPage,
			Row:    settings.CompanionPostResultRow,
			Column: settings.CompanionPostResultColumn,
		},
		partner.EventAllianceSelection: {
			Page:   settings.CompanionAllianceSelectionPage,
			Row:    settings.CompanionAllianceSelectionRow,
			Column: settings.CompanionAllianceSelectionColumn,
		},
		partner.EventMatchAbort: {
			Page:   settings.CompanionMatchAbortPage,
			Row:    settings.CompanionMatchAbortRow,
			Column: settings.CompanionMatchAbortColumn,
		},
	}
	arena.CompanionClient = partner.NewCompanionClient(
		settings.CompanionAddress,
		settings.CompanionPort,
		companionEventConfigs,
	)

	// Take the selected game and its parameters into use, or note that it has to wait for the match in progress.
	// A selection that cannot be used is reported only after everything else is set up, so that the rest of the
	// settings still take effect; the arena keeps the game it has.
	gameErr := arena.refreshGame()
	if gameErr != nil && arena.Game == nil {
		return gameErr
	}

	// Reconstruct the playoff tournament in memory.
	if err = arena.CreatePlayoffTournament(); err != nil {
		return err
	}
	if err = arena.UpdatePlayoffTournament(); err != nil {
		return err
	}

	return gameErr
}

// Constructs an empty playoff tournament in memory, based only on the number of alliances.
func (arena *Arena) CreatePlayoffTournament() error {
	var err error
	arena.PlayoffTournament, err = playoff.NewPlayoffTournament(
		arena.EventSettings.PlayoffType, arena.EventSettings.NumPlayoffAlliances,
	)
	return err
}

// Performs the one-time creation of all matches for the playoff tournament.
func (arena *Arena) CreatePlayoffMatches(startTime time.Time) error {
	return arena.PlayoffTournament.CreateMatchesAndBreaks(arena.Database, startTime)
}

// Traverses the playoff tournament rounds to assess winners and populate subsequent matches.
func (arena *Arena) UpdatePlayoffTournament() error {
	alliances, err := arena.Database.GetAllAlliances()
	if err != nil {
		return err
	}
	if len(alliances) > 0 {
		return arena.PlayoffTournament.UpdateMatches(arena.Database)
	}
	return nil
}

// Sets up the arena for the given match.
func (arena *Arena) LoadMatch(match *model.Match) error {
	if arena.MatchState != PreMatch && arena.MatchState != TimeoutActive {
		return fmt.Errorf("cannot load match while there is a match still in progress or with results pending")
	}
	if arena.MatchState == PreMatch {
		arena.applyPendingGame()
	}

	arena.CurrentMatch = match

	loadedByNexus := false
	if match.ShouldAllowNexusSubstitution() && arena.EventSettings.NexusEnabled {
		// Attempt to get the match lineup from Nexus for FRC.
		lineup, err := arena.NexusClient.GetLineup(match.TbaMatchKey)
		if err != nil {
			log.Printf("Failed to load lineup from Nexus: %s", err.Error())
		} else {
			err = arena.SubstituteTeams(lineup[0], lineup[1], lineup[2], lineup[3], lineup[4], lineup[5])
			if err != nil {
				log.Printf("Failed to substitute teams using Nexus lineup; loading match normally: %s", err.Error())
			} else {
				log.Printf(
					"Successfully loaded lineup for match %s from Nexus: %v", match.TbaMatchKey.String(), *lineup,
				)
				loadedByNexus = true
			}
		}
	}

	if !loadedByNexus {
		err := arena.assignTeam(match.Red1, "R1")
		if err != nil {
			return err
		}
		err = arena.assignTeam(match.Red2, "R2")
		if err != nil {
			return err
		}
		err = arena.assignTeam(match.Red3, "R3")
		if err != nil {
			return err
		}
		err = arena.assignTeam(match.Blue1, "B1")
		if err != nil {
			return err
		}
		err = arena.assignTeam(match.Blue2, "B2")
		if err != nil {
			return err
		}
		err = arena.assignTeam(match.Blue3, "B3")
		if err != nil {
			return err
		}

		arena.setupNetwork(
			[6]*model.Team{
				arena.AllianceStations["R1"].Team,
				arena.AllianceStations["R2"].Team,
				arena.AllianceStations["R3"].Team,
				arena.AllianceStations["B1"].Team,
				arena.AllianceStations["B2"].Team,
				arena.AllianceStations["B3"].Team,
			},
			false,
		)
	}

	// Reset the arena state and realtime scores.
	arena.resetScore()
	arena.ScoringPanelRegistry.resetScoreCommitted()
	arena.Plc.ResetMatch()

	// Notify any listeners about the new match.
	arena.publishMatchLoaded()
	arena.MatchLoadNotifier.Notify()
	arena.RealtimeScoreNotifier.Notify()
	arena.AllianceStationDisplayMode = "match"
	arena.AllianceStationDisplayModeNotifier.Notify()
	arena.ScoringStatusNotifier.Notify()

	return nil
}

// Sets a new test match containing no teams as the current match.
func (arena *Arena) LoadTestMatch() error {
	return arena.LoadMatch(&model.Match{Type: model.Test, ShortName: "T", LongName: "Test Match"})
}

// Loads the first unplayed match of the current match type.
func (arena *Arena) LoadNextMatch(startScheduledBreak bool) error {
	nextMatch, err := arena.getNextMatch(false)
	if err != nil {
		return err
	}
	if nextMatch == nil {
		return arena.LoadTestMatch()
	}
	err = arena.LoadMatch(nextMatch)
	if err != nil {
		return err
	}

	// Start the timeout timer if there is a scheduled break before this match.
	if startScheduledBreak {
		scheduledBreak, err := arena.Database.GetScheduledBreakByMatchTypeOrder(nextMatch.Type, nextMatch.TypeOrder)
		if err != nil {
			return err
		}
		if scheduledBreak != nil {
			go func() {
				time.Sleep(time.Second * scheduledBreakDelaySec)
				if err := arena.StartTimeout(scheduledBreak.Description, scheduledBreak.DurationSec); err != nil {
					log.Printf("Failed to start scheduled break timeout before match %d: %v", nextMatch.Id, err)
				}
			}()
		}
	}

	return nil
}

// Assigns the given team to the given station, also substituting it into the match record.
func (arena *Arena) SubstituteTeams(red1, red2, red3, blue1, blue2, blue3 int) error {
	if !arena.CurrentMatch.ShouldAllowSubstitution() {
		return fmt.Errorf("Can't substitute teams for qualification matches.")
	}

	if err := arena.validateTeams(red1, red2, red3, blue1, blue2, blue3); err != nil {
		return err
	}
	if err := arena.assignTeam(red1, "R1"); err != nil {
		return err
	}
	if err := arena.assignTeam(red2, "R2"); err != nil {
		return err
	}
	if err := arena.assignTeam(red3, "R3"); err != nil {
		return err
	}
	if err := arena.assignTeam(blue1, "B1"); err != nil {
		return err
	}
	if err := arena.assignTeam(blue2, "B2"); err != nil {
		return err
	}
	if err := arena.assignTeam(blue3, "B3"); err != nil {
		return err
	}

	arena.CurrentMatch.Red1 = red1
	arena.CurrentMatch.Red2 = red2
	arena.CurrentMatch.Red3 = red3
	arena.CurrentMatch.Blue1 = blue1
	arena.CurrentMatch.Blue2 = blue2
	arena.CurrentMatch.Blue3 = blue3
	arena.setupNetwork(
		[6]*model.Team{
			arena.AllianceStations["R1"].Team,
			arena.AllianceStations["R2"].Team,
			arena.AllianceStations["R3"].Team,
			arena.AllianceStations["B1"].Team,
			arena.AllianceStations["B2"].Team,
			arena.AllianceStations["B3"].Team,
		},
		false,
	)
	arena.publishMatchLoaded()
	arena.MatchLoadNotifier.Notify()

	if arena.CurrentMatch.Type != model.Test {
		arena.Database.UpdateMatch(arena.CurrentMatch)
	}
	return nil
}

// publishMatchLoaded announces on the event bus which match is on the field and who plays in it.
func (arena *Arena) publishMatchLoaded() {
	teams := make(map[string]int, len(arena.AllianceStations))
	for stationId, allianceStation := range arena.AllianceStations {
		if allianceStation.Team != nil {
			teams[stationId] = allianceStation.Team.Id
		} else {
			teams[stationId] = 0
		}
	}
	arena.Bus.Publish(
		"match.loaded",
		map[string]any{
			"id": arena.CurrentMatch.Id, "type": arena.CurrentMatch.Type.String(),
			"shortName": arena.CurrentMatch.ShortName, "teams": teams,
		},
	)
}

// Starts the match if all conditions are met.
func (arena *Arena) StartMatch() error {
	err := arena.checkCanStartMatch()
	if err == nil {
		// Save the match start time to the database for posterity.
		arena.CurrentMatch.StartedAt = time.Now()
		if arena.CurrentMatch.Type != model.Test {
			arena.Database.UpdateMatch(arena.CurrentMatch)
		}
		arena.updateCycleTime(arena.CurrentMatch.StartedAt)

		for _, allianceStation := range arena.AllianceStations {
			if allianceStation.Team != nil {
				err = allianceStation.startTeamMatchLog(arena.CurrentMatch)
				if err != nil {
					log.Println(err)
				}
			}
			if allianceStation.DsConn != nil {
				allianceStation.DsConn.MissedPacketCount = 0
			}

			// Save the teams that have successfully connected to the field.
			if allianceStation.Team != nil && !allianceStation.Team.HasConnected && allianceStation.DsConn != nil &&
				allianceStation.DsConn.RobotLinked {
				allianceStation.Team.HasConnected = true
				arena.Database.UpdateTeam(allianceStation.Team)
			}
		}

		arena.lastTeamLogTime = time.Time{}

		arena.MatchState = StartMatch

		if arena.EventSettings.NexusAutoQueueEnabled && arena.CurrentMatch.Type != model.Test {
			go arena.NexusClient.MatchStarted(arena.CurrentMatch.LongName, arena.CurrentMatch.TypeOrder)
		}
	}
	return err
}

// Kills the current match or timeout if it is underway.
func (arena *Arena) AbortMatch() error {
	if arena.MatchState == PreMatch || arena.MatchState == PostMatch || arena.MatchState == PostTimeout {
		return fmt.Errorf("cannot abort match when it is not in progress")
	}

	if arena.MatchState == TimeoutActive {
		// Handle by advancing the timeout clock to the end and letting the regular logic deal with it.
		arena.MatchStartTime = time.Now().Add(-time.Second * time.Duration(arena.TimeoutDurationSec))
		return nil
	}

	arena.PlaySound("abort")
	arena.endMatch(true)
	arena.SetAudienceDisplayMode("blank")
	go arena.BlackmagicClient.StopRecording()
	go arena.CompanionClient.SendEvent(partner.EventMatchAbort)
	return nil
}

// Clears out the match and resets the arena state unless there is a match underway.
func (arena *Arena) ResetMatch() error {
	if arena.MatchState != PostMatch && arena.MatchState != PreMatch && arena.MatchState != TimeoutActive {
		return fmt.Errorf("cannot reset match while it is in progress")
	}
	if arena.MatchState != TimeoutActive {
		arena.MatchState = PreMatch
	}
	arena.closeTeamMatchLogs()
	arena.matchAborted = false
	for _, allianceStation := range arena.AllianceStations {
		allianceStation.Bypass = false
	}
	arena.MuteMatchSounds = false
	return nil
}

// Starts a timeout of the given duration.
func (arena *Arena) StartTimeout(description string, durationSec int) error {
	return arena.startTimeout(description, arena.currentMatchDisplayName(), durationSec)
}

// Starts an ad-hoc timeout of the given duration.
func (arena *Arena) StartAdHocTimeout(description string, nextMatchName string, durationSec int) error {
	return arena.startTimeout(description, nextMatchName, durationSec)
}

// Sets the text shown on timeout displays.
func (arena *Arena) SetTimeoutDisplay(description string, nextMatchName string) {
	arena.breakDescription = description
	arena.breakNextMatchName = nextMatchName
	arena.MatchLoadNotifier.Notify()
}

func (arena *Arena) startTimeout(description string, nextMatchName string, durationSec int) error {
	if arena.MatchState != PreMatch {
		return fmt.Errorf("cannot start timeout while there is a match still in progress or with results pending")
	}

	arena.TimeoutDurationSec = durationSec
	arena.breakDescription = description
	arena.breakNextMatchName = nextMatchName
	arena.MatchLoadNotifier.Notify()
	arena.MatchState = TimeoutActive
	arena.MatchStartTime = time.Now()
	arena.LastMatchTimeSec = -1
	arena.AllianceStationDisplayMode = "timeout"
	arena.AllianceStationDisplayModeNotifier.Notify()

	if arena.EventSettings.NexusAutoQueueEnabled {
		go arena.NexusClient.BreakStarted(durationSec)
	}

	return nil
}

func (arena *Arena) currentMatchDisplayName() string {
	if arena.CurrentMatch == nil {
		return ""
	}
	name := arena.CurrentMatch.LongName
	if arena.CurrentMatch.NameDetail != "" {
		name += " - " + arena.CurrentMatch.NameDetail
	}
	return name
}

// Updates the audience display screen.
func (arena *Arena) SetAudienceDisplayMode(mode string) {
	if arena.AudienceDisplayMode != mode {
		arena.AudienceDisplayMode = mode
		arena.AudienceDisplayModeNotifier.Notify()
		if mode == "score" {
			arena.PlaySound("match_result")
			go arena.CompanionClient.SendEvent(partner.EventShowFinalScore)
		} else if mode == "allianceSelection" {
			go arena.CompanionClient.SendEvent(partner.EventAllianceSelection)
		} else if mode == "intro" {
			go arena.CompanionClient.SendEvent(partner.EventMatchPreview)
		} else if mode == "match" {
			go arena.CompanionClient.SendEvent(partner.EventShowOverlay)
		}
	}
}

// Updates the alliance station display screen.
func (arena *Arena) SetAllianceStationDisplayMode(mode string) {
	if arena.AllianceStationDisplayMode != mode {
		arena.AllianceStationDisplayMode = mode
		arena.AllianceStationDisplayModeNotifier.Notify()
	}
}

// Performs a single iteration of checking inputs and timers and setting outputs accordingly to control the
// flow of a match.
func (arena *Arena) Update() {
	if arena.MatchState == PreMatch {
		arena.applyPendingGame()
	}

	// Work out where in the match we are.
	sendDsPacket := false
	matchTimeSec := arena.MatchTimeSec()
	switch arena.MatchState {
	case StartMatch:
		arena.SetAudienceDisplayMode("match")
		arena.SetAllianceStationDisplayMode("match")
		go arena.BlackmagicClient.StartRecording()
		go arena.CompanionClient.SendEvent(partner.EventMatchStart)
		arena.Plc.ResetMatch()
		arena.FieldVolunteers = false
		arena.FieldReset = false
		arena.beginMatch()
		matchTimeSec = 0
		sendDsPacket = true
	case InMatch:
		if arena.advancePhases(matchTimeSec) {
			sendDsPacket = true
		}
		if arena.MatchState == PostMatch {
			go arena.BlackmagicClient.StopRecording()
			go arena.CompanionClient.SendEvent(partner.EventMatchEnd)
			go func() {
				// Leave the scores on the screen briefly at the end of the match.
				time.Sleep(time.Second * matchEndScoreDwellSec)
				arena.SetAudienceDisplayMode("blank")
			}()
			go func() {
				// Configure the network in advance for the next match after a delay.
				time.Sleep(time.Second * preLoadNextMatchDelaySec)
				arena.preLoadNextMatch()
			}()
		}
	case TimeoutActive:
		if matchTimeSec >= float64(arena.TimeoutDurationSec) {
			arena.MatchState = PostTimeout

			if arena.EventSettings.NexusAutoQueueEnabled {
				go arena.NexusClient.BreakEnded()
			}

			go func() {
				// Leave the timer on the screen briefly at the end of the timeout period.
				time.Sleep(time.Second * matchEndScoreDwellSec)
				arena.SetAudienceDisplayMode("intro")
				arena.SetAllianceStationDisplayMode("match")
			}()
		}
	case PostTimeout:
		if matchTimeSec >= float64(arena.TimeoutDurationSec+postTimeoutSec) {
			arena.MatchState = PreMatch
		}
	}

	// Send a match tick notification if passing an integer second threshold or if the match state or phase changed.
	if int(matchTimeSec) != int(arena.LastMatchTimeSec) || arena.MatchState != arena.lastMatchState ||
		arena.PhaseIndex != arena.lastPhaseIndex {
		arena.MatchTimeNotifier.Notify()
	}

	// Send a packet if at a period transition point or if it's been long enough since the last one.
	msSinceLastDsPacket := int(time.Since(arena.lastDsPacketTime).Seconds() * 1000)
	if sendDsPacket || msSinceLastDsPacket >= dsPacketPeriodMs {
		if msSinceLastDsPacket >= dsPacketWarningMs && arena.lastDsPacketTime.After(time.Time{}) {
			log.Printf("Warning: Long time since last driver station packet: %dms", msSinceLastDsPacket)
		}
		auto, enabled := arena.robotMode()
		arena.sendDsPacket(auto, enabled)
		arena.ArenaStatusNotifier.Notify()
	}

	// Handle field sensors/lights/actuators.
	arena.handlePlcInputOutput()

	// Log after PLC input so each sample includes the latest physical DS Ethernet state.
	arena.logTeamSnapshots()

	// Let the game's clock-dependent values catch up, after the PLC so that a field e-stop abort is reflected in the
	// same cycle.
	arena.updateLiveValues()

	arena.publishStationStatuses()

	// Handle the team number / timer displays.
	arena.TeamSigns.Update(arena)

	arena.LastMatchTimeSec = matchTimeSec
	arena.lastMatchState = arena.MatchState
	arena.lastPhaseIndex = arena.PhaseIndex
}

// publishStationStatuses tells the event bus about each station whose driver station, robot or radio link, stops
// or bypass changed since the last time around.
func (arena *Arena) publishStationStatuses() {
	for _, stationIds := range fieldStationIds {
		for _, stationId := range stationIds {
			allianceStation := arena.AllianceStations[stationId]
			status := stationStatus{
				eStop: allianceStation.EStop, aStop: allianceStation.AStop, bypass: allianceStation.Bypass,
			}
			if dsConn := allianceStation.DsConn; dsConn != nil {
				status.driverStation, status.robot, status.radio = dsConn.DsLinked, dsConn.RobotLinked, dsConn.RadioLinked
			}
			last := arena.lastStationStatuses[stationId]
			if status == last {
				continue
			}
			arena.lastStationStatuses[stationId] = status

			var changed []string
			for _, field := range []struct {
				name     string
				old, new bool
			}{
				{"driverStation", last.driverStation, status.driverStation},
				{"robot", last.robot, status.robot},
				{"radio", last.radio, status.radio},
				{"eStop", last.eStop, status.eStop},
				{"aStop", last.aStop, status.aStop},
				{"bypass", last.bypass, status.bypass},
			} {
				if field.old != field.new {
					changed = append(changed, field.name)
				}
			}
			team := 0
			if allianceStation.Team != nil {
				team = allianceStation.Team.Id
			}
			arena.Bus.Publish(
				"station.status",
				map[string]any{
					"station": stationId, "team": team, "changed": changed, "driverStation": status.driverStation,
					"robot": status.robot, "radio": status.radio, "eStop": status.eStop, "aStop": status.aStop,
					"bypass": status.bypass,
				},
			)
		}
	}
}

// logTeamSnapshots records one row per station-owned team log at the configured cadence while a match is active.
func (arena *Arena) logTeamSnapshots() {
	matchTimeSec := arena.MatchTimeSec()
	if matchTimeSec <= 0 {
		return
	}

	msSinceLastTeamLog := int(time.Since(arena.lastTeamLogTime).Seconds() * 1000)
	if arena.lastTeamLogTime.After(time.Time{}) && msSinceLastTeamLog < teamLogPeriodMs {
		return
	}

	// Logs are owned by stations, not DS TCP connections, so disconnected stations still get outage rows.
	for stationId, allianceStation := range arena.AllianceStations {
		if allianceStation.TeamMatchLog != nil {
			allianceStation.TeamMatchLog.LogStationSnapshot(matchTimeSec, allianceStation, stationId)
		}
	}
	arena.lastTeamLogTime = time.Now()
}

// busContext supplies the match that events published on the bus belong to.
func (arena *Arena) busContext() (match, run string) {
	if arena.CurrentMatch != nil && arena.MatchState != PreMatch {
		return arena.CurrentMatch.ShortName, ""
	}
	return "", ""
}

// Loops indefinitely to track and update the arena components.
func (arena *Arena) Run() {
	// Bind the shared driver station UDP socket before any loop sends control packets from it.
	arena.initializeUdpListener()

	// Start other loops in goroutines.
	go arena.listenForDriverStations()
	go arena.listenForDsUdpPackets()
	go arena.accessPoint.Run()
	go arena.Plc.Run()
	go arena.watchGames()

	for {
		loopStartTime := time.Now()
		arena.Update()
		if time.Since(loopStartTime).Milliseconds() > arenaLoopWarningMs {
			log.Printf("Warning: Arena loop iteration took a long time: %dms", time.Since(loopStartTime).Milliseconds())
		}
		if time.Since(arena.lastPeriodicTaskTime).Seconds() >= periodicTaskPeriodSec {
			arena.lastPeriodicTaskTime = time.Now()
			go arena.runPeriodicTasks()
		}

		time.Sleep(time.Millisecond * arenaLoopPeriodMs)
	}
}

// Checks that the given teams are present in the database, allowing team ID 0 which indicates an empty spot.
func (arena *Arena) validateTeams(teamIds ...int) error {
	for _, teamId := range teamIds {
		if teamId == 0 {
			continue
		}
		team, err := arena.Database.GetTeamById(teamId)
		if err != nil {
			return err
		}
		if team == nil {
			return fmt.Errorf("Team %d is not present at the event.", teamId)
		}
	}
	return nil
}

// startTeamMatchLog opens a fresh persisted log for the station's assigned team at match start.
func (allianceStation *AllianceStation) startTeamMatchLog(match *model.Match) error {
	if allianceStation.TeamMatchLog != nil {
		// A previous log can exist after an aborted start or replay; close it before replacing the handle.
		allianceStation.TeamMatchLog.Close()
		allianceStation.TeamMatchLog = nil
	}
	if allianceStation.Team == nil {
		return nil
	}

	teamMatchLog, err := NewTeamMatchLog(allianceStation.Team.Id, match, &allianceStation.WifiStatus)
	if err != nil {
		return err
	}
	allianceStation.TeamMatchLog = teamMatchLog
	return nil
}

// closeTeamMatchLog closes the station-owned match log, if one is active.
func (allianceStation *AllianceStation) closeTeamMatchLog() {
	if allianceStation.TeamMatchLog != nil {
		allianceStation.TeamMatchLog.Close()
		allianceStation.TeamMatchLog = nil
	}
}

// closeTeamMatchLogs closes all station-owned match logs when match ownership is ending.
func (arena *Arena) closeTeamMatchLogs() {
	for _, allianceStation := range arena.AllianceStations {
		allianceStation.closeTeamMatchLog()
	}
}

// Loads a team into an alliance station, cleaning up the previous team there if there is one.
func (arena *Arena) assignTeam(teamId int, station string) error {
	// Reject invalid station values.
	if _, ok := arena.AllianceStations[station]; !ok {
		return fmt.Errorf("Invalid alliance station '%s'.", station)
	}

	// Force the A-stop to be reset by the new team if it is already pressed (if the PLC is enabled).
	arena.AllianceStations[station].aStopReset = !arena.Plc.IsEnabled()

	// Do nothing if the station is already assigned to the requested team.
	dsConn := arena.AllianceStations[station].DsConn
	if dsConn != nil && dsConn.TeamId == teamId {
		return nil
	}
	if dsConn != nil {
		dsConn.close()
		arena.AllianceStations[station].Team = nil
		arena.AllianceStations[station].DsConn = nil
	}
	arena.AllianceStations[station].closeTeamMatchLog()

	// Leave the station empty if the team number is zero.
	if teamId == 0 {
		arena.AllianceStations[station].Team = nil
		return nil
	}

	// Load the team model. If it doesn't exist, enable anonymous operation.
	team, err := arena.Database.GetTeamById(teamId)
	if err != nil {
		return err
	}
	if team == nil {
		team = &model.Team{Id: teamId}
	}

	arena.AllianceStations[station].Team = team
	return nil
}

// Returns the next match of the same type that is currently loaded, or nil if there are no more matches.
func (arena *Arena) getNextMatch(excludeCurrent bool) (*model.Match, error) {
	if arena.CurrentMatch.Type == model.Test {
		return nil, nil
	}

	matches, err := arena.Database.GetMatchesByType(arena.CurrentMatch.Type, false)
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		if !match.IsComplete() && !(excludeCurrent && match.Id == arena.CurrentMatch.Id) {
			return &match, nil
		}
	}

	// There are no matches left of the same type.
	return nil, nil
}

// Configures the field network for the next match in advance of the current match being scored and committed.
func (arena *Arena) preLoadNextMatch() {
	if arena.MatchState != PostMatch {
		// The next match has already been loaded; no need to do anything.
		return
	}

	nextMatch, err := arena.getNextMatch(true)
	if err != nil {
		log.Printf("Failed to pre-load next match: %s", err.Error())
	}
	if nextMatch == nil {
		return
	}

	teamIds := [6]int{nextMatch.Red1, nextMatch.Red2, nextMatch.Red3, nextMatch.Blue1, nextMatch.Blue2, nextMatch.Blue3}
	if nextMatch.ShouldAllowNexusSubstitution() && arena.EventSettings.NexusEnabled {
		// Attempt to get the match lineup from Nexus for FRC.
		lineup, err := arena.NexusClient.GetLineup(nextMatch.TbaMatchKey)
		if err != nil {
			log.Printf("Failed to load lineup from Nexus: %s", err.Error())
		} else {
			teamIds = *lineup
		}
	}

	var teams [6]*model.Team
	for i, teamId := range teamIds {
		if teamId == 0 {
			continue
		}
		if teams[i], err = arena.Database.GetTeamById(teamId); err != nil {
			log.Printf("Failed to get model for Team %d while pre-loading next match: %s", teamId, err.Error())
		}
	}
	arena.setupNetwork(teams, true)
	arena.TeamSigns.SetNextMatchTeams(teamIds)
}

// Enable or disable the team ethernet ports on both SCCs
func (arena *Arena) setSCCEthernetEnabled(enabled bool) {
	if arena.EventSettings.SCCManagementEnabled {
		var wg sync.WaitGroup
		wg.Add(2)

		configureSCC := func(scc *network.SCCSwitch, name string) {
			defer wg.Done()
			err := scc.SetTeamEthernetEnabled(enabled)
			if err != nil {
				log.Printf("Failed to set %s SCC enabled state to %t: %s", name, enabled, err.Error())
			}
		}
		go configureSCC(arena.redSCC, "red")
		go configureSCC(arena.blueSCC, "blue")
		wg.Wait()
	}
}

// Asynchronously reconfigures the networking hardware for the new set of teams.
func (arena *Arena) setupNetwork(teams [6]*model.Team, isPreload bool) {
	if isPreload {
		arena.preloadedTeams = &teams
	} else if arena.preloadedTeams != nil {
		preloadedTeams := *arena.preloadedTeams
		arena.preloadedTeams = nil
		if reflect.DeepEqual(teams, preloadedTeams) {
			// Skip configuring the network; this is the same set of teams that was preloaded.
			return
		}
	}

	if arena.EventSettings.NetworkSecurityEnabled {
		if err := arena.accessPoint.ConfigureTeamWifi(teams); err != nil {
			log.Printf("Failed to configure team WiFi: %s", err.Error())
		}
		if arena.switchConfigEnabled() {
			go func() {
				arena.setSCCEthernetEnabled(false)
				if err := arena.networkSwitch.ConfigureTeamEthernet(teams); err != nil {
					log.Printf("Failed to configure team Ethernet: %s", err.Error())
				}
				arena.setSCCEthernetEnabled(true)
			}()
		}
	}
}

// Returns true if the managed switch should be reconfigured for each match's teams. In radio-only mode the field
// radio is still configured but there is no managed switch to give each team its own VLAN and address range.
func (arena *Arena) switchConfigEnabled() bool {
	return arena.EventSettings.NetworkSecurityEnabled && !arena.EventSettings.RadioOnlyNetwork
}

// Returns nil if the match can be started, and an error otherwise.
func (arena *Arena) checkCanStartMatch() error {
	conditions := arena.getStartMatchConditions()
	if len(conditions) > 0 {
		return fmt.Errorf("cannot start match: %s", strings.Join(conditions, "; "))
	}
	return nil
}

// Returns descriptions of all conditions preventing the match from being started.
func (arena *Arena) getStartMatchConditions() []string {
	var conditions []string
	if arena.MatchState != PreMatch {
		conditions = append(
			conditions,
			"a match is still in progress or has results pending",
		)
	}

	conditions = append(conditions, arena.getAllianceStationStartConditions(arena.activeStationIds()...)...)

	if arena.Plc.IsEnabled() {
		if !arena.Plc.IsHealthy() {
			conditions = append(conditions, "PLC is not healthy")
		}
		if arena.Plc.GetFieldEStop() {
			conditions = append(conditions, "field emergency stop is active")
		}
		if !arena.Plc.IsFtaReady() {
			conditions = append(conditions, "FTA ready switch is not active")
		}
		var disconnectedArmorBlocks []string
		for name, status := range arena.Plc.GetArmorBlockStatuses() {
			if !status {
				disconnectedArmorBlocks = append(disconnectedArmorBlocks, name)
			}
		}
		sort.Strings(disconnectedArmorBlocks)
		for _, name := range disconnectedArmorBlocks {
			conditions = append(
				conditions,
				fmt.Sprintf("PLC ArmorBlock %q is not connected", name),
			)
		}
	}

	return conditions
}

func (arena *Arena) checkAllianceStationsReady(stations ...string) error {
	conditions := arena.getAllianceStationStartConditions(stations...)
	if len(conditions) > 0 {
		return fmt.Errorf("cannot start match: %s", strings.Join(conditions, "; "))
	}
	return nil
}

func (arena *Arena) getAllianceStationStartConditions(stations ...string) []string {
	var eStoppedStations, aStopNotResetStations, disconnectedStations []string
	for _, station := range stations {
		allianceStation := arena.AllianceStations[station]
		if allianceStation.EStop {
			eStoppedStations = append(eStoppedStations, station)
		}
		if !allianceStation.aStopReset {
			aStopNotResetStations = append(aStopNotResetStations, station)
		}
		if !allianceStation.Bypass {
			if allianceStation.DsConn == nil || !allianceStation.DsConn.RobotLinked {
				disconnectedStations = append(disconnectedStations, station)
			}
		}
	}

	var conditions []string
	if len(eStoppedStations) > 0 {
		conditions = append(
			conditions,
			fmt.Sprintf("an emergency stop is active (%s)", strings.Join(eStoppedStations, ", ")),
		)
	}
	if len(aStopNotResetStations) > 0 {
		conditions = append(
			conditions,
			fmt.Sprintf(
				"an autonomous stop has not been reset since the previous match (%s)",
				strings.Join(aStopNotResetStations, ", "),
			),
		)
	}
	if len(disconnectedStations) > 0 {
		conditions = append(
			conditions,
			fmt.Sprintf(
				"not all robots are connected or bypassed (%s)",
				strings.Join(disconnectedStations, ", "),
			),
		)
	}
	return conditions
}

func (arena *Arena) sendDsPacket(auto bool, enabled bool) {
	for stationId, allianceStation := range arena.AllianceStations {
		allianceStation.GameData = arena.stationGameData(stationId)
		dsConn := allianceStation.DsConn
		if dsConn != nil {
			dsConn.Auto = auto
			dsConn.Enabled = enabled && !allianceStation.EStop && !(auto && allianceStation.AStop) &&
				!allianceStation.Bypass
			dsConn.EStop = allianceStation.EStop
			dsConn.AStop = allianceStation.AStop
			err := dsConn.update(arena, allianceStation.GameData)
			if err != nil {
				log.Printf("Unable to send driver station packet for team %d.", allianceStation.Team.Id)
			}
		}
	}
	arena.lastDsPacketTime = time.Now()
}

// Returns the alliance station identifier for the given team, or the empty string if the team is not present
// in the current match.
func (arena *Arena) getAssignedAllianceStation(teamId int) string {
	for station, allianceStation := range arena.AllianceStations {
		if allianceStation.Team != nil && allianceStation.Team.Id == teamId {
			return station
		}
	}

	return ""
}

// stationGameData returns the game-specific string for the driver station at the given station: whatever the
// game's ds_game_data expression gives for it right now, or nothing if the game has none.
func (arena *Arena) stationGameData(stationId string) string {
	expression := arena.Game.Match.DSGameData
	allianceIndex, position, ok := arena.stationPlace(stationId)
	if expression == nil || !ok {
		return ""
	}
	alliance := arena.Game.Alliances.List[allianceIndex]
	team := 0
	if allianceStation := arena.AllianceStations[stationId]; allianceStation.Team != nil {
		team = allianceStation.Team.Id
	}
	// The expression sees the station under the name the game gives it, which is built from the alliance's short
	// code and need not be the name of the physical station.
	station := map[string]any{
		"id": alliance.Short + strconv.Itoa(position), "alliance": alliance.ID, "position": position, "team": team,
	}
	value, err := arena.evalLive(expression, alliance.ID, map[string]any{"station": station})
	if err != nil {
		arena.logExpressionError(expression, "driver station game data", err)
		return ""
	}
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

// logExpressionError reports an expression of the game that failed, once: the arena evaluates the same ones many
// times a second.
func (arena *Arena) logExpressionError(expression *gamedef.Expr, what string, err error) {
	if !arena.expressionErrorsLogged[expression] {
		arena.expressionErrorsLogged[expression] = true
		log.Printf("Failed to evaluate the %s of game %q (line %d): %v", what, arena.Game.ID, expression.Line, err)
	}
}

// Updates the score given new input information from the field PLC, and actuates PLC outputs accordingly.
func (arena *Arena) handlePlcInputOutput() {
	if !arena.Plc.IsEnabled() {
		return
	}

	// Handle PLC functions that are always active.
	if arena.Plc.GetFieldEStop() && !arena.matchAborted {
		arena.AbortMatch()
	}
	redEStops, blueEStops := arena.Plc.GetTeamEStops()
	redAStops, blueAStops := arena.Plc.GetTeamAStops()
	redEthernets, blueEthernets := arena.Plc.GetEthernetConnected()
	eStops := [fieldAllianceCount][3]bool{redEStops, blueEStops}
	aStops := [fieldAllianceCount][3]bool{redAStops, blueAStops}
	ethernets := [fieldAllianceCount][3]bool{redEthernets, blueEthernets}
	for allianceIndex, stationIds := range fieldStationIds {
		for i, stationId := range stationIds {
			arena.handleTeamStop(stationId, eStops[allianceIndex][i], aStops[allianceIndex][i])
			arena.AllianceStations[stationId].Ethernet = ethernets[allianceIndex][i]
		}
	}
	arena.Plc.SetAwardsModeLight(arena.AllianceStationDisplayMode == "logo")

	// Handle in-match PLC functions. The stack lights have one lamp for each side of the field.
	redAllianceReady := arena.checkAllianceStationsReady(arena.AllianceStationIds(0)...) == nil
	blueAllianceReady := arena.checkAllianceStationsReady(arena.AllianceStationIds(1)...) == nil

	// Handle the evergreen PLC functions: stack lights, stack buzzer, and field reset light.
	switch arena.MatchState {
	case PreMatch:
		fallthrough
	case TimeoutActive:
		fallthrough
	case PostTimeout:
		// Set the stack light state -- solid alliance color(s) if robots are not connected, solid orange if scores are
		// not input, or blinking green if ready.
		greenStackLight := redAllianceReady && blueAllianceReady && arena.Plc.GetCycleState(2, 0, 2)
		arena.Plc.SetStackLights(!redAllianceReady, !blueAllianceReady, false, greenStackLight)
		arena.Plc.SetStackBuzzer(redAllianceReady && blueAllianceReady)

		// Turn off lights if all teams become ready.
		if redAllianceReady && blueAllianceReady {
			arena.FieldVolunteers = false
			arena.FieldReset = false
			arena.Plc.SetFieldResetLight(false)
			if arena.CurrentMatch.FieldReadyAt.IsZero() {
				arena.CurrentMatch.FieldReadyAt = time.Now()
			}
		}
	case PostMatch:
		if arena.FieldReset {
			arena.Plc.SetFieldResetLight(true)
		}
		scoreReady := arena.RefereeScoreReady() && arena.scoringPanelsReady()
		arena.Plc.SetStackLights(false, false, !scoreReady, false)
	case InMatch:
		arena.Plc.SetStackBuzzer(false)
		arena.Plc.SetStackLights(!redAllianceReady, !blueAllianceReady, false, true)
	}
}

func (arena *Arena) ToggleBypass(station string) error {
	if _, ok := arena.AllianceStations[station]; !ok {
		return fmt.Errorf("Invalid alliance station '%s'.", station)
	}
	arena.AllianceStations[station].Bypass = !arena.AllianceStations[station].Bypass
	arena.ArenaStatusNotifier.Notify()
	return nil
}

func (arena *Arena) handleTeamStop(station string, eStopState, aStopState bool) {
	allianceStation := arena.AllianceStations[station]
	if eStopState {
		allianceStation.EStop = true
	} else if arena.MatchState != InMatch {
		// Keep the E-stop latched until the match is over. This asks the state rather than the clock: right after
		// the start the clock can still read zero, and a robot stopped then must stay stopped.
		allianceStation.EStop = false
	}
	if aStopState {
		allianceStation.AStop = true
	} else if !arena.inAutoPhase() {
		// Keep the A-stop latched until the autonomous period is over.
		allianceStation.AStop = false
		allianceStation.aStopReset = true
	}
}

// Set the field lights and team signs to purple, if not in a match.
func (arena *Arena) SignalVolunteers() {
	if arena.MatchState != PostMatch && arena.MatchState != PreMatch && arena.MatchState != TimeoutActive {
		// Don't signal volunteers during matches.
		return
	}
	arena.FieldVolunteers = true
	arena.FieldReset = false
	arena.AllianceStationDisplayMode = "signalCount"
	arena.AllianceStationDisplayModeNotifier.Notify()
	arena.Plc.SetFieldResetLight(false)
	arena.Bus.Publish("field.volunteers", nil)
}

// Set the field lights and team signs to green, if not in a match.
func (arena *Arena) SignalReset() {
	if arena.MatchState != PostMatch && arena.MatchState != PreMatch && arena.MatchState != TimeoutActive {
		// Don't signal reset during matches.
		return
	}
	if !arena.FieldReset {
		// Play the audio cue once, even if reset is signaled multiple times.
		arena.PlaySound("field_reset")
	}
	arena.FieldVolunteers = false
	arena.FieldReset = true
	arena.AllianceStationDisplayMode = "fieldReset"
	arena.AllianceStationDisplayModeNotifier.Notify()
	arena.Plc.SetFieldResetLight(true)
	arena.Bus.Publish("field.reset", nil)
}

func (arena *Arena) PlaySound(name string) {
	if !arena.MuteMatchSounds {
		arena.PlaySoundNotifier.NotifyWithMessage(name)
	}
}

func (arena *Arena) positionPostMatchScoreReady(position string) bool {
	numPanels := arena.ScoringPanelRegistry.GetNumPanels(position)
	return numPanels > 0 && arena.ScoringPanelRegistry.GetNumScoreCommitted(position) >= numPanels
}

// scoringPanelsReady reports whether every scoring panel position of the game is staffed and has committed.
func (arena *Arena) scoringPanelsReady() bool {
	for _, panel := range arena.ScoringPanels() {
		if !arena.positionPostMatchScoreReady(panel.ID) {
			return false
		}
	}
	return true
}

func (arena *Arena) checkForUpdatedNexusLineup() {
	if !(arena.EventSettings.NexusEnabled && arena.CurrentMatch.ShouldAllowNexusSubstitution()) {
		return
	}

	if arena.MatchState != PreMatch {
		// Only check for an updated lineup pre-match.
		return
	}

	lineup, err := arena.NexusClient.GetLineup(arena.CurrentMatch.TbaMatchKey)
	if err != nil {
		log.Printf("Failed to load lineup from Nexus: %s", err.Error())
		return
	}

	if !arena.CurrentMatch.IsLineupEqual(lineup[0], lineup[1], lineup[2], lineup[3], lineup[4], lineup[5]) {
		log.Printf("Got updated lineup from Nexus, substituting")
		err = arena.SubstituteTeams(lineup[0], lineup[1], lineup[2], lineup[3], lineup[4], lineup[5])
		if err != nil {
			log.Printf("Failed to substitute teams using Nexus lineup: %s", err.Error())
			return
		}
		log.Printf(
			"Successfully updated lineup for match %s from Nexus: %v", arena.CurrentMatch.TbaMatchKey.String(), *lineup,
		)
	}
}

// Performs any actions that need to run at the interval specified by periodicTaskPeriodSec.
func (arena *Arena) runPeriodicTasks() {
	arena.updateEarlyLateMessage()
	arena.purgeDisconnectedDisplays()
	arena.checkForUpdatedNexusLineup()
}

// Handles audience display automation from after score post to next match intro.
func (arena *Arena) AutomateAudienceDisplay(postedMatch *model.Match) {
	// Show the score for 20 seconds before moving on.
	time.Sleep(20 * time.Second)
	if arena.AudienceDisplayMode != "score" {
		return
	}

	if arena.CurrentMatch.Type == model.Playoff {
		time.Sleep(10 * time.Second)
		isFinals := strings.Contains(postedMatch.LongName, "Final") || strings.Contains(
			postedMatch.LongName,
			"Overtime",
		)
		if !isFinals {
			arena.SetAudienceDisplayMode("bracket")
			time.Sleep(20 * time.Second)
			if arena.AudienceDisplayMode != "bracket" {
				return
			}
		}
	}

	if arena.MatchState == TimeoutActive {
		arena.SetAudienceDisplayMode("timeout")
		arena.SetAllianceStationDisplayMode("timeout")
		return
	}

	if arena.CurrentMatch.Type == model.Test {
		// No next match loaded, show the score for longer and then go into awards mode.
		time.Sleep(40 * time.Second)
		if arena.AudienceDisplayMode != "score" {
			return
		}

		arena.SetAudienceDisplayMode("logoLuma")
		arena.SetAllianceStationDisplayMode("logo")
		return
	}

	arena.SetAudienceDisplayMode("intro")
}
