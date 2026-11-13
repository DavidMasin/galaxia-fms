// The arena's side of the field devices: it tells them where the match is, carries out the field actions they ask
// for, combines their stops with the PLC's, and hands them the state of the field for their outputs.

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/rules"
	"log"
	"time"
)

const (
	// Device outputs are evaluated on every fifth pass of the arena loop: about twenty times a second.
	deviceOutputTicks = 5
	// Match events kept for the next output evaluation; only a stalled arena loop would let more pile up.
	deviceEventLimit = 200
	// Field actions waiting for a handler (commit, next match) beyond this many are refused.
	fieldActionQueueSize = 16
)

// FieldActionHandler carries out a field action that needs more than the arena: committing and posting the score,
// or loading the next match. The web layer registers them at startup with SetFieldActionHandler. A handler returns
// an error to refuse, which shows on the device that asked.
type FieldActionHandler func() error

// newDevices builds the device manager with its virtual and MQTT transports. The transports start in Run, so a test
// arena never opens a port.
func (arena *Arena) newDevices() error {
	var err error
	arena.Devices, err = device.NewManager(device.Options{Database: arena.Database, Bus: arena.Bus, Scorer: arena})
	if err != nil {
		return err
	}
	arena.DeviceVirtual = device.NewVirtualTransport()
	arena.DeviceMqtt = device.NewMqttTransport(device.MqttConfig{})
	if err = arena.Devices.AddTransport(arena.DeviceVirtual); err != nil {
		return err
	}
	return arena.Devices.AddTransport(arena.DeviceMqtt)
}

// applyDeviceSettings takes the device settings into use: the field profile whose bindings apply, and the ports of
// the MQTT broker, which is restarted if they changed while it runs.
func (arena *Arena) applyDeviceSettings() {
	arena.Devices.SetProfile(arena.EventSettings.FieldProfile)
	config := device.MqttConfig{
		TCPAddress: device.MqttAddress(arena.EventSettings.DeviceMqttPort),
		WSAddress:  device.MqttAddress(arena.EventSettings.DeviceMqttWsPort),
	}
	if err := arena.DeviceMqtt.Configure(config); err != nil {
		log.Printf("Failed to restart the device MQTT broker: %v", err)
	}
}

// fieldStationIdList returns the id of every station of the field, the ones the active game leaves unused too:
// device bindings name physical stations.
func fieldStationIdList() []string {
	var ids []string
	for _, stationIds := range fieldStationIds {
		ids = append(ids, stationIds[:]...)
	}
	return ids
}

// deviceMatchInfo is what device input processing needs to know about the match right now.
func (arena *Arena) deviceMatchInfo() device.MatchInfo {
	var info device.MatchInfo
	switch arena.MatchState {
	case InMatch:
		if phase := arena.CurrentPhase(); phase != nil {
			info.Running, info.Scoring, info.Phase = true, true, phase.ID
		}
		if grace := arena.gracePhase(); grace != nil {
			info.GracePhase = grace.ID
		}
	case PostMatch:
		// Input still on its way when the match stopped counts for the phase that was running.
		phases := arena.Game.Match.Phases
		if arena.endPhaseIndex >= 0 && arena.endPhaseIndex < len(phases) &&
			time.Since(arena.matchStopTime).Seconds() < arena.Game.Match.ScoreGrace {
			info.Scoring, info.GracePhase = true, phases[arena.endPhaseIndex].ID
		}
	}
	return info
}

// updateDevices runs the device side of one pass of the arena loop, before the stops are handled: the manager learns
// where the match is and runs its timers (debounce, the heartbeat watchdog), and the field actions devices asked for
// are carried out here, on the arena goroutine.
func (arena *Arena) updateDevices() {
	arena.Devices.SetMatch(arena.deviceMatchInfo())
	arena.Devices.Tick(time.Now())
	for _, action := range arena.Devices.TakeFieldActions() {
		var err error
		switch action.Action {
		case device.ActionStartMatch:
			err = arena.StartMatch()
		case device.ActionAbortMatch:
			err = arena.AbortMatch()
		case device.ActionFieldReset, device.ActionVolunteers:
			if arena.MatchState != PostMatch && arena.MatchState != PreMatch && arena.MatchState != TimeoutActive {
				err = fmt.Errorf("not while a match is in progress")
			} else if action.Action == device.ActionFieldReset {
				arena.SignalReset()
			} else {
				arena.SignalVolunteers()
			}
		case device.ActionCommit, device.ActionNextMatch:
			arena.queueFieldActionHandler(action)
			continue
		default:
			err = fmt.Errorf("the arena does not carry out %q", action.Action)
		}
		arena.Devices.ReportFieldAction(action, err)
	}
}

// updateDeviceOutputs hands the device manager the state of the field, every deviceOutputTicks passes of the loop.
func (arena *Arena) updateDeviceOutputs() {
	arena.deviceTicks++
	if arena.deviceTicks < deviceOutputTicks {
		return
	}
	arena.deviceTicks = 0
	arena.Devices.UpdatePlc(arena.Plc)
	arena.Devices.UpdateOutputs(arena.deviceSnapshot())
}

// deviceSnapshot builds what output bindings are evaluated against. It runs on the arena goroutine; the expressions
// themselves are evaluated under the score lock.
func (arena *Arena) deviceSnapshot() *device.Snapshot {
	clock := arena.clock()
	result, _, _ := arena.ScoreSnapshot()
	scores := make(map[string]float64)
	if result != nil {
		for allianceId, allianceResult := range result.Alliances {
			scores[allianceId] = allianceResult.Score
		}
	}
	stations := make(map[string]device.StationState, len(arena.AllianceStations))
	for _, stationId := range fieldStationIdList() {
		allianceStation := arena.AllianceStations[stationId]
		state := device.StationState{
			EStop: allianceStation.EStop, AStop: allianceStation.AStop, Bypass: allianceStation.Bypass,
			Ready: len(arena.getAllianceStationStartConditions(stationId)) == 0,
		}
		if allianceStation.Team != nil {
			state.Team = allianceStation.Team.Id
		}
		if allianceStation.DsConn != nil {
			state.Connected = allianceStation.DsConn.RobotLinked
		}
		stations[stationId] = state
	}
	return &device.Snapshot{
		Time: time.Now(), Game: arena.Game, Clock: clock, Scores: scores, Stations: stations,
		FieldReset: arena.FieldReset, Volunteers: arena.FieldVolunteers, Events: arena.takeDeviceEvents(),
		Eval: func(program *rules.Program, own string, extra map[string]any) (any, error) {
			arena.scoreMutex.Lock()
			defer arena.scoreMutex.Unlock()
			if arena.Score.Result == nil {
				return nil, fmt.Errorf("the score could not be computed")
			}
			return arena.Score.Result.Eval(program, own, clock, extra)
		},
	}
}

// recordDeviceEvent notes a match event for the next output evaluation, where outputs bound to it pulse. It may be
// called from any goroutine: a score is posted from the web layer's.
func (arena *Arena) recordDeviceEvent(event string) {
	arena.deviceEventsMutex.Lock()
	defer arena.deviceEventsMutex.Unlock()
	arena.deviceEvents = append(arena.deviceEvents, event)
	if len(arena.deviceEvents) > deviceEventLimit {
		arena.deviceEvents = arena.deviceEvents[len(arena.deviceEvents)-deviceEventLimit:]
	}
}

func (arena *Arena) takeDeviceEvents() []string {
	arena.deviceEventsMutex.Lock()
	defer arena.deviceEventsMutex.Unlock()
	events := arena.deviceEvents
	arena.deviceEvents = nil
	return events
}

// SetFieldActionHandler registers what carries out the commit or next_match field action. The web layer does this
// at startup.
func (arena *Arena) SetFieldActionHandler(action string, handler FieldActionHandler) error {
	if action != device.ActionCommit && action != device.ActionNextMatch {
		return fmt.Errorf("the arena carries out %q itself", action)
	}
	arena.fieldActionMutex.Lock()
	defer arena.fieldActionMutex.Unlock()
	arena.fieldActionHandlers[action] = handler
	return nil
}

// queueFieldActionHandler hands a commit or next_match to the worker that runs their handlers. They do what the
// Commit & Post and next match buttons do, which includes writing a database backup and may include The Blue
// Alliance, so they run on a goroutine of their own, one at a time, as the buttons' handlers run on the web
// layer's: never on the arena loop.
func (arena *Arena) queueFieldActionHandler(action device.FieldAction) {
	arena.fieldActionMutex.Lock()
	if arena.fieldActionQueue == nil {
		arena.fieldActionQueue = make(chan device.FieldAction, fieldActionQueueSize)
		go arena.runFieldActionHandlers(arena.fieldActionQueue)
	}
	queue := arena.fieldActionQueue
	arena.fieldActionMutex.Unlock()
	select {
	case queue <- action:
	default:
		arena.Devices.ReportFieldAction(action, fmt.Errorf("too many field actions are waiting"))
	}
}

func (arena *Arena) runFieldActionHandlers(queue chan device.FieldAction) {
	for action := range queue {
		arena.fieldActionMutex.Lock()
		handler := arena.fieldActionHandlers[action.Action]
		arena.fieldActionMutex.Unlock()
		var err error
		if handler == nil {
			err = fmt.Errorf("nothing has been set up to carry out %s", action.Action)
		} else {
			err = handler()
		}
		arena.Devices.ReportFieldAction(action, err)
	}
}
