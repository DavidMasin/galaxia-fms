// Copyright 2017 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Helper methods for use in tests in this package and others.

package field

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

// SetupTestArena creates an arena on a scratch database. It plays the example games in the repository's games
// directory, and starts with the default one selected.
func SetupTestArena(t *testing.T) *Arena {
	model.BaseDir = ".."
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	arena, err := NewArena(dbPath)
	assert.Nil(t, err)
	t.Cleanup(
		func() {
			arena.Database.Close()
		},
	)
	return arena
}

func setupTestArena(t *testing.T) *Arena {
	return SetupTestArena(t)
}
