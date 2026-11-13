package partner

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

func TestTbaBreakdownKeys(t *testing.T) {
	keys := TbaBreakdownKeys(2026)
	assert.Len(t, keys, 38)
	assert.Contains(t, keys, "hubScore.shift3Points")
	assert.Contains(t, keys, "rp")
	assert.Nil(t, TbaBreakdownKeys(1999))
}

func TestValidateTbaBreakdown(t *testing.T) {
	model.BaseDir = ".."
	// The 2026-style game maps a subset of the official keys, all of them known.
	validation := ValidateTbaBreakdown(model.TestGame("rebuilt-style"))
	assert.Empty(t, validation.Errors)
	if assert.Len(t, validation.Warnings, 1) {
		assert.Contains(t, validation.Warnings[0], "are not mapped")
		assert.Contains(t, validation.Warnings[0], "hubScore.autoCount")
	}

	// A game with no mapping publishes without a breakdown.
	validation = ValidateTbaBreakdown(model.TestGame("ball-count"))
	assert.Empty(t, validation.Errors)
	assert.Contains(t, validation.Warnings[0], "no tba section")

	// Three alliances cannot be published at all.
	validation = ValidateTbaBreakdown(model.TestGame("orbital-rush"))
	if assert.Len(t, validation.Errors, 1) {
		assert.Contains(t, validation.Errors[0], "has 3 alliances")
	}

	// An unknown key, a missing year and an unknown year.
	game := model.TestGame("rebuilt-style")
	game.TBA.Breakdown["madeUpKey"] = game.TBA.Breakdown["totalPoints"]
	validation = ValidateTbaBreakdown(game)
	if assert.Len(t, validation.Errors, 1) {
		assert.Contains(t, validation.Errors[0], "madeUpKey")
	}
	assert.Error(t, checkPublishable(game))
	assert.True(t, strings.Contains(checkPublishable(game).Error(), "not publishing to The Blue Alliance"))

	game = model.TestGame("rebuilt-style")
	game.TBA.Year = 0
	assert.Contains(t, ValidateTbaBreakdown(game).Errors[0], "set tba.year")
	game.TBA.Year = 1999
	validation = ValidateTbaBreakdown(game)
	assert.Empty(t, validation.Errors)
	assert.Contains(t, validation.Warnings[0], "not known here")
	assert.NoError(t, checkPublishable(model.TestGame("rebuilt-style")))
}
