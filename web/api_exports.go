// Exports: matches, scores, rankings and the audit log, as CSV for spreadsheets and JSON for programs. Columns that
// depend on the game (breakdown rows, element points, ranking points) come from the game each match was played
// with, so a spreadsheet of an event stays correct even if the game file has changed since.

package web

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// exportedMatch is one played or scheduled match with everything the exports need.
type exportedMatch struct {
	match   model.Match
	summary *apiMatchSummary
	result  *model.MatchResult
	game    *gamedef.Game
	scored  *scoring.Result
}

// loadExportMatches reads the matches of the requested types (all played types when none is given), in order.
func (web *Web) loadExportMatches(r *http.Request) ([]exportedMatch, error) {
	types := []model.MatchType{model.Practice, model.Qualification, model.Playoff}
	if value := r.URL.Query().Get("type"); value != "" {
		matchType, err := model.MatchTypeFromString(value)
		if err != nil {
			return nil, err
		}
		types = []model.MatchType{matchType}
	}
	var out []exportedMatch
	for _, matchType := range types {
		matches, err := web.arena.Database.GetMatchesByType(matchType, false)
		if err != nil {
			return nil, err
		}
		for i := range matches {
			summary, scored, result, err := web.summarizeMatch(&matches[i])
			if err != nil {
				return nil, err
			}
			entry := exportedMatch{match: matches[i], summary: summary, result: result, scored: scored}
			if result != nil {
				if entry.game, err = web.arena.Database.GetSnapshotGame(result.GameHash); err != nil {
					return nil, err
				}
			}
			out = append(out, entry)
		}
	}
	return out, nil
}

// apiExportHandler serves /api/v1/exports/{file}, where file is matches, scores, rankings or audit, with a .csv or
// .json extension.
func (web *Web) apiExportHandler(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	name, format, found := strings.Cut(file, ".")
	if !found || (format != "csv" && format != "json") {
		writeJsonError(w, http.StatusNotFound, fmt.Sprintf("there is no export %q: use a .csv or .json name", file))
		return
	}
	var table [][]string
	var data any
	var err error
	switch name {
	case "matches":
		table, data, err = web.exportMatches(r)
	case "scores":
		table, data, err = web.exportScores(r)
	case "rankings":
		table, data, err = web.exportRankings()
	case "audit":
		table, data, err = web.exportAudit(r)
	default:
		writeJsonError(w, http.StatusNotFound, fmt.Sprintf("there is no export %q", name))
		return
	}
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	eventCode := strings.ToLower(strings.ReplaceAll(web.arena.EventSettings.Name, " ", "-"))
	filename := fmt.Sprintf("%s-%s-%s.%s", eventCode, name, time.Now().Format("20060102-1504"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if format == "json" {
		body, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			writeJsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	writer := csv.NewWriter(w)
	writer.WriteAll(table)
}

func formatExportNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatExportTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// allianceOrder lists the alliance ids of a match: those of the game it was played with, or the active game's.
func (web *Web) allianceOrder(entry exportedMatch) []string {
	if entry.game != nil {
		return entry.game.AllianceIDs()
	}
	if web.arena.Game != nil {
		return web.arena.Game.AllianceIDs()
	}
	return nil
}

func (web *Web) exportMatches(r *http.Request) ([][]string, any, error) {
	entries, err := web.loadExportMatches(r)
	if err != nil {
		return nil, nil, err
	}
	table := [][]string{{
		"match", "type", "scheduled", "started", "status", "game", "alliance", "teams", "score", "result", "winner",
		"tiebreak",
	}}
	summaries := make([]*apiMatchSummary, 0, len(entries))
	for _, entry := range entries {
		summaries = append(summaries, entry.summary)
		for _, allianceId := range web.allianceOrder(entry) {
			teams := make([]string, 0, 4)
			for _, team := range entry.summary.Teams[allianceId] {
				if team != 0 {
					teams = append(teams, strconv.Itoa(team))
				}
			}
			row := []string{
				entry.match.ShortName, entry.match.Type.String(), formatExportTime(entry.match.Time),
				formatExportTime(entry.match.StartedAt), entry.summary.Status, "", allianceId,
				strings.Join(teams, " "), "", "", "", "",
			}
			if entry.scored != nil {
				alliance := entry.scored.Alliances[allianceId]
				row[5] = entry.result.GameId
				row[8] = formatExportNumber(alliance.Score)
				row[9] = alliance.Result
				row[10] = entry.scored.Winner
				row[11] = entry.scored.Tiebreak
			}
			table = append(table, row)
		}
	}
	return table, map[string]any{"matches": summaries}, nil
}

// exportScores has one row per alliance per played match. Its columns are the union of every game's breakdown rows,
// element and derived points, and ranking points, prefixed so names cannot collide.
func (web *Web) exportScores(r *http.Request) ([][]string, any, error) {
	entries, err := web.loadExportMatches(r)
	if err != nil {
		return nil, nil, err
	}
	columnSet := map[string]bool{}
	type scoreRow struct {
		Match    string             `json:"match"`
		Type     string             `json:"type"`
		Game     string             `json:"game"`
		Alliance string             `json:"alliance"`
		Teams    []int              `json:"teams"`
		Score    float64            `json:"score"`
		Result   string             `json:"result"`
		Values   map[string]float64 `json:"values"`
	}
	var rows []scoreRow
	for _, entry := range entries {
		if entry.scored == nil {
			continue
		}
		for _, allianceId := range web.allianceOrder(entry) {
			alliance := entry.scored.Alliances[allianceId]
			values := map[string]float64{"foul_points": alliance.FoulPoints, "rp_total": alliance.RPTotal}
			for _, row := range alliance.Breakdown {
				values["breakdown."+row.ID] = row.Value
			}
			for id, points := range alliance.Points {
				values["points."+id] = points
			}
			for id, points := range alliance.RP {
				values["rp."+id] = points
			}
			for column := range values {
				columnSet[column] = true
			}
			rows = append(rows, scoreRow{
				Match: entry.match.ShortName, Type: entry.match.Type.String(), Game: entry.result.GameId,
				Alliance: allianceId, Teams: entry.summary.Teams[allianceId], Score: alliance.Score,
				Result: alliance.Result, Values: values,
			})
		}
	}
	columns := make([]string, 0, len(columnSet))
	for column := range columnSet {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	table := [][]string{append([]string{"match", "type", "game", "alliance", "teams", "score", "result"}, columns...)}
	for _, row := range rows {
		teams := make([]string, 0, len(row.Teams))
		for _, team := range row.Teams {
			if team != 0 {
				teams = append(teams, strconv.Itoa(team))
			}
		}
		line := []string{
			row.Match, row.Type, row.Game, row.Alliance, strings.Join(teams, " "), formatExportNumber(row.Score),
			row.Result,
		}
		for _, column := range columns {
			if value, ok := row.Values[column]; ok {
				line = append(line, formatExportNumber(value))
			} else {
				line = append(line, "")
			}
		}
		table = append(table, line)
	}
	if rows == nil {
		rows = []scoreRow{}
	}
	return table, map[string]any{"scores": rows}, nil
}

func (web *Web) exportRankings() ([][]string, any, error) {
	rankings, err := web.arena.Database.GetAllRankings()
	if err != nil {
		return nil, nil, err
	}
	columns, sortIndexes := rankingColumns(web.arena.Game)
	header := []string{"rank", "team", "rp", "played", "wins", "losses", "ties", "disqualifications"}
	for _, column := range columns {
		header = append(header, column.Name)
	}
	table := [][]string{header}
	type rankingRow struct {
		Rank              int                `json:"rank"`
		Team              int                `json:"team"`
		RP                float64            `json:"rp"`
		Played            int                `json:"played"`
		Wins              int                `json:"wins"`
		Losses            int                `json:"losses"`
		Ties              int                `json:"ties"`
		Disqualifications int                `json:"disqualifications"`
		Values            map[string]float64 `json:"values"`
		Stats             map[string]float64 `json:"stats"`
	}
	rows := make([]rankingRow, 0, len(rankings))
	for _, ranking := range rankings {
		line := []string{
			strconv.Itoa(ranking.Rank), strconv.Itoa(ranking.TeamId), formatExportNumber(ranking.RP),
			strconv.Itoa(ranking.Played), strconv.Itoa(ranking.Wins), strconv.Itoa(ranking.Losses),
			strconv.Itoa(ranking.Ties), strconv.Itoa(ranking.Disqualifications),
		}
		values := map[string]float64{}
		for i, column := range columns {
			value := 0.0
			if index := sortIndexes[i]; index < len(ranking.Sort) {
				value = ranking.Sort[index]
			}
			values[column.Name] = value
			line = append(line, formatExportNumber(value))
		}
		table = append(table, line)
		stats := ranking.Stats
		if stats == nil {
			stats = map[string]float64{}
		}
		rows = append(rows, rankingRow{
			Rank: ranking.Rank, Team: ranking.TeamId, RP: ranking.RP, Played: ranking.Played, Wins: ranking.Wins,
			Losses: ranking.Losses, Ties: ranking.Ties, Disqualifications: ranking.Disqualifications,
			Values: values, Stats: stats,
		})
	}
	return table, map[string]any{"rankings": rows}, nil
}

// exportAudit lists every score change of every match: what changed, when, in which phase, and who or what did it.
func (web *Web) exportAudit(r *http.Request) ([][]string, any, error) {
	entries, err := web.loadExportMatches(r)
	if err != nil {
		return nil, nil, err
	}
	table := [][]string{{
		"match", "play", "seq", "time", "phase", "credited_to", "match_time", "alliance", "robot", "element", "op",
		"value", "rule", "reverts", "source_kind", "source_id", "source_name", "confidence",
	}}
	type auditMatch struct {
		Match   string           `json:"match"`
		Play    int              `json:"play"`
		Changes []scoring.Change `json:"changes"`
	}
	matches := []auditMatch{}
	for _, entry := range entries {
		if entry.result == nil {
			continue
		}
		matches = append(matches, auditMatch{
			Match: entry.match.ShortName, Play: entry.result.PlayNumber, Changes: entry.result.Changes,
		})
		for _, change := range entry.result.Changes {
			value := ""
			if change.Value != nil {
				encoded, _ := json.Marshal(change.Value)
				value = strings.Trim(string(encoded), `"`)
			}
			robot, reverts, confidence := "", "", ""
			if change.Robot != 0 {
				robot = strconv.Itoa(change.Robot)
			}
			if change.Reverts != 0 {
				reverts = strconv.Itoa(change.Reverts)
			}
			if change.Source.Confidence != nil {
				confidence = formatExportNumber(*change.Source.Confidence)
			}
			table = append(table, []string{
				entry.match.ShortName, strconv.Itoa(entry.result.PlayNumber), strconv.Itoa(change.Seq),
				formatExportTime(change.Time), change.Phase, change.Bucket,
				strconv.FormatFloat(change.MatchTime, 'f', 3, 64), change.Alliance, robot, change.Element, change.Op,
				value, change.Rule, reverts, change.Source.Kind, change.Source.ID, change.Source.Name, confidence,
			})
		}
	}
	return table, map[string]any{"matches": matches}, nil
}
