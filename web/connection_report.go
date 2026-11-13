// Connection reports: a summary of how well a robot stayed connected through a match, built from its match log.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/diag"
	"github.com/DavidMasin/galaxia-fms/model"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// connectionReport analyses one log file's rows.
func connectionReport(log MatchLog) *diag.ConnectionReport {
	samples := make([]diag.Sample, 0, len(log.Rows))
	for _, row := range log.Rows {
		samples = append(samples, diag.Sample{
			MatchTimeSec: row.MatchTimeSec, DsLinked: row.DsLinked, RadioLinked: row.RadioLinked,
			RioLinked: row.RioLinked, RobotLinked: row.RobotLinked, Enabled: row.Enabled,
			BatteryVoltage: row.BatteryVoltage, MissedPacketCount: row.MissedPacketCount,
			TripTimeMs: row.DsRobotTripTimeMs, RxRate: row.RxRate, TxRate: row.TxRate,
			SignalNoiseRatio: row.SignalNoiseRatio,
		})
	}
	return diag.AnalyzeConnection(samples)
}

// apiConnectionReportHandler returns the connection report for every logged play of one station in one match.
func (web *Web) apiConnectionReportHandler(w http.ResponseWriter, r *http.Request) {
	match, logs, _, err := web.getMatchLogFromRequest(r)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if match == nil || logs == nil {
		writeJsonError(w, http.StatusNotFound, "no team played in that station in that match")
		return
	}
	type play struct {
		StartTime string                 `json:"startTime"`
		File      string                 `json:"file"`
		Report    *diag.ConnectionReport `json:"report"`
	}
	plays := make([]play, 0, len(logs.Logs))
	for _, log := range logs.Logs {
		plays = append(plays, play{StartTime: log.StartTime, File: "/" + log.Filename, Report: connectionReport(log)})
	}
	writeJson(w, http.StatusOK, map[string]any{
		"match": match.ShortName, "station": logs.AllianceStation, "team": logs.TeamId, "plays": plays,
	})
}

// teamConnectionReportHandler sends a team to the connection report of the last match it played. Field monitors show
// a QR code of this address next to each team, so it never goes stale as matches move on.
func (web *Web) teamConnectionReportHandler(w http.ResponseWriter, r *http.Request) {
	team, err := strconv.Atoi(r.PathValue("team"))
	if err != nil || team <= 0 {
		http.Error(w, "The team number must be a positive number.", http.StatusBadRequest)
		return
	}
	files, err := filepath.Glob(filepath.Join(".", "static", "logs", "*_*_Match_*_"+strconv.Itoa(team)+".csv"))
	if err != nil {
		handleWebErr(w, err)
		return
	}
	// File names start with the time the log was started, so the last in order is the newest.
	sort.Strings(files)
	for i := len(files) - 1; i >= 0; i-- {
		name := filepath.Base(files[i])
		parts := strings.Split(strings.TrimSuffix(name, ".csv"), "_")
		if len(parts) < 5 {
			continue
		}
		shortName := strings.Join(parts[3:len(parts)-1], "_")
		matchType, err := model.MatchTypeFromString(parts[1])
		if err != nil {
			continue
		}
		matches, err := web.arena.Database.GetMatchesByType(matchType, true)
		if err != nil {
			handleWebErr(w, err)
			return
		}
		for _, match := range matches {
			if match.ShortName != shortName {
				continue
			}
			sides := map[string]int{
				"R1": match.Red1, "R2": match.Red2, "R3": match.Red3, "B1": match.Blue1, "B2": match.Blue2,
				"B3": match.Blue3,
			}
			for station, stationTeam := range sides {
				if stationTeam == team {
					http.Redirect(w, r, fmt.Sprintf("/match_logs/%d/%s/log", match.Id, station), http.StatusSeeOther)
					return
				}
			}
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "No match log has been recorded for team %d yet. It appears after the team plays a match.\n", team)
}
