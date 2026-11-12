// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package model

import (
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"testing"
)

func testRanking() *Ranking {
	ranking := NewRanking(
		scoring.TeamRanking{
			TeamID: 254, Rank: 1, RP: 20, Played: 10, Wins: 3, Losses: 2, Ties: 1,
			Stats: map[string]float64{"match_points": 625, "tower_points": 554}, Sort: []float64{2, 62.5, 0.254},
			Random: 0.254,
		},
	)
	return &ranking
}

func TestGetNonexistentRanking(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	ranking, err := db.GetRankingForTeam(1114)
	assert.Nil(t, err)
	assert.Nil(t, ranking)
}

func TestRankingCrud(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	ranking := testRanking()
	assert.Equal(t, 254, ranking.TeamId)
	assert.Nil(t, db.CreateRanking(ranking))
	ranking2, err := db.GetRankingForTeam(254)
	assert.Nil(t, err)
	assert.Equal(t, ranking, ranking2)

	ranking.Random = 0.1114
	ranking.Stats["match_points"] = 700
	db.UpdateRanking(ranking)
	ranking2, err = db.GetRankingForTeam(254)
	assert.Nil(t, err)
	assert.Equal(t, ranking, ranking2)
}

func TestTruncateRankings(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	ranking := testRanking()
	db.CreateRanking(ranking)
	db.TruncateRankings()
	ranking2, err := db.GetRankingForTeam(254)
	assert.Nil(t, err)
	assert.Nil(t, ranking2)
}

func TestGetAllRankings(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	rankings, err := db.GetAllRankings()
	assert.Nil(t, err)
	assert.Empty(t, rankings)

	// Rankings come back in rank order, whatever order the teams are stored in.
	numRankings := 20
	for i := 1; i <= numRankings; i++ {
		ranking := NewRanking(scoring.TeamRanking{TeamID: i, Rank: numRankings + 1 - i})
		assert.Nil(t, db.CreateRanking(&ranking))
	}
	rankings, err = db.GetAllRankings()
	assert.Nil(t, err)
	assert.Equal(t, numRankings, len(rankings))
	for i := 0; i < numRankings; i++ {
		assert.Equal(t, i+1, rankings[i].Rank)
		assert.Equal(t, numRankings-i, rankings[i].TeamId)
		assert.Equal(t, numRankings-i, rankings[i].TeamID)
	}
}

func TestReplaceAllRankings(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	assert.Nil(t, db.CreateRanking(testRanking()))
	replacement := []Ranking{
		NewRanking(scoring.TeamRanking{TeamID: 1114, Rank: 1}), NewRanking(scoring.TeamRanking{TeamID: 2056, Rank: 2}),
	}
	assert.Nil(t, db.ReplaceAllRankings(replacement))
	rankings, err := db.GetAllRankings()
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(rankings)) {
		assert.Equal(t, 1114, rankings[0].TeamId)
		assert.Equal(t, 2056, rankings[1].TeamId)
	}
}
