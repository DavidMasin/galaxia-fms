// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package model

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestEventSettingsReadWrite(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	eventSettings, err := db.GetEventSettings()
	assert.Nil(t, err)
	assert.Equal(
		t,
		EventSettings{
			Id:                         1,
			Name:                       "Untitled Event",
			PlayoffType:                DoubleEliminationPlayoff,
			NumPlayoffAlliances:        8,
			SelectionRound2Order:       "L",
			SelectionRound3Order:       "",
			SelectionShowUnpickedTeams: true,
			TbaDownloadEnabled:         true,
			ApChannel:                  36,
			SCCUpCommands:              "configure terminal\ninterface range gigabitEthernet 1/2-4\nno shutdown\nexit\nexit\nexit",
			SCCDownCommands:            "configure terminal\ninterface range gigabitEthernet 1/2-4\nshutdown\nexit\nexit\nexit",
			CompanionAddress:           "",
			CompanionPort:              0,
			GameId:                     "rebuilt-style",
			GameParams:                 map[string]float64{},
			FieldProfile:               "default",
			DeviceMqttPort:             1883,
			DeviceMqttWsPort:           8083,
		},
		*eventSettings,
	)

	eventSettings.Name = "Chezy Champs"
	eventSettings.NumPlayoffAlliances = 6
	eventSettings.SelectionRound2Order = "F"
	eventSettings.SelectionRound3Order = "L"
	eventSettings.GameId = "ball-count"
	eventSettings.GameParams = map[string]float64{"bonus_threshold": 12.5}
	err = db.UpdateEventSettings(eventSettings)
	assert.Nil(t, err)
	eventSettings2, err := db.GetEventSettings()
	assert.Nil(t, err)
	assert.Equal(t, eventSettings, eventSettings2)
}

func TestEventSettingsWithoutGame(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	// A record written before games were selectable gets the default game rather than no game at all.
	eventSettings, err := db.GetEventSettings()
	assert.Nil(t, err)
	eventSettings.GameId = ""
	eventSettings.GameParams = nil
	assert.Nil(t, db.UpdateEventSettings(eventSettings))
	eventSettings2, err := db.GetEventSettings()
	assert.Nil(t, err)
	assert.Equal(t, DefaultGameId, eventSettings2.GameId)
	assert.Equal(t, map[string]float64{}, eventSettings2.GameParams)
}
