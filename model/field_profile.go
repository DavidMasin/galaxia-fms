// Model and datastore methods for field profiles: named physical setups of the field, such as "tent" or "school gym".
// Device bindings are kept per game and per profile.

package model

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultFieldProfile is the profile a new event starts with.
const DefaultFieldProfile = "default"

type FieldProfile struct {
	Id   int `db:"id"`
	Name string
}

// CreateFieldProfile stores a new profile. Names are unique, ignoring surrounding spaces.
func (database *Database) CreateFieldProfile(profile *FieldProfile) error {
	name, err := database.checkFieldProfileName(profile.Name, 0)
	if err != nil {
		return err
	}
	profile.Name = name
	return database.fieldProfileTable.create(profile)
}

// GetAllFieldProfiles returns every profile, ordered by name. The default profile is created the first time the
// profiles are asked for, so that an event always has one.
func (database *Database) GetAllFieldProfiles() ([]FieldProfile, error) {
	profiles, err := database.fieldProfileTable.getAll()
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		profile := FieldProfile{Name: DefaultFieldProfile}
		if err = database.fieldProfileTable.create(&profile); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// GetFieldProfileByName returns the profile with the given name, or nil if there is none.
func (database *Database) GetFieldProfileByName(name string) (*FieldProfile, error) {
	profiles, err := database.GetAllFieldProfiles()
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		if profiles[i].Name == name {
			return &profiles[i], nil
		}
	}
	return nil, nil
}

// RenameFieldProfile renames a profile, and with it the binding sets kept for it and, if it is the active one, the
// event's choice of profile.
func (database *Database) RenameFieldProfile(id int, newName string) error {
	profile, err := database.fieldProfileTable.getById(id)
	if err != nil {
		return err
	}
	if profile == nil {
		return fmt.Errorf("there is no field profile with id %d", id)
	}
	if newName, err = database.checkFieldProfileName(newName, id); err != nil {
		return err
	}
	oldName := profile.Name
	profile.Name = newName
	if err = database.fieldProfileTable.update(profile); err != nil {
		return err
	}

	bindingSets, err := database.bindingSetTable.getAll()
	if err != nil {
		return err
	}
	for i := range bindingSets {
		if bindingSets[i].Profile == oldName {
			bindingSets[i].Profile = newName
			if err = database.bindingSetTable.update(&bindingSets[i]); err != nil {
				return err
			}
		}
	}
	settings, err := database.GetEventSettings()
	if err != nil {
		return err
	}
	if settings.FieldProfile == oldName {
		settings.FieldProfile = newName
		return database.UpdateEventSettings(settings)
	}
	return nil
}

// DeleteFieldProfile deletes a profile and the binding sets kept for it. The active profile cannot be deleted.
func (database *Database) DeleteFieldProfile(id int) error {
	profile, err := database.fieldProfileTable.getById(id)
	if err != nil {
		return err
	}
	if profile == nil {
		return fmt.Errorf("there is no field profile with id %d", id)
	}
	settings, err := database.GetEventSettings()
	if err != nil {
		return err
	}
	if settings.FieldProfile == profile.Name {
		return fmt.Errorf("field profile %q is the active one; choose another in the settings first", profile.Name)
	}
	bindingSets, err := database.bindingSetTable.getAll()
	if err != nil {
		return err
	}
	for _, bindingSet := range bindingSets {
		if bindingSet.Profile == profile.Name {
			if err = database.bindingSetTable.delete(bindingSet.Id); err != nil {
				return err
			}
		}
	}
	if err = database.DeleteLightingOfProfile(profile.Id); err != nil {
		return err
	}
	return database.fieldProfileTable.delete(id)
}

func (database *Database) TruncateFieldProfiles() error {
	return database.fieldProfileTable.truncate()
}

// checkFieldProfileName returns the name trimmed, or an error if it is empty or taken by a profile other than the
// one with the given id.
func (database *Database) checkFieldProfileName(name string, id int) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("a field profile needs a name")
	}
	profiles, err := database.fieldProfileTable.getAll()
	if err != nil {
		return "", err
	}
	for _, profile := range profiles {
		if profile.Name == name && profile.Id != id {
			return "", fmt.Errorf("there is already a field profile named %q", name)
		}
	}
	return name, nil
}
