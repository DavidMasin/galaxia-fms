// Copyright 2026 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Methods for publishing data to and retrieving data from The Blue Alliance.

package partner

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/rules"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"io"
	"log"
	"maps"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
)

const (
	tbaBaseUrl = "https://www.thebluealliance.com"
	tbaAuthKey = "MAApv9MCuKY9MSFkXLuzTSYBCdosboxDq8Q3ujUE2Mn8PD3Nmv64uczu5Lvy0NQ3"
	AvatarsDir = "static/img/avatars"
)

type TbaClient struct {
	BaseUrl         string
	eventCode       string
	secretId        string
	secret          string
	eventNamesCache map[string]string
}

type TbaMatch struct {
	CompLevel          string                    `json:"comp_level"`
	SetNumber          int                       `json:"set_number"`
	MatchNumber        int                       `json:"match_number"`
	Alliances          map[string]*TbaAlliance   `json:"alliances"`
	ScoreBreakdown     map[string]map[string]any `json:"score_breakdown"`
	TimeString         string                    `json:"time_string"`
	TimeUtc            string                    `json:"time_utc"`
	ActualStartTimeUtc string                    `json:"actual_start_time_utc,omitempty"`
	PostResultsTimeUtc string                    `json:"post_results_time_utc,omitempty"`
	DisplayName        string                    `json:"display_name"`
}

type TbaAlliance struct {
	Teams      []string `json:"teams"`
	Surrogates []string `json:"surrogates"`
	Dqs        []string `json:"dqs"`
	Score      *int     `json:"score"`
}

// TbaRanking is one team's row in the published standings: team_key, rank, wins, losses, ties, dqs and played, plus
// one property per column listed in TbaRankings.Breakdowns. The columns are the ranking order of the game being
// played, so the row cannot be a struct with fixed fields.
type TbaRanking map[string]any

type TbaRankings struct {
	Breakdowns []string     `json:"breakdowns"`
	Rankings   []TbaRanking `json:"rankings"`
}

type TbaTeam struct {
	TeamNumber int    `json:"team_number"`
	Name       string `json:"name"`
	Nickname   string `json:"nickname"`
	City       string `json:"city"`
	StateProv  string `json:"state_prov"`
	Country    string `json:"country"`
	RookieYear int    `json:"rookie_year"`
}

type TbaRobot struct {
	RobotName string `json:"robot_name"`
	Year      int    `json:"year"`
}

type TbaAward struct {
	Name      string `json:"name"`
	EventKey  string `json:"event_key"`
	Year      int    `json:"year"`
	EventName string
}

type TbaEvent struct {
	Name string `json:"name"`
}

type TbaMediaItem struct {
	Details map[string]any `json:"details"`
	Type    string         `json:"type"`
}

type TbaPublishedAward struct {
	TypeEnum *int    `json:"type_enum,omitempty"`
	Name     string  `json:"name_str"`
	TeamKey  *string `json:"team_key"`
	Awardee  *string `json:"awardee"`
}

// Helper to get a pointer to a string.
func stringPtr(s string) *string {
	return &s
}

func NewTbaClient(eventCode, secretId, secret string) *TbaClient {
	return &TbaClient{
		BaseUrl:         tbaBaseUrl,
		eventCode:       eventCode,
		secretId:        secretId,
		secret:          secret,
		eventNamesCache: make(map[string]string),
	}
}

func (client *TbaClient) GetTeam(teamNumber int) (*TbaTeam, error) {
	path := fmt.Sprintf("/api/v3/team/%s", getTbaTeam(teamNumber))
	resp, err := client.getRequest(path)
	if err != nil {
		return nil, err
	}

	// Get the response and handle errors
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var teamData TbaTeam
	err = json.Unmarshal(body, &teamData)

	return &teamData, err
}

func (client *TbaClient) GetRobotName(teamNumber int, year int) (string, error) {
	path := fmt.Sprintf("/api/v3/team/%s/robots", getTbaTeam(teamNumber))
	resp, err := client.getRequest(path)
	if err != nil {
		return "", err
	}

	// Get the response and handle errors
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var robots []*TbaRobot
	err = json.Unmarshal(body, &robots)
	if err != nil {
		return "", err
	}
	for _, robot := range robots {
		if robot.Year == year {
			return robot.RobotName, nil
		}
	}
	return "", nil
}

func (client *TbaClient) GetTeamAwards(teamNumber int) ([]*TbaAward, error) {
	path := fmt.Sprintf("/api/v3/team/%s/awards", getTbaTeam(teamNumber))
	resp, err := client.getRequest(path)
	if err != nil {
		return nil, err
	}

	// Get the response and handle errors
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var awards []*TbaAward
	err = json.Unmarshal(body, &awards)
	if err != nil {
		return nil, err
	}

	for _, award := range awards {
		if _, ok := client.eventNamesCache[award.EventKey]; !ok {
			client.eventNamesCache[award.EventKey], err = client.getEventName(award.EventKey)
			if err != nil {
				return nil, err
			}
		}
		award.EventName = client.eventNamesCache[award.EventKey]
	}

	return awards, nil
}

func (client *TbaClient) DownloadTeamAvatar(teamNumber, year int) error {
	path := fmt.Sprintf("/api/v3/team/%s/media/%d", getTbaTeam(teamNumber), year)
	resp, err := client.getRequest(path)
	if err != nil {
		return err
	}

	// Get the response and handle errors
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var mediaItems []*TbaMediaItem
	err = json.Unmarshal(body, &mediaItems)
	if err != nil {
		return err
	}

	for _, item := range mediaItems {
		if item.Type == "avatar" {
			base64String, ok := item.Details["base64Image"].(string)
			if !ok {
				return fmt.Errorf("Could not interpret avatar response from TBA: %v", item)
			}
			avatarBytes, err := base64.StdEncoding.DecodeString(base64String)
			if err != nil {
				return err
			}

			// Store the avatar to disk as a PNG file.
			avatarPath := fmt.Sprintf("%s/%d.png", AvatarsDir, teamNumber)
			return os.WriteFile(avatarPath, avatarBytes, 0644)
		}
	}

	return nil
}

// Uploads the event team list to The Blue Alliance.
func (client *TbaClient) PublishTeams(database *model.Database) error {
	teams, err := database.GetAllTeams()
	if err != nil {
		return err
	}

	// Build a JSON array of TBA-format team keys (e.g. "frc254").
	teamKeys := make([]string, len(teams))
	for i, team := range teams {
		teamKeys[i] = getTbaTeam(team.Id)
	}
	jsonBody, err := json.Marshal(teamKeys)
	if err != nil {
		return err
	}

	resp, err := client.postRequest("team_list", "update", jsonBody)
	if err != nil {
		return err
	}
	return checkTbaPostResponse(resp)
}

// Uploads the practice, qualification, and playoff match schedule and results to The Blue Alliance.
func (client *TbaClient) PublishMatches(database *model.Database) error {
	practiceMatches, err := database.GetMatchesByType(model.Practice, false)
	if err != nil {
		return err
	}
	qualMatches, err := database.GetMatchesByType(model.Qualification, false)
	if err != nil {
		return err
	}
	playoffMatches, err := database.GetMatchesByType(model.Playoff, false)
	if err != nil {
		return err
	}
	matches := append(practiceMatches, qualMatches...)
	matches = append(matches, playoffMatches...)
	tbaMatches := make([]TbaMatch, len(matches))

	// Build a JSON array of TBA-format matches.
	for i, match := range matches {
		// Fill in scores if the match has been played (excluding practice matches, which only publish timing and lineups).
		var scoreBreakdown map[string]map[string]any
		var redScore, blueScore *int
		var redDisqualified, blueDisqualified [3]bool
		if match.IsComplete() && match.Type != model.Practice {
			matchResult, err := database.GetMatchResultForMatch(match.Id)
			if err != nil {
				return err
			}
			if matchResult != nil {
				result, g, err := matchResult.Compute(database, match.UseTiebreakCriteria)
				if err != nil {
					return err
				}

				// The Blue Alliance only knows a red and a blue side. They are the first and second alliance of the
				// game the match was played with, whatever that game calls them.
				redId, blueId, err := matchResult.AllianceIds(database)
				if err != nil {
					return err
				}
				redScore = tbaScore(result.Alliances[redId])
				blueScore = tbaScore(result.Alliances[blueId])

				// The raw state is only a cache of the change list, so a result may come without it.
				state := matchResult.State
				if state == nil {
					state, _ = scoring.Fold(g, matchResult.Changes)
				}
				redDisqualified = tbaDisqualifications(g, &match, result.Alliances[redId], state.Alliances[redId])
				blueDisqualified = tbaDisqualifications(g, &match, result.Alliances[blueId], state.Alliances[blueId])

				// The Blue Alliance rejects breakdown keys it does not know for the event's year, so a game that
				// does not map itself onto them is published with its scores only.
				if len(g.TBA.Breakdown) > 0 {
					scoreBreakdown = make(map[string]map[string]any)
					scoreBreakdown["red"], err = createTbaScoringBreakdown(g, result, redId)
					if err != nil {
						return err
					}
					scoreBreakdown["blue"], err = createTbaScoringBreakdown(g, result, blueId)
					if err != nil {
						return err
					}
				}
			}
		}
		alliances := make(map[string]*TbaAlliance)
		alliances["red"] = createTbaAlliance(
			[3]int{match.Red1, match.Red2, match.Red3},
			[3]bool{match.Red1IsSurrogate, match.Red2IsSurrogate, match.Red3IsSurrogate},
			redScore,
			redDisqualified,
		)
		alliances["blue"] = createTbaAlliance(
			[3]int{match.Blue1, match.Blue2, match.Blue3},
			[3]bool{match.Blue1IsSurrogate, match.Blue2IsSurrogate, match.Blue3IsSurrogate},
			blueScore,
			blueDisqualified,
		)

		var actualStartTimeUtc string
		if !match.StartedAt.IsZero() {
			actualStartTimeUtc = match.StartedAt.UTC().Format("2006-01-02T15:04:05")
		}
		var postResultsTimeUtc string
		if !match.ScoreCommittedAt.IsZero() {
			postResultsTimeUtc = match.ScoreCommittedAt.UTC().Format("2006-01-02T15:04:05")
		}

		compLevel := match.TbaMatchKey.CompLevel
		if match.Type == model.Practice || compLevel == "p" {
			compLevel = "pm"
		}

		tbaMatches[i] = TbaMatch{
			CompLevel:          compLevel,
			SetNumber:          match.TbaMatchKey.SetNumber,
			MatchNumber:        match.TbaMatchKey.MatchNumber,
			Alliances:          alliances,
			ScoreBreakdown:     scoreBreakdown,
			TimeString:         match.Time.Local().Format("3:04 PM"),
			TimeUtc:            match.Time.UTC().Format("2006-01-02T15:04:05"),
			ActualStartTimeUtc: actualStartTimeUtc,
			PostResultsTimeUtc: postResultsTimeUtc,
		}
	}
	jsonBody, err := json.Marshal(tbaMatches)
	if err != nil {
		return err
	}

	resp, err := client.postRequest("matches", "update", jsonBody)
	if err != nil {
		return err
	}
	return checkTbaPostResponse(resp)
}

// Uploads the team standings to The Blue Alliance. The columns are the ranking order of the given game, which must be
// the event's active game: the one the stored rankings were sorted by.
func (client *TbaClient) PublishRankings(database *model.Database, g *gamedef.Game) error {
	rankings, err := database.GetAllRankings()
	if err != nil {
		return err
	}

	// Every key of the game's ranking order is a column, except the coin flip that settles what is left tied. A
	// column is published as a property of each team's row, next to the fixed ones, so its name has to be its own.
	breakdowns := []string{}
	var sortIndices []int
	taken := map[string]bool{
		"team_key": true, "rank": true, "wins": true, "losses": true, "ties": true, "dqs": true, "played": true,
	}
	for i, key := range g.Ranking.Order {
		if key.Random {
			continue
		}
		if taken[key.Name] {
			return fmt.Errorf(
				"ranking column %q cannot be published to The Blue Alliance: the name is already in use", key.Name,
			)
		}
		taken[key.Name] = true
		breakdowns = append(breakdowns, key.Name)
		sortIndices = append(sortIndices, i)
	}

	// Build a JSON object of TBA-format rankings.
	tbaRankings := make([]TbaRanking, len(rankings))
	for i, ranking := range rankings {
		tbaRankings[i] = TbaRanking{
			"team_key": getTbaTeam(ranking.TeamId),
			"rank":     ranking.Rank,
			"wins":     ranking.Wins,
			"losses":   ranking.Losses,
			"ties":     ranking.Ties,
			"dqs":      ranking.Disqualifications,
			"played":   ranking.Played,
		}
		for j, sortIndex := range sortIndices {
			// Rankings stored before the game's ranking order grew have no value for the newer columns.
			value := 0.0
			if sortIndex < len(ranking.Sort) {
				value = ranking.Sort[sortIndex]
			}
			tbaRankings[i][breakdowns[j]] = value
		}
	}
	jsonBody, err := json.Marshal(TbaRankings{breakdowns, tbaRankings})
	if err != nil {
		return err
	}

	resp, err := client.postRequest("rankings", "update", jsonBody)
	if err != nil {
		return err
	}
	return checkTbaPostResponse(resp)
}

// Uploads the alliances selection results to The Blue Alliance.
func (client *TbaClient) PublishAlliances(database *model.Database) error {
	alliances, err := database.GetAllAlliances()
	if err != nil {
		return err
	}

	// Build a JSON object of TBA-format alliances.
	tbaAlliances := make([][]string, len(alliances))
	for i, alliance := range alliances {
		for _, allianceTeamId := range alliance.TeamIds {
			tbaAlliances[i] = append(tbaAlliances[i], getTbaTeam(allianceTeamId))
		}
	}
	jsonBody, err := json.Marshal(tbaAlliances)
	if err != nil {
		return err
	}

	resp, err := client.postRequest("alliance_selections", "update", jsonBody)
	if err != nil {
		return err
	}
	if err := checkTbaPostResponse(resp); err != nil {
		return err
	}

	// Also set the playoff type so that TBA renders the correct bracket.
	eventSettings, err := database.GetEventSettings()
	if err != nil {
		return err
	}
	playoffType := 0
	if eventSettings.PlayoffType == model.DoubleEliminationPlayoff {
		playoffType = 10
	}
	resp, err = client.postRequest("info", "update", []byte(fmt.Sprintf("{\"playoff_type\":%d}", playoffType)))
	if err != nil {
		return err
	}
	return checkTbaPostResponse(resp)
}

// Uploads the awards to The Blue Alliance.
func (client *TbaClient) PublishAwards(database *model.Database) error {
	awards, err := database.GetAllAwards()
	if err != nil {
		return err
	}

	// Build a JSON array of TBA-format award models.
	tbaAwards := make([]TbaPublishedAward, len(awards))
	for i, award := range awards {
		tbaAwards[i].TypeEnum = award.TbaTypeEnum
		tbaAwards[i].Name = award.AwardName
		if award.TeamId > 0 {
			tbaAwards[i].TeamKey = stringPtr(getTbaTeam(award.TeamId))
		}
		if trimmed := strings.TrimSpace(award.PersonName); trimmed != "" {
			tbaAwards[i].Awardee = stringPtr(trimmed)
		}
	}
	jsonBody, err := json.Marshal(tbaAwards)
	if err != nil {
		return err
	}

	resp, err := client.postRequest("awards", "update", jsonBody)
	if err != nil {
		return err
	}
	return checkTbaPostResponse(resp)
}

// Clears out the existing match data on The Blue Alliance for the event.
func (client *TbaClient) DeletePublishedMatches() error {
	resp, err := client.postRequest("matches", "delete_all", []byte(client.eventCode))
	if err != nil {
		return err
	}
	return checkTbaPostResponse(resp)
}

func checkTbaPostResponse(resp *http.Response) error {
	if resp.StatusCode != 200 {
		body, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if err != nil {
			return fmt.Errorf("Got status code %d from TBA and failed to read response body: %w", resp.StatusCode, err)
		}
		if closeErr != nil {
			return fmt.Errorf("Got status code %d from TBA: %s; failed to close response body: %w",
				resp.StatusCode, body, closeErr)
		}
		return fmt.Errorf("Got status code %d from TBA: %s", resp.StatusCode, body)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("Failed to close TBA response body: %w", err)
	}
	return nil
}

func (client *TbaClient) getEventName(eventCode string) (string, error) {
	path := fmt.Sprintf("/api/v3/event/%s", eventCode)
	resp, err := client.getRequest(path)
	if err != nil {
		return "", err
	}

	// Get the response and handle errors
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var event TbaEvent
	err = json.Unmarshal(body, &event)
	if err != nil {
		return "", err
	}

	return event.Name, err
}

// Converts an integer team number into the "frcXXXX" format TBA expects.
func getTbaTeam(team int) string {
	return fmt.Sprintf("frc%d", team)
}

// Sends a GET request to the TBA API.
func (client *TbaClient) getRequest(path string) (*http.Response, error) {
	url := client.BaseUrl + path

	// Make an HTTP GET request with the TBA auth headers.
	httpClient := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-TBA-Auth-Key", tbaAuthKey)
	return httpClient.Do(req)
}

// Signs the request and sends it to the TBA API.
func (client *TbaClient) postRequest(resource string, action string, body []byte) (*http.Response, error) {
	path := fmt.Sprintf("/api/trusted/v1/event/%s/%s/%s", client.eventCode, resource, action)
	signature := fmt.Sprintf("%x", md5.Sum(append([]byte(client.secret+path), body...)))

	httpClient := &http.Client{}
	request, err := http.NewRequest("POST", client.BaseUrl+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Add("X-TBA-Auth-Id", client.secretId)
	request.Header.Add("X-TBA-Auth-Sig", signature)
	response, err := httpClient.Do(request)
	if client.BaseUrl == tbaBaseUrl && err == nil && response.StatusCode == 200 {
		// Send a non-blocking ping to track usage.
		pingRequest, pingErr := http.NewRequest(
			"POST", fmt.Sprintf("https://cheesyarena.com/events/%s/%s", client.eventCode, resource), nil,
		)
		if pingErr != nil {
			log.Printf("Failed to create TBA usage ping request: %v", pingErr)
		} else {
			pingResponse, pingErr := httpClient.Do(pingRequest)
			if pingErr != nil {
				log.Printf("Failed to send TBA usage ping: %v", pingErr)
			} else if err := pingResponse.Body.Close(); err != nil {
				log.Printf("Failed to close TBA usage ping response body: %v", err)
			}
		}
	}
	return response, err
}

func createTbaAlliance(teamIds [3]int, surrogates [3]bool, score *int, disqualified [3]bool) *TbaAlliance {
	alliance := TbaAlliance{Teams: []string{}, Surrogates: []string{}, Dqs: []string{}, Score: score}
	for i, teamId := range teamIds {
		if teamId == 0 {
			continue
		}
		teamKey := getTbaTeam(teamId)
		alliance.Teams = append(alliance.Teams, teamKey)
		if surrogates[i] {
			alliance.Surrogates = append(alliance.Surrogates, teamKey)
		}
		if disqualified[i] {
			alliance.Dqs = append(alliance.Dqs, teamKey)
		}
	}

	return &alliance
}

// Returns the score to publish for an alliance. The Blue Alliance only takes whole points, while a game may award
// fractions of them.
func tbaScore(allianceResult *scoring.AllianceResult) *int {
	score := int(math.Round(allianceResult.Score))
	return &score
}

// Returns which of an alliance's three stations in the match record hold a team that is disqualified from the match.
// A disqualified alliance takes all of its teams with it, which is also what a disqualifying card comes to in a
// playoff match: there the result already holds it against the whole alliance. In any other match such a card only
// takes out the team it was given to.
func tbaDisqualifications(
	g *gamedef.Game, match *model.Match, allianceResult *scoring.AllianceResult, allianceState *scoring.AllianceState,
) [3]bool {
	if allianceResult.Disqualified {
		return [3]bool{true, true, true}
	}
	var disqualified [3]bool
	if match.Type != model.Playoff {
		for _, position := range model.DisqualifiedPositions(g, allianceState) {
			// A game may field more robots per alliance than a match record has stations.
			if position <= len(disqualified) {
				disqualified[position-1] = true
			}
		}
	}
	return disqualified
}

// Builds the score breakdown published for one alliance by evaluating every expression of the game's mapping onto
// The Blue Alliance's breakdown keys against the computed result.
func createTbaScoringBreakdown(g *gamedef.Game, result *scoring.Result, allianceId string) (map[string]any, error) {
	values := make(map[string]any, len(g.TBA.Breakdown))
	extra := map[string]any{"result": result.Alliances[allianceId].Result}

	// Keys are visited in order so that a mistake in a game definition is reported the same way every time.
	for _, key := range slices.Sorted(maps.Keys(g.TBA.Breakdown)) {
		expression := g.TBA.Breakdown[key]
		value, err := result.Eval(expression.Prog, allianceId, scoring.Clock{}, extra)
		if err == nil {
			values[key], err = tbaBreakdownValue(value)
		}
		if err != nil {
			return nil, fmt.Errorf(
				"tba breakdown key %q (line %d of the game definition): %w", key, expression.Line, err,
			)
		}
	}
	return nestTbaBreakdown(values)
}

// Converts the result of a breakdown expression into the value published for it. The game engine computes with
// floating point numbers, but The Blue Alliance expects counts and points as integers, so whole numbers are published
// as integers.
func tbaBreakdownValue(value any) (any, error) {
	switch value.(type) {
	case bool, string:
		return value, nil
	case nil:
		return nil, fmt.Errorf("the expression gave no value")
	}
	number, ok := rules.ToNumber(value)
	if !ok {
		return nil, fmt.Errorf(
			"the expression gave %v (%T); only numbers, booleans and text can be published", value, value,
		)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("the expression gave %v, which is not a finite number", number)
	}
	// Beyond 2^53 a float no longer identifies one integer, so such a number is left as it is.
	if number == math.Trunc(number) && math.Abs(number) < 1<<53 {
		return int(number), nil
	}
	return number, nil
}

// Turns breakdown keys containing dots into nested objects, which is how The Blue Alliance groups related values:
// "a.b" and "a.c" are published as {"a": {"b": ..., "c": ...}}.
func nestTbaBreakdown(values map[string]any) (map[string]any, error) {
	breakdown := make(map[string]any, len(values))
	for _, key := range slices.Sorted(maps.Keys(values)) {
		names := strings.Split(key, ".")
		if slices.Contains(names, "") {
			return nil, fmt.Errorf("tba breakdown key %q has an empty name before or after a dot", key)
		}
		object := breakdown
		for i, name := range names[:len(names)-1] {
			// A key cannot hold a value of its own and also be the object that holds other keys. Publishing either
			// would silently drop the other, so the game definition has to say which one is meant.
			parent := strings.Join(names[:i+1], ".")
			if _, isValue := values[parent]; isValue {
				return nil, fmt.Errorf("tba breakdown key %q is both a value and the parent of %q", parent, key)
			}
			child, ok := object[name].(map[string]any)
			if !ok {
				child = make(map[string]any)
				object[name] = child
			}
			object = child
		}
		object[names[len(names)-1]] = values[key]
	}
	return breakdown, nil
}
