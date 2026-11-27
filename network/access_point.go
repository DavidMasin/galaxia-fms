// Copyright 2017 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Methods for configuring a Vivid-Hosting VH-113 access point running OpenWRT for team SSIDs and VLANs.

package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/DavidMasin/galaxia-fms/model"
)

const (
	accessPointPollPeriodSec = 1
)

// The access point is polled on a goroutine of its own (Run) while the arena configures it and reads what it
// reports. mutex guards every field, and what TeamWifiStatuses points to; it is never held while talking to the
// access point. Other goroutines read through GetStatus and Snapshot.
type AccessPoint struct {
	mutex                  sync.Mutex
	apiUrl                 string
	password               string
	channel                int
	networkSecurityEnabled bool
	// Whether a team stays on the access point when its station empties, until another team takes the station.
	keepTeams        bool
	Status           string
	TeamWifiStatuses [6]*TeamWifiStatus
	// What the access point was last asked for, and whether it has been asked for anything yet.
	lastConfiguredTeams   [6]*model.Team
	lastConfiguredChannel int
	configured            bool
	// Counts the times the status or the stations' radio links were updated, so that a reader can tell whether
	// there is anything new.
	revision uint64
}

type TeamWifiStatus struct {
	TeamId            int
	RadioLinked       bool
	MBits             float64
	RxRate            float64
	TxRate            float64
	SignalNoiseRatio  int
	ConnectionQuality int
}

type configurationRequest struct {
	Channel               int                             `json:"channel"`
	StationConfigurations map[string]stationConfiguration `json:"stationConfigurations"`
}

type stationConfiguration struct {
	Ssid   string `json:"ssid"`
	WpaKey string `json:"wpaKey"`
}

type accessPointStatus struct {
	Channel         int                       `json:"channel"`
	Status          string                    `json:"status"`
	StationStatuses map[string]*stationStatus `json:"stationStatuses"`
}

type stationStatus struct {
	Ssid              string  `json:"ssid"`
	HashedWpaKey      string  `json:"hashedWpaKey"`
	WpaKeySalt        string  `json:"wpaKeySalt"`
	IsLinked          bool    `json:"isLinked"`
	RxRateMbps        float64 `json:"rxRateMbps"`
	TxRateMbps        float64 `json:"txRateMbps"`
	SignalNoiseRatio  int     `json:"signalNoiseRatio"`
	BandwidthUsedMbps float64 `json:"bandwidthUsedMbps"`
	ConnectionQuality string  `json:"connectionQuality"`
}

var connectionQualityMap = map[string]int{
	"caution":   1,
	"warning":   2,
	"good":      3,
	"excellent": 4,
}

func (ap *AccessPoint) SetSettings(
	address, password string,
	channel int,
	networkSecurityEnabled bool,
	keepTeams bool,
	wifiStatuses [6]*TeamWifiStatus,
) {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	ap.apiUrl = fmt.Sprintf("http://%s", address)
	ap.password = password
	ap.channel = channel
	ap.networkSecurityEnabled = networkSecurityEnabled
	ap.keepTeams = keepTeams
	ap.Status = "UNKNOWN"
	ap.TeamWifiStatuses = wifiStatuses
	ap.revision++
}

// GetStatus returns the state of the access point: UNKNOWN, CONFIGURING, ACTIVE, ERROR, or what it reports itself.
func (ap *AccessPoint) GetStatus() string {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	return ap.Status
}

// Snapshot returns a copy of what the access point last reported about each station's radio link, in the order red
// 1 to 3 then blue 1 to 3, and a number that changes whenever any of it does.
func (ap *AccessPoint) Snapshot() (statuses [6]TeamWifiStatus, revision uint64) {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	for i, status := range ap.TeamWifiStatuses {
		if status != nil {
			statuses[i] = *status
		}
	}
	return statuses, ap.revision
}

// Loops indefinitely to read status from the access point.
func (ap *AccessPoint) Run() {
	for {
		time.Sleep(time.Second * accessPointPollPeriodSec)
		if err := ap.updateMonitoring(); err != nil {
			log.Printf("Failed to update access point monitoring: %v", err)
			continue
		}

		// If the access point is in a good state but doesn't match the expected configuration, try again.
		ap.mutex.Lock()
		retry := ap.Status == "ACTIVE" && !ap.statusMatchesLastConfiguration()
		ap.mutex.Unlock()
		if retry {
			log.Println("Access point is ACTIVE but does not match expected configuration; retrying configuration.")
			if err := ap.configure(nil); err != nil {
				log.Printf("Failed to reconfigure access point: %v", err)
			}
		}
	}
}

// Calls the access point's API to configure the team SSIDs and WPA keys. When teams are kept, a station given no
// team keeps the one it has, and the access point is not called at all when that leaves it with what it was last
// asked for: the robots already on it stay linked from one match to the next.
func (ap *AccessPoint) ConfigureTeamWifi(teams [6]*model.Team) error {
	return ap.configure(&teams)
}

// configure asks the access point for the given teams, or for the last ones again when given none.
func (ap *AccessPoint) configure(newTeams *[6]*model.Team) error {
	ap.mutex.Lock()
	if !ap.networkSecurityEnabled {
		ap.mutex.Unlock()
		return nil
	}

	teams := ap.lastConfiguredTeams
	if newTeams != nil {
		teams = *newTeams
		if ap.keepTeams {
			teams = withKeptTeams(ap.lastConfiguredTeams, teams)
			// Before anything has been asked for there is no channel to compare, and nothing to ask for either.
			sameChannel := !ap.configured || ap.channel == ap.lastConfiguredChannel
			if sameChannel && sameTeams(teams, ap.lastConfiguredTeams) {
				ap.mutex.Unlock()
				return nil
			}
		}
	}

	ap.Status = "CONFIGURING"
	ap.revision++
	ap.lastConfiguredTeams = teams
	ap.lastConfiguredChannel = ap.channel
	ap.configured = true
	channel, apiUrl, password := ap.channel, ap.apiUrl, ap.password
	ap.mutex.Unlock()

	request := configurationRequest{
		Channel:               channel,
		StationConfigurations: make(map[string]stationConfiguration),
	}
	addStation(request.StationConfigurations, "red1", teams[0])
	addStation(request.StationConfigurations, "red2", teams[1])
	addStation(request.StationConfigurations, "red3", teams[2])
	addStation(request.StationConfigurations, "blue1", teams[3])
	addStation(request.StationConfigurations, "blue2", teams[4])
	addStation(request.StationConfigurations, "blue3", teams[5])
	jsonBody, err := json.Marshal(request)
	if err != nil {
		return err
	}

	// Send the configuration to the access point API.
	url := apiUrl + "/configuration"
	httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	if password != "" {
		httpRequest.Header.Add("Authorization", fmt.Sprintf("Bearer %s", password))
	}
	httpClient := http.Client{Timeout: time.Second * 3}
	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		return err
	}
	defer func() {
		if err := httpResponse.Body.Close(); err != nil {
			log.Printf("Failed to close access point configuration response body: %v", err)
		}
	}()
	if httpResponse.StatusCode/100 != 2 {
		body, err := io.ReadAll(httpResponse.Body)
		if err != nil {
			return fmt.Errorf("access point returned status %d and failed to read response body: %w",
				httpResponse.StatusCode, err)
		}
		return fmt.Errorf("access point returned status %d: %s", httpResponse.StatusCode, string(body))
	}

	log.Println("Access point accepted the new configuration and will apply it asynchronously.")
	return nil
}

// Fetches the current access point status from the API and updates the status structure.
func (ap *AccessPoint) updateMonitoring() error {
	ap.mutex.Lock()
	enabled, apiUrl, password := ap.networkSecurityEnabled, ap.apiUrl, ap.password
	ap.mutex.Unlock()
	if !enabled {
		return nil
	}

	// Fetch the status from the access point API.
	url := apiUrl + "/status"
	httpRequest, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if password != "" {
		httpRequest.Header.Add("Authorization", fmt.Sprintf("Bearer %s", password))
	}
	var httpClient http.Client
	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		ap.setStatus("ERROR")
		return fmt.Errorf("failed to fetch access point status: %v", err)
	}
	defer func() {
		if err := httpResponse.Body.Close(); err != nil {
			log.Printf("Failed to close access point status response body: %v", err)
		}
	}()
	if httpResponse.StatusCode/100 != 2 {
		ap.setStatus("ERROR")
		body, err := io.ReadAll(httpResponse.Body)
		if err != nil {
			return fmt.Errorf("access point returned status %d and failed to read response body: %w",
				httpResponse.StatusCode, err)
		}
		return fmt.Errorf("access point returned status %d: %s", httpResponse.StatusCode, string(body))
	}

	// Parse the response and populate the status structure.
	var apStatus accessPointStatus
	err = json.NewDecoder(httpResponse.Body).Decode(&apStatus)
	if err != nil {
		ap.setStatus("ERROR")
		return fmt.Errorf("failed to parse access point status: %v", err)
	}

	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	ap.revision++
	if ap.Status != apStatus.Status {
		log.Printf("Access point status changed from %s to %s.", ap.Status, apStatus.Status)
		ap.Status = apStatus.Status
		if ap.Status == "ACTIVE" {
			log.Printf("Access point detailed status:\n%s", apStatus.toLogString())
		}
	}
	updateTeamWifiStatus(ap.TeamWifiStatuses[0], apStatus.StationStatuses["red1"])
	updateTeamWifiStatus(ap.TeamWifiStatuses[1], apStatus.StationStatuses["red2"])
	updateTeamWifiStatus(ap.TeamWifiStatuses[2], apStatus.StationStatuses["red3"])
	updateTeamWifiStatus(ap.TeamWifiStatuses[3], apStatus.StationStatuses["blue1"])
	updateTeamWifiStatus(ap.TeamWifiStatuses[4], apStatus.StationStatuses["blue2"])
	updateTeamWifiStatus(ap.TeamWifiStatuses[5], apStatus.StationStatuses["blue3"])

	return nil
}

func (ap *AccessPoint) setStatus(status string) {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	ap.Status = status
	ap.revision++
}

// Returns true if the access point's current status matches the last configuration that was sent to it. The caller
// holds mutex. When teams are kept, a station that was asked nothing for may hold any team: one from before this
// program started, which is not to be taken off.
func (ap *AccessPoint) statusMatchesLastConfiguration() bool {
	for i := 0; i < 6; i++ {
		var expectedTeamId, actualTeamId int
		if ap.lastConfiguredTeams[i] != nil {
			expectedTeamId = ap.lastConfiguredTeams[i].Id
		}
		if ap.keepTeams && expectedTeamId == 0 {
			continue
		}
		if ap.TeamWifiStatuses[i] != nil {
			actualTeamId = ap.TeamWifiStatuses[i].TeamId
		}
		if expectedTeamId != actualTeamId {
			return false
		}
	}
	return true
}

// withKeptTeams returns the teams to ask for when a team stays until another takes its station: a station given no
// team keeps the one it was last given, unless that team has been given another station.
func withKeptTeams(last, teams [6]*model.Team) [6]*model.Team {
	kept := teams
	for i, team := range teams {
		if team != nil || last[i] == nil {
			continue
		}
		moved := false
		for _, other := range teams {
			if other != nil && other.Id == last[i].Id {
				moved = true
			}
		}
		if !moved {
			kept[i] = last[i]
		}
	}
	return kept
}

// sameTeams returns true if both sets would be the same configuration: the same team with the same key in every
// station.
func sameTeams(a, b [6]*model.Team) bool {
	for i := range a {
		if (a[i] == nil) != (b[i] == nil) {
			return false
		}
		if a[i] != nil && (a[i].Id != b[i].Id || a[i].WpaKey != b[i].WpaKey) {
			return false
		}
	}
	return true
}

// Generates the configuration for the given team's station and adds it to the map. If the team is nil, no entry is
// added for the station.
func addStation(stationsConfigurations map[string]stationConfiguration, station string, team *model.Team) {
	if team == nil {
		return
	}
	stationsConfigurations[station] = stationConfiguration{
		Ssid:   strconv.Itoa(team.Id),
		WpaKey: team.WpaKey,
	}
}

// Updates the given team's wifi status structure with the given station status.
func updateTeamWifiStatus(teamWifiStatus *TeamWifiStatus, stationStatus *stationStatus) {
	if teamWifiStatus == nil {
		return
	}
	if stationStatus == nil {
		teamWifiStatus.TeamId = 0
		teamWifiStatus.RadioLinked = false
		teamWifiStatus.MBits = 0
		teamWifiStatus.RxRate = 0
		teamWifiStatus.TxRate = 0
		teamWifiStatus.SignalNoiseRatio = 0
		teamWifiStatus.ConnectionQuality = 0
	} else {
		var err error
		teamWifiStatus.TeamId, err = strconv.Atoi(stationStatus.Ssid)
		if err != nil {
			log.Printf("Failed to parse access point station SSID %q as team ID: %v", stationStatus.Ssid, err)
		}
		teamWifiStatus.RadioLinked = stationStatus.IsLinked
		teamWifiStatus.MBits = stationStatus.BandwidthUsedMbps
		teamWifiStatus.RxRate = stationStatus.RxRateMbps
		teamWifiStatus.TxRate = stationStatus.TxRateMbps
		teamWifiStatus.SignalNoiseRatio = stationStatus.SignalNoiseRatio
		if quality, ok := connectionQualityMap[stationStatus.ConnectionQuality]; ok {
			teamWifiStatus.ConnectionQuality = quality
		} else {
			// Default to 0 if there is no mapping for the connection quality string.
			teamWifiStatus.ConnectionQuality = 0
		}
	}
}

// Returns an abbreviated string representation of the access point status for inclusion in the log.
func (apStatus *accessPointStatus) toLogString() string {
	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("Channel: %d\n", apStatus.Channel))
	for _, station := range []string{"red1", "red2", "red3", "blue1", "blue2", "blue3"} {
		stationStatus := apStatus.StationStatuses[station]
		ssid := "[empty]"
		if stationStatus != nil {
			ssid = stationStatus.Ssid
		}
		buffer.WriteString(fmt.Sprintf("%-6s %s\n", station+":", ssid))
	}
	return buffer.String()
}
