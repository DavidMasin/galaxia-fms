// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Model and datastore CRUD methods for team ranking data at an event.

package model

import (
	"github.com/DavidMasin/galaxia-fms/scoring"
	"sort"
)

// Ranking is one team's stored place in the qualification rankings. The record itself (ranking points, matches
// played, the game's stats and sort values) is the engine's; this type only gives it a key in the datastore.
type Ranking struct {
	TeamId int `db:"id,manual"`
	scoring.TeamRanking
}

// NewRanking wraps an engine ranking for storage.
func NewRanking(teamRanking scoring.TeamRanking) Ranking {
	return Ranking{TeamId: teamRanking.TeamID, TeamRanking: teamRanking}
}

func (database *Database) CreateRanking(ranking *Ranking) error {
	return database.rankingTable.create(ranking)
}

func (database *Database) GetRankingForTeam(teamId int) (*Ranking, error) {
	return database.rankingTable.getById(teamId)
}

func (database *Database) UpdateRanking(ranking *Ranking) error {
	return database.rankingTable.update(ranking)
}

func (database *Database) DeleteRanking(teamId int) error {
	return database.rankingTable.delete(teamId)
}

func (database *Database) TruncateRankings() error {
	return database.rankingTable.truncate()
}

func (database *Database) GetAllRankings() ([]Ranking, error) {
	rankings, err := database.rankingTable.getAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(
		rankings,
		func(i, j int) bool {
			return rankings[i].Rank < rankings[j].Rank
		},
	)
	return rankings, nil
}

// Deletes the existing rankings and inserts the given ones as a replacement.
func (database *Database) ReplaceAllRankings(rankings []Ranking) error {
	if err := database.rankingTable.truncate(); err != nil {
		return err
	}

	for _, ranking := range rankings {
		if err := database.CreateRanking(&ranking); err != nil {
			return err
		}
	}
	return nil
}
