// Copyright 2017 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Functions for calculating the qualification rankings.

package tournament

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"slices"
)

// Determines the rankings from the stored match results, and saves them to the database. Every result is scored
// with the game it was played with; g is the event's active game, whose ranking order sorts the teams.
func CalculateRankings(database *model.Database, g *gamedef.Game, preservePreviousRank bool) ([]model.Ranking, error) {
	matches, err := database.GetMatchesByType(model.Qualification, false)
	if err != nil {
		return nil, err
	}

	// Adding a match to a team's record draws its random tiebreaker value, so the records are built in an order
	// that is the same on every run: matches in schedule order, red before blue, by robot position. The map only
	// finds a team's record; it is never iterated.
	var teamRankings []scoring.TeamRanking
	teamIndexes := make(map[int]int)
	for _, match := range matches {
		if !match.IsComplete() {
			continue
		}
		matchResult, err := latestMatchResult(database, match.Id)
		if err != nil {
			return nil, err
		}
		result, resultGame, err := matchResult.Compute(database, match.UseTiebreakCriteria)
		if err != nil {
			return nil, fmt.Errorf("match %d: %w", match.Id, err)
		}
		sides, err := resultSides(&match, matchResult, resultGame)
		if err != nil {
			return nil, err
		}
		for _, side := range sides {
			// A card that disqualifies costs only the team that holds it; its partners keep what they earned.
			disqualifiedPositions := model.DisqualifiedPositions(resultGame, side.state)
			for i, teamId := range side.teamIds {
				if teamId == 0 || side.isSurrogate[i] {
					continue
				}
				index, ok := teamIndexes[teamId]
				if !ok {
					index = len(teamRankings)
					teamIndexes[teamId] = index
					teamRankings = append(teamRankings, scoring.TeamRanking{TeamID: teamId})
				}
				disqualified := slices.Contains(disqualifiedPositions, i+1)
				teamRankings[index].AddMatch(result.Alliances[side.allianceId], disqualified)
			}
		}
	}
	if err = scoring.SortRankings(g, teamRankings); err != nil {
		return nil, err
	}

	// Retrieve old rankings so that we can display changes in rank as a result of this calculation.
	oldRankings, err := database.GetAllRankings()
	if err != nil {
		return nil, err
	}
	oldRankingsMap := make(map[int]model.Ranking, len(oldRankings))
	for _, ranking := range oldRankings {
		oldRankingsMap[ranking.TeamId] = ranking
	}

	var sortedRankings []model.Ranking
	for _, teamRanking := range teamRankings {
		ranking := model.NewRanking(teamRanking)
		if oldRanking, ok := oldRankingsMap[ranking.TeamId]; ok {
			if preservePreviousRank {
				ranking.PreviousRank = oldRanking.PreviousRank
			} else {
				ranking.PreviousRank = oldRanking.Rank
			}
		}
		sortedRankings = append(sortedRankings, ranking)
	}
	err = database.ReplaceAllRankings(sortedRankings)
	if err != nil {
		return nil, err
	}

	return sortedRankings, nil
}

// Rebuilds carried team cards from qualification results.
func CalculateTeamCards(database *model.Database) error {
	teams, err := database.GetAllTeams()
	if err != nil {
		return err
	}
	teamsMap := make(map[int]model.Team)
	for _, team := range teams {
		team.YellowCard = false
		teamsMap[team.Id] = team
	}

	matches, err := database.GetMatchesByType(model.Qualification, false)
	if err != nil {
		return err
	}
	for _, match := range matches {
		if !match.IsComplete() {
			continue
		}
		matchResult, err := latestMatchResult(database, match.Id)
		if err != nil {
			return err
		}
		resultGame, err := database.GetSnapshotGame(matchResult.GameHash)
		if err != nil {
			return fmt.Errorf("match %d: %w", match.Id, err)
		}
		sides, err := resultSides(&match, matchResult, resultGame)
		if err != nil {
			return err
		}

		// Mark the team as carrying a card if it was given any card in a previous match. Which cards exist is up
		// to the game, so none of them is singled out here.
		for _, side := range sides {
			for _, position := range side.cardedPositions(resultGame) {
				if team, ok := teamsMap[side.teamAt(position)]; ok {
					team.YellowCard = true
					teamsMap[team.Id] = team
				}
			}
		}
	}

	// Save the teams to the database.
	for _, team := range teamsMap {
		err = database.UpdateTeam(&team)
		if err != nil {
			return err
		}
	}

	return nil
}
