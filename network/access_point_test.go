// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package network

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestAccessPoint_ConfigureTeamWifi(t *testing.T) {
	var ap AccessPoint
	var request configurationRequest
	wifiStatuses := [6]*TeamWifiStatus{{}, {}, {}, {}, {}, {}}
	ap.SetSettings("dummy", "password1", 123, true, false, wifiStatuses)

	// Mock the radio API server.
	radioServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, r.URL.Path, "/configuration")
				assert.Equal(t, "Bearer password1", r.Header.Get("Authorization"))
				assert.Nil(t, json.NewDecoder(r.Body).Decode(&request))
			},
		),
	)
	ap.apiUrl = radioServer.URL

	// All stations assigned.
	team1 := &model.Team{Id: 254, WpaKey: "11111111"}
	team2 := &model.Team{Id: 1114, WpaKey: "22222222"}
	team3 := &model.Team{Id: 469, WpaKey: "33333333"}
	team4 := &model.Team{Id: 2046, WpaKey: "44444444"}
	team5 := &model.Team{Id: 2056, WpaKey: "55555555"}
	team6 := &model.Team{Id: 1678, WpaKey: "66666666"}
	assert.Nil(t, ap.ConfigureTeamWifi([6]*model.Team{team1, team2, team3, team4, team5, team6}))
	assert.Equal(
		t,
		configurationRequest{
			Channel: 123,
			StationConfigurations: map[string]stationConfiguration{
				"red1":  {"254", "11111111"},
				"red2":  {"1114", "22222222"},
				"red3":  {"469", "33333333"},
				"blue1": {"2046", "44444444"},
				"blue2": {"2056", "55555555"},
				"blue3": {"1678", "66666666"},
			},
		},
		request,
	)

	// Different channel and only some stations assigned.
	ap.channel = 456
	request = configurationRequest{}
	assert.Nil(t, ap.ConfigureTeamWifi([6]*model.Team{nil, nil, team2, nil, team1, nil}))
	assert.Equal(
		t,
		configurationRequest{
			Channel: 456,
			StationConfigurations: map[string]stationConfiguration{
				"red3":  {"1114", "22222222"},
				"blue2": {"254", "11111111"},
			},
		},
		request,
	)

	// Radio API returns an error.
	radioServer = httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, r.URL.Path, "/configuration")
				http.Error(w, "oh noes", 507)
			},
		),
	)
	ap.apiUrl = radioServer.URL
	err := ap.ConfigureTeamWifi([6]*model.Team{team1, team2, team3, team4, team5, team6})
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "returned status 507: oh noes")
	}
	assert.Equal(t, "CONFIGURING", ap.Status)
}

func TestAccessPoint_updateMonitoring(t *testing.T) {
	var ap AccessPoint
	wifiStatuses := [6]*TeamWifiStatus{{}, {}, {}, {}, {}, {}}
	ap.SetSettings("dummy", "password2", 123, true, false, wifiStatuses)

	apStatus := accessPointStatus{
		Channel: 456,
		Status:  "ACTIVE",
		StationStatuses: map[string]*stationStatus{
			"red1":  {"254", "hash111", "salt1", true, 1, 2, 3, 4, "excellent"},
			"red2":  {"1114", "hash222", "salt2", false, 5, 6, 7, 8, ""},
			"red3":  {"469", "hash333", "salt3", true, 9, 10, 11, 12, "caution"},
			"blue1": {"2046", "hash444", "salt4", false, 13, 14, 15, 16, "warning"},
			"blue2": {"2056", "hash555", "salt5", true, 17, 18, 19, 20, "nonexistent"},
			"blue3": {"1678", "hash666", "salt6", false, 21, 22, 23, 24, "good"},
		},
	}

	// Mock the radio API server.
	radioServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, r.URL.Path, "/status")
				assert.Equal(t, "Bearer password2", r.Header.Get("Authorization"))
				assert.Nil(t, json.NewEncoder(w).Encode(apStatus))
			},
		),
	)
	ap.apiUrl = radioServer.URL

	// All stations assigned.
	assert.Nil(t, ap.updateMonitoring())
	assert.Equal(t, 123, ap.channel) // Should not have changed to reflect the radio API.
	assert.Equal(t, "ACTIVE", ap.Status)
	assert.Equal(t, TeamWifiStatus{254, true, 4, 1, 2, 3, 4}, *wifiStatuses[0])
	assert.Equal(t, TeamWifiStatus{1114, false, 8, 5, 6, 7, 0}, *wifiStatuses[1])
	assert.Equal(t, TeamWifiStatus{469, true, 12, 9, 10, 11, 1}, *wifiStatuses[2])
	assert.Equal(t, TeamWifiStatus{2046, false, 16, 13, 14, 15, 2}, *wifiStatuses[3])
	assert.Equal(t, TeamWifiStatus{2056, true, 20, 17, 18, 19, 0}, *wifiStatuses[4])
	assert.Equal(t, TeamWifiStatus{1678, false, 24, 21, 22, 23, 3}, *wifiStatuses[5])

	// Only some stations assigned.
	apStatus.Status = "CONFIGURING"
	apStatus.StationStatuses = map[string]*stationStatus{
		"red1":  nil,
		"red2":  nil,
		"red3":  {"469", "hash333", "salt3", true, 9, 10, 11, 12, "caution"},
		"blue1": nil,
		"blue2": {"2056", "hash555", "salt5", true, 17, 18, 19, 20, "excellent"},
		"blue3": nil,
	}
	assert.Nil(t, ap.updateMonitoring())
	assert.Equal(t, "CONFIGURING", ap.Status)
	assert.Equal(t, TeamWifiStatus{}, *wifiStatuses[0])
	assert.Equal(t, TeamWifiStatus{}, *wifiStatuses[1])
	assert.Equal(t, TeamWifiStatus{469, true, 12, 9, 10, 11, 1}, *wifiStatuses[2])
	assert.Equal(t, TeamWifiStatus{}, *wifiStatuses[3])
	assert.Equal(t, TeamWifiStatus{2056, true, 20, 17, 18, 19, 4}, *wifiStatuses[4])
	assert.Equal(t, TeamWifiStatus{}, *wifiStatuses[5])

	// Radio API returns an error.
	radioServer = httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, r.URL.Path, "/status")
				http.Error(w, "gosh darn", 404)
			},
		),
	)
	ap.apiUrl = radioServer.URL
	err := ap.updateMonitoring()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "returned status 404: gosh darn")
	}
	assert.Equal(t, "ERROR", ap.Status)
}

func TestAccessPoint_statusMatchesLastConfiguration(t *testing.T) {
	var ap AccessPoint
	wifiStatuses := [6]*TeamWifiStatus{{}, {}, {}, {}, {}, {}}
	ap.SetSettings("dummy", "", 123, true, false, wifiStatuses)

	assert.True(t, ap.statusMatchesLastConfiguration())
	team1 := &model.Team{Id: 254, WpaKey: "11111111"}
	team2 := &model.Team{Id: 1678, WpaKey: "22222222"}
	ap.ConfigureTeamWifi([6]*model.Team{nil, team1, nil, team2, nil, nil})
	assert.False(t, ap.statusMatchesLastConfiguration())
	ap.TeamWifiStatuses[1].TeamId = 254
	assert.False(t, ap.statusMatchesLastConfiguration())
	ap.TeamWifiStatuses[3].TeamId = 1677
	assert.False(t, ap.statusMatchesLastConfiguration())
	ap.TeamWifiStatuses[3].TeamId = 1678
	assert.True(t, ap.statusMatchesLastConfiguration())
	ap.TeamWifiStatuses[4].TeamId = 111
	assert.False(t, ap.statusMatchesLastConfiguration())
}

func TestAccessPoint_KeepTeams(t *testing.T) {
	var ap AccessPoint
	var requests []configurationRequest
	wifiStatuses := [6]*TeamWifiStatus{{}, {}, {}, {}, {}, {}}
	ap.SetSettings("dummy", "", 13, true, true, wifiStatuses)

	radioServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				var request configurationRequest
				assert.Nil(t, json.NewDecoder(r.Body).Decode(&request))
				requests = append(requests, request)
			},
		),
	)
	defer radioServer.Close()
	ap.apiUrl = radioServer.URL

	team1 := &model.Team{Id: 254, WpaKey: "11111111"}
	team2 := &model.Team{Id: 1114, WpaKey: "22222222"}
	team3 := &model.Team{Id: 469, WpaKey: "33333333"}
	stations := func(pairs ...any) map[string]stationConfiguration {
		configurations := map[string]stationConfiguration{}
		for i := 0; i < len(pairs); i += 2 {
			team := pairs[i+1].(*model.Team)
			configurations[pairs[i].(string)] = stationConfiguration{strconv.Itoa(team.Id), team.WpaKey}
		}
		return configurations
	}

	steps := []struct {
		name     string
		teams    [6]*model.Team
		channel  int
		expected map[string]stationConfiguration // What is sent; nil when nothing is.
	}{
		{"an empty match at startup leaves the access point alone", [6]*model.Team{}, 13, nil},
		{"a team is put on", [6]*model.Team{team1}, 13, stations("red1", team1)},
		{"the next match is empty: nothing is taken off", [6]*model.Team{}, 13, nil},
		{"the same team in the same station again", [6]*model.Team{team1}, 13, nil},
		{
			"a second team joins and the first stays", [6]*model.Team{nil, nil, nil, team2}, 13,
			stations("red1", team1, "blue1", team2),
		},
		{
			"a team given another station leaves its old one", [6]*model.Team{nil, nil, nil, nil, team1}, 13,
			stations("blue1", team2, "blue2", team1),
		},
		{
			"another team takes a station", [6]*model.Team{nil, nil, nil, team3}, 13,
			stations("blue1", team3, "blue2", team1),
		},
		{"a new channel is sent with the teams as they are", [6]*model.Team{}, 21, stations("blue1", team3, "blue2", team1)},
		{
			"a new key for a team is sent", [6]*model.Team{nil, nil, nil, {Id: 469, WpaKey: "44444444"}}, 21,
			stations("blue1", &model.Team{Id: 469, WpaKey: "44444444"}, "blue2", team1),
		},
	}
	for _, step := range steps {
		requests = nil
		ap.channel = step.channel
		assert.Nil(t, ap.ConfigureTeamWifi(step.teams), step.name)
		if step.expected == nil {
			assert.Empty(t, requests, step.name)
		} else if assert.Len(t, requests, 1, step.name) {
			assert.Equal(t, configurationRequest{step.channel, step.expected}, requests[0], step.name)
		}
	}

	// A station that was asked nothing for may hold a team from before, but one that was asked for must be there.
	ap.TeamWifiStatuses[0].TeamId = 2046
	ap.TeamWifiStatuses[3].TeamId = 469
	ap.TeamWifiStatuses[4].TeamId = 254
	assert.True(t, ap.statusMatchesLastConfiguration())
	ap.TeamWifiStatuses[4].TeamId = 0
	assert.False(t, ap.statusMatchesLastConfiguration())

	// Putting right an access point that has lost its teams sends them again, though nothing about them changed.
	requests = nil
	assert.Nil(t, ap.configure(nil))
	if assert.Len(t, requests, 1) {
		assert.Equal(t, stations("blue1", &model.Team{Id: 469, WpaKey: "44444444"}, "blue2", team1),
			requests[0].StationConfigurations)
	}

	// Without the setting, an empty match empties the access point, as it always has.
	ap.keepTeams = false
	requests = nil
	assert.Nil(t, ap.ConfigureTeamWifi([6]*model.Team{}))
	if assert.Len(t, requests, 1) {
		assert.Empty(t, requests[0].StationConfigurations)
	}
	ap.TeamWifiStatuses[0].TeamId = 2046
	assert.False(t, ap.statusMatchesLastConfiguration())
}
