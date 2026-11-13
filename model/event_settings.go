// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Model and datastore read/write methods for event-level configuration.

package model

import (
	"slices"
	"strings"
)

// DefaultGameId is the game a new event starts with.
const DefaultGameId = "rebuilt-style"

// The ports field devices reach the MQTT broker on, unless the settings say otherwise.
const (
	DefaultDeviceMqttPort   = 1883
	DefaultDeviceMqttWsPort = 8083
)

type PlayoffType int

const (
	DoubleEliminationPlayoff PlayoffType = iota
	SingleEliminationPlayoff
)

// Configured here to avoid circular import dependencies.
var (
	sccDefaultUpCommands = []string{
		"configure terminal",
		"interface range gigabitEthernet 1/2-4",
		"no shutdown",
		"exit",
		"exit",
		"exit",
	}
	sccDefaultDownCommands = []string{
		"configure terminal",
		"interface range gigabitEthernet 1/2-4",
		"shutdown",
		"exit",
		"exit",
		"exit",
	}
)

type EventSettings struct {
	Id                               int `db:"id"`
	Name                             string
	PlayoffType                      PlayoffType
	NumPlayoffAlliances              int
	SelectionRound2Order             string
	SelectionRound3Order             string
	SelectionShowUnpickedTeams       bool
	TbaDownloadEnabled               bool
	TbaPublishingEnabled             bool
	TbaEventCode                     string
	TbaSecretId                      string
	TbaSecret                        string
	AutoAudienceDisplayEnabled       bool
	NexusEnabled                     bool
	NexusAutoQueueEnabled            bool
	NexusAutoQueueKey                string
	NetworkSecurityEnabled           bool
	RadioOnlyNetwork                 bool
	ApAddress                        string
	ApPassword                       string
	ApChannel                        int
	SwitchAddress                    string
	SwitchPassword                   string
	SCCManagementEnabled             bool
	RedSCCAddress                    string
	BlueSCCAddress                   string
	SCCUsername                      string
	SCCPassword                      string
	SCCUpCommands                    string
	SCCDownCommands                  string
	PlcAddress                       string
	AdminPassword                    string
	TeamSignRed1Id                   int
	TeamSignRed2Id                   int
	TeamSignRed3Id                   int
	TeamSignRedTimerId               int
	TeamSignBlue1Id                  int
	TeamSignBlue2Id                  int
	TeamSignBlue3Id                  int
	TeamSignBlueTimerId              int
	UseLiteUdpPort                   bool
	BlackmagicAddresses              string
	CompanionAddress                 string
	CompanionPort                    int
	CompanionMatchPreviewPage        int
	CompanionMatchPreviewRow         int
	CompanionMatchPreviewColumn      int
	CompanionSetAudiencePage         int
	CompanionSetAudienceRow          int
	CompanionSetAudienceColumn       int
	CompanionMatchStartPage          int
	CompanionMatchStartRow           int
	CompanionMatchStartColumn        int
	CompanionTeleopStartPage         int
	CompanionTeleopStartRow          int
	CompanionTeleopStartColumn       int
	CompanionEndgameStartPage        int
	CompanionEndgameStartRow         int
	CompanionEndgameStartColumn      int
	CompanionMatchEndPage            int
	CompanionMatchEndRow             int
	CompanionMatchEndColumn          int
	CompanionPostResultPage          int
	CompanionPostResultRow           int
	CompanionPostResultColumn        int
	CompanionAllianceSelectionPage   int
	CompanionAllianceSelectionRow    int
	CompanionAllianceSelectionColumn int
	CompanionMatchAbortPage          int
	CompanionMatchAbortRow           int
	CompanionMatchAbortColumn        int
	GameId                           string             // The game definition the event is played with.
	GameParams                       map[string]float64 // Overrides of that game's parameter defaults.
	FieldProfile                     string             // The physical setup whose device bindings are in use.
	DeviceMqttPort                   int                // TCP port of the device MQTT broker; 0 turns it off.
	DeviceMqttWsPort                 int                // Port of MQTT over WebSocket for devices; 0 turns it off.
	DeviceSerialPorts                string             // Ports devices are plugged into, comma-separated.
}

// SerialPorts returns the serial ports field devices are plugged into, which the FMS keeps open: none unless some
// were chosen.
func (eventSettings *EventSettings) SerialPorts() []string {
	return cleanSerialPorts(strings.Split(eventSettings.DeviceSerialPorts, ","))
}

// SetSerialPorts chooses the serial ports field devices are plugged into. A port name cannot hold a comma, which is
// what separates them.
func (eventSettings *EventSettings) SetSerialPorts(ports []string) {
	eventSettings.DeviceSerialPorts = strings.Join(cleanSerialPorts(ports), ",")
}

// cleanSerialPorts drops blank and repeated port names.
func cleanSerialPorts(ports []string) []string {
	var clean []string
	for _, port := range ports {
		if port = strings.TrimSpace(port); port != "" && !slices.Contains(clean, port) {
			clean = append(clean, port)
		}
	}
	return clean
}

func (database *Database) GetEventSettings() (*EventSettings, error) {
	allEventSettings, err := database.eventSettingsTable.getAll()
	if err != nil {
		return nil, err
	}
	if len(allEventSettings) == 1 {
		eventSettings := allEventSettings[0]
		if eventSettings.GameId == "" {
			// A record written before games were selectable.
			eventSettings.GameId = DefaultGameId
		}
		if eventSettings.GameParams == nil {
			eventSettings.GameParams = make(map[string]float64)
		}
		if eventSettings.FieldProfile == "" {
			// A record written before field devices existed. The profile is never stored empty, which is what tells
			// these zero ports apart from ports turned off on purpose.
			eventSettings.FieldProfile = DefaultFieldProfile
			eventSettings.DeviceMqttPort = DefaultDeviceMqttPort
			eventSettings.DeviceMqttWsPort = DefaultDeviceMqttWsPort
		}
		return &eventSettings, nil
	}

	// Database record doesn't exist yet; create it now.
	eventSettings := EventSettings{
		Name:                       "Untitled Event",
		PlayoffType:                DoubleEliminationPlayoff,
		NumPlayoffAlliances:        8,
		SelectionRound2Order:       "L",
		SelectionRound3Order:       "",
		SelectionShowUnpickedTeams: true,
		TbaDownloadEnabled:         true,
		ApChannel:                  36,
		SCCUpCommands:              strings.Join(sccDefaultUpCommands, "\n"),
		SCCDownCommands:            strings.Join(sccDefaultDownCommands, "\n"),
		CompanionAddress:           "",
		GameId:                     DefaultGameId,
		GameParams:                 make(map[string]float64),
		FieldProfile:               DefaultFieldProfile,
		DeviceMqttPort:             DefaultDeviceMqttPort,
		DeviceMqttWsPort:           DefaultDeviceMqttWsPort,
	}

	if err := database.eventSettingsTable.create(&eventSettings); err != nil {
		return nil, err
	}
	return &eventSettings, nil
}

func (database *Database) UpdateEventSettings(eventSettings *EventSettings) error {
	return database.eventSettingsTable.update(eventSettings)
}
