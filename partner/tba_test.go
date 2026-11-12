// Copyright 2026 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package partner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testReferee = scoring.Source{Kind: scoring.SourceReferee, ID: "test"}

func TestPublishTeams(t *testing.T) {
	database := setupTestDb(t)

	database.CreateTeam(&model.Team{Id: 254})
	database.CreateTeam(&model.Team{Id: 1114})

	// Mock the TBA server.
	tbaServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.String(), "event/my_event_code")
				var reader bytes.Buffer
				reader.ReadFrom(r.Body)
				assert.Equal(t, "[\"frc254\",\"frc1114\"]", reader.String())
				assert.Equal(t, "my_secret_id", r.Header["X-Tba-Auth-Id"][0])
				assert.Equal(t, "f5c022fde6d1186ea0719fe28ab6cc63", r.Header["X-Tba-Auth-Sig"][0])
			},
		),
	)
	defer tbaServer.Close()
	client := NewTbaClient("my_event_code", "my_secret_id", "my_secret")
	client.BaseUrl = tbaServer.URL

	assert.Nil(t, client.PublishTeams(database))
}

func TestPublishMatches(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	practiceMatch := model.Match{
		Type:             model.Practice,
		ShortName:        "P1",
		Time:             time.Unix(300, 0),
		StartedAt:        time.Unix(310, 0),
		ScoreCommittedAt: time.Unix(450, 0),
		Red1:             1,
		Red2:             2,
		Red3:             3,
		Blue1:            4,
		Blue2:            5,
		Blue3:            6,
		Status:           model.RedWonMatch,
		TbaMatchKey:      model.TbaMatchKey{CompLevel: "p", SetNumber: 0, MatchNumber: 1},
	}
	match1 := model.Match{
		Type:             model.Qualification,
		ShortName:        "Q2",
		Time:             time.Unix(600, 0),
		StartedAt:        time.Unix(620, 0),
		ScoreCommittedAt: time.Unix(780, 0),
		Red1:             7,
		Red2:             8,
		Red2IsSurrogate:  true,
		Red3:             9,
		Blue1:            10,
		Blue2:            11,
		Blue3:            12,
		Status:           model.BlueWonMatch,
		TbaMatchKey:      model.TbaMatchKey{CompLevel: "qm", SetNumber: 0, MatchNumber: 2},
	}
	match2 := model.Match{
		Type:        model.Playoff,
		ShortName:   "SF2-2",
		TbaMatchKey: model.TbaMatchKey{CompLevel: "omg", SetNumber: 5, MatchNumber: 29},
	}
	database.CreateMatch(&practiceMatch)
	database.CreateMatch(&match1)
	database.CreateMatch(&match2)

	// A practice match only publishes its timing and lineup, whatever its result holds.
	practiceMatchResult := model.BuildTestMatchResult(database, practiceMatch.Id, 1)
	practiceMatchResult.MatchType = model.Practice
	_, err := practiceMatchResult.Edit(
		g,
		scoring.Change{
			Alliance: "red", Robot: 1, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "red",
			Source: testReferee,
		},
	)
	assert.Nil(t, err)
	database.CreateMatchResult(practiceMatchResult)
	matchResult1 := model.BuildTestMatchResult(database, match1.Id, 1)
	database.CreateMatchResult(matchResult1)

	var matches []json.RawMessage
	assert.Nil(t, json.Unmarshal(publishMatches(t, database), &matches))
	if !assert.Equal(t, 3, len(matches)) {
		return
	}
	assert.JSONEq(
		t,
		fmt.Sprintf(
			`{
				"comp_level": "pm",
				"set_number": 0,
				"match_number": 1,
				"alliances": {
					"red": {"teams": ["frc1", "frc2", "frc3"], "surrogates": [], "dqs": [], "score": null},
					"blue": {"teams": ["frc4", "frc5", "frc6"], "surrogates": [], "dqs": [], "score": null}
				},
				"score_breakdown": null,
				"time_string": %q,
				"time_utc": "1970-01-01T00:05:00",
				"actual_start_time_utc": "1970-01-01T00:05:10",
				"post_results_time_utc": "1970-01-01T00:07:30",
				"display_name": ""
			}`,
			time.Unix(300, 0).Local().Format("3:04 PM"),
		),
		string(matches[0]),
	)

	// The breakdown has exactly the keys of the game's mapping onto The Blue Alliance. The numbers are the first
	// worked example in the game's tests.yaml.
	assert.JSONEq(
		t,
		fmt.Sprintf(
			`{
				"comp_level": "qm",
				"set_number": 0,
				"match_number": 2,
				"alliances": {
					"red": {"teams": ["frc7", "frc8", "frc9"], "surrogates": ["frc8"], "dqs": [], "score": 133},
					"blue": {"teams": ["frc10", "frc11", "frc12"], "surrogates": [], "dqs": [], "score": 289}
				},
				"score_breakdown": {
					"red": {
						"autoTowerPoints": 15,
						"endGameTowerPoints": 30,
						"totalTowerPoints": 45,
						"minorFoulCount": 2,
						"majorFoulCount": 5,
						"foulPoints": 0,
						"totalAutoPoints": 33,
						"totalTeleopPoints": 100,
						"totalPoints": 133,
						"energizedAchieved": false,
						"superchargedAchieved": false,
						"traversalAchieved": false,
						"g206Penalty": false,
						"rp": 0
					},
					"blue": {
						"autoTowerPoints": 30,
						"endGameTowerPoints": 60,
						"totalTowerPoints": 90,
						"minorFoulCount": 0,
						"majorFoulCount": 0,
						"foulPoints": 85,
						"totalAutoPoints": 65,
						"totalTeleopPoints": 139,
						"totalPoints": 289,
						"energizedAchieved": true,
						"superchargedAchieved": false,
						"traversalAchieved": true,
						"g206Penalty": false,
						"rp": 5
					}
				},
				"time_string": %q,
				"time_utc": "1970-01-01T00:10:00",
				"actual_start_time_utc": "1970-01-01T00:10:20",
				"post_results_time_utc": "1970-01-01T00:13:00",
				"display_name": ""
			}`,
			time.Unix(600, 0).Local().Format("3:04 PM"),
		),
		string(matches[1]),
	)

	assert.JSONEq(
		t,
		fmt.Sprintf(
			`{
				"comp_level": "omg",
				"set_number": 5,
				"match_number": 29,
				"alliances": {
					"red": {"teams": [], "surrogates": [], "dqs": [], "score": null},
					"blue": {"teams": [], "surrogates": [], "dqs": [], "score": null}
				},
				"score_breakdown": null,
				"time_string": %q,
				"time_utc": "0001-01-01T00:00:00",
				"display_name": ""
			}`,
			time.Time{}.Local().Format("3:04 PM"),
		),
		string(matches[2]),
	)
}

func TestPublishMatchDisqualifications(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	// Every match has the same lineup and starts from the same result: red 133, blue 289, and a yellow card for the
	// robot at red's first position, which disqualifies nobody.
	createMatch := func(matchType model.MatchType, typeOrder int, status model.MatchStatus, changes ...scoring.Change) {
		match := model.Match{
			Type: matchType, TypeOrder: typeOrder, Red1: 7, Red2: 8, Red3: 9, Blue1: 10, Blue2: 11, Blue3: 12,
			Status: status,
		}
		assert.Nil(t, database.CreateMatch(&match))
		matchResult := model.BuildTestMatchResult(database, match.Id, 1)
		matchResult.MatchType = matchType
		for _, change := range changes {
			_, err := matchResult.Edit(g, change)
			assert.Nil(t, err)
		}
		assert.Nil(t, database.CreateMatchResult(matchResult))
	}
	redCard := scoring.Change{
		Alliance: "blue", Robot: 3, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "red", Source: testReferee,
	}
	createMatch(model.Qualification, 1, model.BlueWonMatch, redCard)
	createMatch(
		model.Qualification,
		2,
		model.BlueWonMatch,
		scoring.Change{
			Alliance: "red", Element: scoring.TargetDisqualified, Op: scoring.OpSet, Value: true, Source: testReferee,
		},
	)
	createMatch(model.Playoff, 1, model.RedWonMatch, redCard)

	var matches []TbaMatch
	assert.Nil(t, json.Unmarshal(publishMatches(t, database), &matches))
	if !assert.Equal(t, 3, len(matches)) {
		return
	}
	redTeams := []string{"frc7", "frc8", "frc9"}
	blueTeams := []string{"frc10", "frc11", "frc12"}
	alliance := func(teams []string, dqs []string, score int) *TbaAlliance {
		return &TbaAlliance{Teams: teams, Surrogates: []string{}, Dqs: dqs, Score: &score}
	}

	// In a qualification match a red card disqualifies the team that holds it. Its alliance keeps its score.
	assert.Equal(t, alliance(redTeams, []string{}, 133), matches[0].Alliances["red"])
	assert.Equal(t, alliance(blueTeams, []string{"frc12"}, 289), matches[0].Alliances["blue"])

	// An alliance disqualified as a whole takes all of its teams with it and scores nothing.
	assert.Equal(t, alliance(redTeams, redTeams, 0), matches[1].Alliances["red"])
	assert.Equal(t, alliance(blueTeams, []string{}, 289), matches[1].Alliances["blue"])

	// In a playoff match the same red card disqualifies the whole alliance.
	assert.Equal(t, alliance(redTeams, []string{}, 133), matches[2].Alliances["red"])
	assert.Equal(t, alliance(blueTeams, blueTeams, 0), matches[2].Alliances["blue"])
}

func TestPublishMatchesWithoutBreakdownMapping(t *testing.T) {
	database := setupTestDb(t)

	// This game does not say how it maps onto The Blue Alliance's score breakdown.
	g := model.TestGame("ball-count")
	assert.Empty(t, g.TBA.Breakdown)
	assert.Nil(t, database.SaveGameSnapshot(g))
	match := model.Match{
		Type:        model.Qualification,
		ShortName:   "Q1",
		Time:        time.Unix(600, 0),
		Red1:        1,
		Red2:        2,
		Red3:        3,
		Blue1:       4,
		Blue2:       5,
		Blue3:       6,
		Status:      model.RedWonMatch,
		TbaMatchKey: model.TbaMatchKey{CompLevel: "qm", SetNumber: 0, MatchNumber: 1},
	}
	assert.Nil(t, database.CreateMatch(&match))
	matchResult := model.NewMatchResult(g)
	matchResult.MatchId = match.Id
	matchResult.PlayNumber = 1
	matchResult.MatchType = model.Qualification
	editBalls(t, g, matchResult, "red", "auto", 3)
	editBalls(t, g, matchResult, "red", "teleop", 10)
	editBalls(t, g, matchResult, "blue", "teleop", 4)
	assert.Nil(t, database.CreateMatchResult(matchResult))

	// The scores are published all the same, without a breakdown.
	var matches []json.RawMessage
	assert.Nil(t, json.Unmarshal(publishMatches(t, database), &matches))
	if !assert.Equal(t, 1, len(matches)) {
		return
	}
	assert.JSONEq(
		t,
		fmt.Sprintf(
			`{
				"comp_level": "qm",
				"set_number": 0,
				"match_number": 1,
				"alliances": {
					"red": {"teams": ["frc1", "frc2", "frc3"], "surrogates": [], "dqs": [], "score": 16},
					"blue": {"teams": ["frc4", "frc5", "frc6"], "surrogates": [], "dqs": [], "score": 4}
				},
				"score_breakdown": null,
				"time_string": %q,
				"time_utc": "1970-01-01T00:10:00",
				"display_name": ""
			}`,
			time.Unix(600, 0).Local().Format("3:04 PM"),
		),
		string(matches[0]),
	)
}

func TestCreateTbaScoringBreakdown(t *testing.T) {
	setupTestDb(t)
	ballCount := string(model.TestGame("ball-count").Source)

	// Builds the game from the smallest example plus the given mapping, and scores the same match with it: red
	// wins 16 to 4.
	compute := func(tbaSection string) (*gamedef.Game, *scoring.Result) {
		g, diagnostics := gamedef.Parse([]byte(ballCount + tbaSection))
		if !assert.NotNil(t, g, "%v", diagnostics) {
			t.FailNow()
		}
		matchResult := model.NewMatchResult(g)
		editBalls(t, g, matchResult, "red", "auto", 3)
		editBalls(t, g, matchResult, "red", "teleop", 10)
		editBalls(t, g, matchResult, "blue", "teleop", 4)
		result, err := scoring.Compute(g, matchResult.State, scoring.Options{})
		if !assert.Nil(t, err) {
			t.FailNow()
		}
		return g, result
	}

	// A key with dots becomes a nested object. Whole numbers become integers even when the engine computed them as
	// floats; other values are published as they are.
	g, result := compute(`
tba:
  breakdown:
    totalPoints: "own.score"
    balls.auto: "own.balls.auto"
    balls.points.teleop: "own.phase_points.balls.teleop"
    balls.perRobot: "own.balls.total / 4"
    alliance: "own.id"
    won: "result == 'win'"
`)
	breakdown, err := createTbaScoringBreakdown(g, result, "red")
	assert.Nil(t, err)
	assert.Equal(
		t,
		map[string]any{
			"totalPoints": 16,
			"balls":       map[string]any{"auto": 3, "points": map[string]any{"teleop": 10}, "perRobot": 3.25},
			"alliance":    "red",
			"won":         true,
		},
		breakdown,
	)
	breakdown, err = createTbaScoringBreakdown(g, result, "blue")
	assert.Nil(t, err)
	assert.Equal(
		t,
		map[string]any{
			"totalPoints": 4,
			"balls":       map[string]any{"auto": 0, "points": map[string]any{"teleop": 4}, "perRobot": 1},
			"alliance":    "blue",
			"won":         false,
		},
		breakdown,
	)

	// A key cannot be a value and an object at once.
	g, result = compute(`
tba:
  breakdown:
    balls: "own.balls.total"
    balls.auto: "own.balls.auto"
`)
	_, err = createTbaScoringBreakdown(g, result, "red")
	if assert.NotNil(t, err) {
		assert.Equal(t, `tba breakdown key "balls" is both a value and the parent of "balls.auto"`, err.Error())
	}

	// Only numbers, booleans and text can be published, and the error says which key gave something else.
	g, result = compute(`
tba:
  breakdown:
    totalPoints: "own.score"
    robots: "own.robots"
`)
	_, err = createTbaScoringBreakdown(g, result, "red")
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), `tba breakdown key "robots" (line `)
		assert.Contains(t, err.Error(), "only numbers, booleans and text can be published")
	}
}

func TestNestTbaBreakdown(t *testing.T) {
	breakdown, err := nestTbaBreakdown(map[string]any{"a": 1, "b.c": 2, "b.d.e": "three", "b.d.f": true})
	assert.Nil(t, err)
	assert.Equal(
		t,
		map[string]any{"a": 1, "b": map[string]any{"c": 2, "d": map[string]any{"e": "three", "f": true}}},
		breakdown,
	)

	breakdown, err = nestTbaBreakdown(map[string]any{})
	assert.Nil(t, err)
	assert.Empty(t, breakdown)

	// The clash is reported for the key that is both, at whatever depth it sits.
	_, err = nestTbaBreakdown(map[string]any{"a.b": 1, "a.b.c": 2, "a.d": 3})
	if assert.NotNil(t, err) {
		assert.Equal(t, `tba breakdown key "a.b" is both a value and the parent of "a.b.c"`, err.Error())
	}
	_, err = nestTbaBreakdown(map[string]any{"a": 1, "a.b.c": 2})
	if assert.NotNil(t, err) {
		assert.Equal(t, `tba breakdown key "a" is both a value and the parent of "a.b.c"`, err.Error())
	}

	for _, key := range []string{"", ".a", "a.", "a..b"} {
		_, err = nestTbaBreakdown(map[string]any{key: 1})
		if assert.NotNil(t, err, key) {
			assert.Contains(t, err.Error(), "empty name")
		}
	}
}

func TestTbaBreakdownValue(t *testing.T) {
	for _, testCase := range []struct {
		value    any
		expected any
	}{
		{5, 5},
		{5.0, 5},
		{-2.0, -2},
		{0.0, 0},
		{2.5, 2.5},
		{1e300, 1e300},
		{true, true},
		{false, false},
		{"level1", "level1"},
		{"", ""},
	} {
		actual, err := tbaBreakdownValue(testCase.value)
		assert.Nil(t, err)
		assert.Equal(t, testCase.expected, actual)
	}

	for _, value := range []any{nil, math.NaN(), math.Inf(1), []any{1, 2}, map[string]any{"a": 1}} {
		_, err := tbaBreakdownValue(value)
		assert.NotNil(t, err, "%v", value)
	}
}

func TestPublishRankings(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	// A ranking holds one sort value per key of the game's ranking order; the last one is the coin flip.
	ranking2 := model.NewRanking(
		scoring.TeamRanking{
			TeamID: 1114, Rank: 2, RP: 13, Played: 7, Wins: 1, Losses: 3, Ties: 2, Disqualifications: 1,
			Sort: []float64{1.857143, 58.3, 12.5, 40, 0.1114}, Random: 0.1114,
		},
	)
	ranking1 := model.NewRanking(
		scoring.TeamRanking{
			TeamID: 254, Rank: 1, RP: 12, Played: 6, Wins: 3, Losses: 2, Ties: 1,
			Sort: []float64{2, 62.5, 16.4, 55.4, 0.254}, Random: 0.254,
		},
	)
	// A ranking stored when the game still sorted by fewer keys has no value for the newer columns.
	ranking3 := model.NewRanking(
		scoring.TeamRanking{TeamID: 1678, Rank: 3, RP: 1, Played: 2, Losses: 2, Sort: []float64{0.5, 20}},
	)
	assert.Nil(t, database.CreateRanking(&ranking2))
	assert.Nil(t, database.CreateRanking(&ranking1))
	assert.Nil(t, database.CreateRanking(&ranking3))

	// Mock the TBA server.
	tbaServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.String(), "event/my_event_code/rankings/update")
				body, _ := io.ReadAll(r.Body)
				assert.JSONEq(
					t,
					`{
						"breakdowns": ["Ranking score", "Avg match", "Avg auto fuel", "Avg tower"],
						"rankings": [
							{
								"team_key": "frc254",
								"rank": 1,
								"Ranking score": 2,
								"Avg match": 62.5,
								"Avg auto fuel": 16.4,
								"Avg tower": 55.4,
								"wins": 3,
								"losses": 2,
								"ties": 1,
								"dqs": 0,
								"played": 6
							},
							{
								"team_key": "frc1114",
								"rank": 2,
								"Ranking score": 1.857143,
								"Avg match": 58.3,
								"Avg auto fuel": 12.5,
								"Avg tower": 40,
								"wins": 1,
								"losses": 3,
								"ties": 2,
								"dqs": 1,
								"played": 7
							},
							{
								"team_key": "frc1678",
								"rank": 3,
								"Ranking score": 0.5,
								"Avg match": 20,
								"Avg auto fuel": 0,
								"Avg tower": 0,
								"wins": 0,
								"losses": 2,
								"ties": 0,
								"dqs": 0,
								"played": 2
							}
						]
					}`,
					string(body),
				)
			},
		),
	)
	defer tbaServer.Close()
	client := NewTbaClient("my_event_code", "my_secret_id", "my_secret")
	client.BaseUrl = tbaServer.URL

	assert.Nil(t, client.PublishRankings(database, g))

	// A column is a property of each team's row, so it cannot take the name of one of the fixed properties, nor of
	// another column.
	ballCount := string(model.TestGame("ball-count").Source)
	for _, name := range []string{"played", "Ranking score"} {
		clashing, diagnostics := gamedef.Parse([]byte(strings.Replace(ballCount, "Avg match points", name, 1)))
		if assert.NotNil(t, clashing, "%v", diagnostics) {
			err := client.PublishRankings(database, clashing)
			if assert.NotNil(t, err) {
				assert.Contains(t, err.Error(), fmt.Sprintf("ranking column %q cannot be published", name))
			}
		}
	}
}

func TestPublishAlliances(t *testing.T) {
	database := setupTestDb(t)

	model.BuildTestAlliances(database)

	// Mock the TBA server.
	tbaServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				var reader bytes.Buffer
				reader.ReadFrom(r.Body)
				if strings.Contains(r.URL.String(), "alliance_selections") {
					assert.Equal(
						t,
						"[[\"frc254\",\"frc469\",\"frc2848\",\"frc74\",\"frc3175\"],[\"frc1718\",\"frc2451\",\"frc1619\"]]",
						reader.String(),
					)
				} else {
					assert.Equal(t, "{\"playoff_type\":10}", reader.String())
				}
			},
		),
	)
	defer tbaServer.Close()
	client := NewTbaClient("my_event_code", "my_secret_id", "my_secret")
	client.BaseUrl = tbaServer.URL

	assert.Nil(t, client.PublishAlliances(database))
}

func TestPublishingErrors(t *testing.T) {
	database := setupTestDb(t)

	model.BuildTestAlliances(database)

	// Mock the TBA server.
	tbaServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "oh noes", 500)
			},
		),
	)
	defer tbaServer.Close()
	client := NewTbaClient("my_event_code", "my_secret_id", "my_secret")
	client.BaseUrl = tbaServer.URL

	assert.NotNil(t, client.PublishTeams(database))
	assert.NotNil(t, client.PublishMatches(database))
	assert.NotNil(t, client.PublishRankings(database, model.TestGame("rebuilt-style")))
	assert.NotNil(t, client.PublishAlliances(database))
}

func TestCheckTbaPostResponseClosesBody(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader("ok")}
	err := checkTbaPostResponse(&http.Response{StatusCode: 200, Body: body})
	assert.Nil(t, err)
	assert.True(t, body.closed)

	body = &closeTrackingBody{Reader: strings.NewReader("oh noes")}
	err = checkTbaPostResponse(&http.Response{StatusCode: 500, Body: body})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "Got status code 500 from TBA: oh noes")
	assert.True(t, body.closed)
}

func TestPublishAwards(t *testing.T) {
	database := setupTestDb(t)

	chairmansType := 0
	spiritType := 30
	database.CreateAward(&model.Award{Type: model.JudgedAward, AwardName: "Saftey Award", TeamId: 254})
	database.CreateAward(&model.Award{
		Type:        model.JudgedAward,
		AwardName:   "Spirt Award",
		PersonName:  "Bob Dorough",
		TbaTypeEnum: &spiritType,
	})
	database.CreateAward(&model.Award{
		Type:        model.JudgedAward,
		AwardName:   "Impact Award",
		TeamId:      1114,
		TbaTypeEnum: &chairmansType,
	})

	// Mock the TBA server.
	tbaServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.String(), "event/my_event_code")
				var reader bytes.Buffer
				reader.ReadFrom(r.Body)
				assert.NotContains(t, reader.String(), `"name_str":"Saftey Award","team_key":"frc254","awardee":null,"type_enum"`)
				assert.Contains(t, reader.String(), `{"name_str":"Saftey Award","team_key":"frc254","awardee":null}`)

				var actual []TbaPublishedAward
				err := json.Unmarshal(reader.Bytes(), &actual)
				assert.Nil(t, err)

				expected := []TbaPublishedAward{
					{TypeEnum: nil, Name: "Saftey Award", TeamKey: stringPtr("frc254"), Awardee: nil},
					{TypeEnum: &spiritType, Name: "Spirt Award", TeamKey: nil, Awardee: stringPtr("Bob Dorough")},
					{TypeEnum: &chairmansType, Name: "Impact Award", TeamKey: stringPtr("frc1114"), Awardee: nil},
				}
				assert.Equal(t, expected, actual)
			},
		),
	)
	defer tbaServer.Close()
	client := NewTbaClient("my_event_code", "my_secret_id", "my_secret")
	client.BaseUrl = tbaServer.URL

	assert.Nil(t, client.PublishAwards(database))
}

func setupTestDb(t *testing.T) *model.Database {
	return model.SetupTestDb(t)
}

// Publishes the matches in the database to a mock of the TBA server and returns the JSON it received.
func publishMatches(t *testing.T, database *model.Database) []byte {
	bodies := make(chan []byte, 1)
	tbaServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.String(), "event/my_event_code/matches/update")
				body, _ := io.ReadAll(r.Body)
				bodies <- body
			},
		),
	)
	defer tbaServer.Close()
	client := NewTbaClient("my_event_code", "my_secret_id", "my_secret")
	client.BaseUrl = tbaServer.URL

	if !assert.Nil(t, client.PublishMatches(database)) {
		t.FailNow()
	}
	return <-bodies
}

// Sets the number of balls an alliance scored in one phase of a match of the ball-count example game.
func editBalls(t *testing.T, g *gamedef.Game, matchResult *model.MatchResult, alliance, phase string, balls int) {
	_, err := matchResult.Edit(
		g,
		scoring.Change{
			Alliance: alliance, Element: "balls", Op: scoring.OpSet, Value: balls, Bucket: phase,
			Source: scoring.Source{Kind: scoring.SourceScorer, ID: "test"},
		},
	)
	assert.Nil(t, err)
}

type closeTrackingBody struct {
	*strings.Reader
	closed bool
}

func (body *closeTrackingBody) Close() error {
	body.closed = true
	return nil
}
