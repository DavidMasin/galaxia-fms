// Model and datastore methods for device binding sets: what each device input does and what each output shows, for
// one game and one field profile. The set is stored as the YAML the Bindings page exports, so that what is stored
// and what an operator reads are the same text; the device package parses and checks it.

package model

import (
	"sort"
	"time"
)

type BindingSet struct {
	Id        int `db:"id"`
	GameId    string
	Profile   string
	Yaml      string
	UpdatedAt time.Time
}

// GetBindingSet returns the binding set of the given game and field profile, or nil if none has been saved.
func (database *Database) GetBindingSet(gameId, profile string) (*BindingSet, error) {
	bindingSets, err := database.bindingSetTable.getAll()
	if err != nil {
		return nil, err
	}
	for i := range bindingSets {
		if bindingSets[i].GameId == gameId && bindingSets[i].Profile == profile {
			return &bindingSets[i], nil
		}
	}
	return nil, nil
}

// SaveBindingSet stores the binding set of the given game and field profile, replacing the one there was.
func (database *Database) SaveBindingSet(gameId, profile, yaml string) error {
	existing, err := database.GetBindingSet(gameId, profile)
	if err != nil {
		return err
	}
	if existing != nil {
		existing.Yaml = yaml
		existing.UpdatedAt = time.Now().UTC()
		return database.bindingSetTable.update(existing)
	}
	return database.bindingSetTable.create(
		&BindingSet{GameId: gameId, Profile: profile, Yaml: yaml, UpdatedAt: time.Now().UTC()},
	)
}

// DeleteBindingSet removes the binding set of the given game and field profile, if there is one.
func (database *Database) DeleteBindingSet(gameId, profile string) error {
	existing, err := database.GetBindingSet(gameId, profile)
	if err != nil || existing == nil {
		return err
	}
	return database.bindingSetTable.delete(existing.Id)
}

// GetAllBindingSets returns every binding set, ordered by game and then profile.
func (database *Database) GetAllBindingSets() ([]BindingSet, error) {
	bindingSets, err := database.bindingSetTable.getAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(
		bindingSets,
		func(i, j int) bool {
			if bindingSets[i].GameId != bindingSets[j].GameId {
				return bindingSets[i].GameId < bindingSets[j].GameId
			}
			return bindingSets[i].Profile < bindingSets[j].Profile
		},
	)
	return bindingSets, nil
}

func (database *Database) TruncateBindingSets() error {
	return database.bindingSetTable.truncate()
}
