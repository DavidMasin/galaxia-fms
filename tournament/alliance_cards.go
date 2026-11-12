// Copyright 2026 Team 254. All Rights Reserved.

package tournament

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
)

// Rebuilds carried alliance cards from the latest results of completed playoff matches. A playoff alliance wins
// or loses as one, so a card given to any of its robots is carried by the whole alliance.
func CalculateAllianceCards(database *model.Database) error {
	alliances, err := database.GetAllAlliances()
	if err != nil {
		return err
	}
	yellowCards := make(map[int]bool)
	matches, err := database.GetMatchesByType(model.Playoff, false)
	if err != nil {
		return err
	}
	for _, match := range matches {
		if !match.IsComplete() {
			continue
		}
		result, err := latestMatchResult(database, match.Id)
		if err != nil {
			return err
		}
		resultGame, err := database.GetSnapshotGame(result.GameHash)
		if err != nil {
			return fmt.Errorf("match %d: %w", match.Id, err)
		}
		sides, err := resultSides(&match, result, resultGame)
		if err != nil {
			return err
		}
		for _, side := range sides {
			if len(side.cardedPositions(resultGame)) > 0 {
				yellowCards[side.playoffAlliance] = true
			}
		}
	}
	for _, alliance := range alliances {
		alliance.YellowCard = yellowCards[alliance.Id]
		if err := database.UpdateAlliance(&alliance); err != nil {
			return err
		}
	}
	return nil
}
