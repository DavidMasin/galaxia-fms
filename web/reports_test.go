// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRankingsCsvReport(t *testing.T) {
	web := setupTestWeb(t)

	web.arena.Database.CreateRanking(testRanking2())
	web.arena.Database.CreateRanking(testRanking1())

	// The columns between the ranking points and the record are the ranking order of the active game, without its
	// random tiebreaker, each to the number of decimals the game asks for.
	recorder := web.getHttpResponse("/reports/csv/rankings")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "text/plain", recorder.Header()["Content-Type"][0])
	expectedBody := "Rank,TeamId,RankingPoints,Ranking score,Avg match,Avg auto fuel,Avg tower,Wins,Losses,Ties," +
		"Disqualifications,Played\n1,254,20,2.00,62.50,9.00,55.40,3,2,1,0,10\n" +
		"2,1114,18,1.80,70.00,62.50,9.00,1,3,2,0,10\n\n"
	assert.Equal(t, expectedBody, recorder.Body.String())

	// Another game, other columns. Its second column is printed to one decimal.
	selectTestGame(t, web, "ball-count")
	recorder = web.getHttpResponse("/reports/csv/rankings")
	assert.Equal(t, 200, recorder.Code)
	expectedBody = "Rank,TeamId,RankingPoints,Ranking score,Avg match points,Wins,Losses,Ties,Disqualifications," +
		"Played\n1,254,20,2.00,62.5,3,2,1,0,10\n2,1114,18,1.80,70.0,1,3,2,0,10\n\n"
	assert.Equal(t, expectedBody, recorder.Body.String())
}

func TestCsvLine(t *testing.T) {
	line, err := csvLine([]string{"1", "Avg, per match", "say \"hi\""})
	assert.Nil(t, err)
	assert.Equal(t, "1,\"Avg, per match\",\"say \"\"hi\"\"\"", line)
}

func TestRankingsPdfReport(t *testing.T) {
	web := setupTestWeb(t)

	web.arena.Database.CreateRanking(testRanking2())
	web.arena.Database.CreateRanking(testRanking1())

	// Can't really parse the PDF content and check it, so just check that what's sent back is a PDF, for a game
	// with several ranking columns and for one with few.
	for _, gameId := range []string{"rebuilt-style", "ball-count"} {
		selectTestGame(t, web, gameId)
		recorder := web.getHttpResponse("/reports/pdf/rankings")
		assert.Equal(t, 200, recorder.Code)
		assert.Equal(t, "application/pdf", recorder.Header()["Content-Type"][0])
	}
}

func TestScheduleCsvReport(t *testing.T) {
	web := setupTestWeb(t)

	match1Time := time.Unix(0, 0)
	match1 := model.Match{
		Type:             model.Qualification,
		ShortName:        "Q1",
		Time:             match1Time,
		Red1:             1,
		Red2:             2,
		Red3:             3,
		Blue1:            4,
		Blue2:            5,
		Blue3:            6,
		Blue1IsSurrogate: true,
		Blue2IsSurrogate: true,
		Blue3IsSurrogate: true,
	}
	match2Time := time.Unix(600, 0)
	match2 := model.Match{
		Type:            model.Qualification,
		ShortName:       "Q2",
		Time:            match2Time,
		Red1:            7,
		Red2:            8,
		Red3:            9,
		Blue1:           10,
		Blue2:           11,
		Blue3:           12,
		Red1IsSurrogate: true,
		Red2IsSurrogate: true,
		Red3IsSurrogate: true,
	}
	match3 := model.Match{
		Type:      model.Practice,
		ShortName: "P1",
		Time:      time.Now(),
		Red1:      6,
		Red2:      5,
		Red3:      4,
		Blue1:     3,
		Blue2:     2,
		Blue3:     1,
	}
	web.arena.Database.CreateMatch(&match1)
	web.arena.Database.CreateMatch(&match2)
	web.arena.Database.CreateMatch(&match3)

	recorder := web.getHttpResponse("/reports/csv/schedule/qualification")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "text/plain", recorder.Header()["Content-Type"][0])
	expectedBody := "Match,Type,Time,Red1,Red1IsSurrogate,Red2,Red2IsSurrogate,Red3,Red3IsSurrogate,Blue1," +
		"Blue1IsSurrogate,Blue2,Blue2IsSurrogate,Blue3,Blue3IsSurrogate\nQ1,Qualification," + match1Time.String() +
		",1,false,2,false,3,false,4,true,5,true,6,true\nQ2,Qualification," + match2Time.String() +
		",7,true,8,true,9,true,10,false,11,false,12,false\n\n"
	assert.Equal(t, expectedBody, recorder.Body.String())
}

func TestSchedulePdfReport(t *testing.T) {
	web := setupTestWeb(t)

	match := model.Match{
		Type:             model.Practice,
		ShortName:        "P1",
		Time:             time.Unix(0, 0),
		Red1:             1,
		Red2:             2,
		Red3:             3,
		Blue1:            4,
		Blue2:            5,
		Blue3:            6,
		Blue1IsSurrogate: true,
		Blue2IsSurrogate: true,
		Blue3IsSurrogate: true,
	}
	web.arena.Database.CreateMatch(&match)
	team := model.Team{
		Id: 254, Name: "NASA", Nickname: "The Cheesy Poofs", City: "San Jose", StateProv: "CA",
		Country: "USA", RookieYear: 1999, RobotName: "Barrage",
	}
	web.arena.Database.CreateTeam(&team)

	// Can't really parse the PDF content and check it, so just check that what's sent back is a PDF.
	recorder := web.getHttpResponse("/reports/pdf/schedule/practice")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/pdf", recorder.Header()["Content-Type"][0])
}

func TestTeamsCsvReport(t *testing.T) {
	web := setupTestWeb(t)

	team1 := model.Team{
		Id:         254,
		Name:       "NASA",
		Nickname:   "The Cheesy Poofs",
		SchoolName: "Bellarmine College Preparatory",
		City:       "San Jose",
		StateProv:  "CA",
		Country:    "USA",
		RookieYear: 1999,
		RobotName:  "Barrage",
	}
	team2 := model.Team{
		Id:         1114,
		Name:       "GM",
		Nickname:   "Simbotics",
		SchoolName: "Governor Simcoe Secondary School",
		City:       "St. Catharines",
		StateProv:  "ON",
		Country:    "Canada",
		RookieYear: 2003,
		RobotName:  "Simbot Evolution",
	}
	web.arena.Database.CreateTeam(&team1)
	web.arena.Database.CreateTeam(&team2)

	recorder := web.getHttpResponse("/reports/csv/teams")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "text/plain", recorder.Header()["Content-Type"][0])
	expectedBody := "Number,Name,Nickname,SchoolName,City,StateProv,Country,RookieYear,RobotName,HasConnected\n254,\"NASA\"," +
		"\"The Cheesy Poofs\",\"Bellarmine College Preparatory\",\"San Jose\",\"CA\",\"USA\",1999,\"Barrage\",false\n1114,\"GM\",\"Simbotics\"," +
		"\"Governor Simcoe Secondary School\",\"St. Catharines\",\"ON\",\"Canada\",2003,\"Simbot Evolution\",false\n\n"
	assert.Equal(t, expectedBody, recorder.Body.String())
}

func TestTeamsPdfReport(t *testing.T) {
	web := setupTestWeb(t)

	team := model.Team{
		Id:         254,
		Name:       "NASA",
		Nickname:   "The Cheesy Poofs",
		City:       "San Jose",
		StateProv:  "CA",
		Country:    "USA",
		RookieYear: 1999,
		RobotName:  "Barrage",
	}
	web.arena.Database.CreateTeam(&team)

	// Can't really parse the PDF content and check it, so just check that what's sent back is a PDF.
	recorder := web.getHttpResponse("/reports/pdf/teams")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/pdf", recorder.Header()["Content-Type"][0])
}

func TestWpaKeysCsvReport(t *testing.T) {
	web := setupTestWeb(t)

	team1 := model.Team{Id: 254, WpaKey: "12345678"}
	team2 := model.Team{Id: 1114, WpaKey: "9876543210"}
	web.arena.Database.CreateTeam(&team1)
	web.arena.Database.CreateTeam(&team2)

	recorder := web.getHttpResponse("/reports/csv/wpa_keys")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "text/csv", recorder.Header()["Content-Type"][0])
	assert.Equal(t, "attachment; filename=keys.csv", recorder.Header()["Content-Disposition"][0])
	assert.Equal(t, "254,12345678\r\n1114,9876543210\r\n", recorder.Body.String())
}

func TestAlliancesPdfReport(t *testing.T) {
	web := setupTestWeb(t)
	tournament.CreateTestAlliances(web.arena.Database, 8)
	web.arena.CreatePlayoffTournament()

	// Can't really parse the PDF content and check it, so just check that what's sent back is a PDF.
	recorder := web.getHttpResponse("/reports/pdf/alliances")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/pdf", recorder.Header()["Content-Type"][0])
}

func TestBracketPdfReport(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/reports/pdf/bracket")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "text/html; charset=utf-8", recorder.Header()["Content-Type"][0])
	assert.Contains(t, recorder.Body.String(), "Finals")
}

func TestJudgingSchedulePdfReport(t *testing.T) {
	web := setupTestWeb(t)

	// Can't really parse the PDF content and check it, so just check that what's sent back is a PDF.
	recorder := web.getHttpResponse("/reports/pdf/judging_schedule")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/pdf", recorder.Header()["Content-Type"][0])
}
