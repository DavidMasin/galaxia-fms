package partner

import (
	"bufio"
	"embed"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// The Blue Alliance accepts only the score breakdown keys of the official game for the event's year, and rejects a
// whole publish when it meets one it does not know, even at an off-season event. The known keys for each year live
// in tbakeys/<year>.txt.
//
//go:embed tbakeys/*.txt
var tbaKeyFiles embed.FS

// TbaBreakdownKeys returns the official breakdown keys for a year, or nil when the year is not known here.
func TbaBreakdownKeys(year int) []string {
	data, err := tbaKeyFiles.ReadFile(fmt.Sprintf("tbakeys/%d.txt", year))
	if err != nil {
		return nil
	}
	var keys []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			keys = append(keys, line)
		}
	}
	return keys
}

// TbaValidation is what checking a game against The Blue Alliance found. Errors block publishing; warnings do not.
type TbaValidation struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func (v *TbaValidation) errorf(format string, args ...any) {
	v.Errors = append(v.Errors, fmt.Sprintf(format, args...))
}

func (v *TbaValidation) warnf(format string, args ...any) {
	v.Warnings = append(v.Warnings, fmt.Sprintf(format, args...))
}

// ValidateTbaBreakdown checks that a game's results can be published to The Blue Alliance.
func ValidateTbaBreakdown(g *gamedef.Game) *TbaValidation {
	validation := &TbaValidation{Errors: []string{}, Warnings: []string{}}
	if len(g.Alliances.List) != 2 {
		validation.errorf(
			"The Blue Alliance has only a red and a blue alliance; %s has %d alliances", g.Name, len(g.Alliances.List),
		)
		return validation
	}
	if g.Alliances.Size > 3 {
		validation.errorf("The Blue Alliance has three robots per alliance; %s has %d", g.Name, g.Alliances.Size)
	}
	if len(g.TBA.Breakdown) == 0 {
		validation.warnf(
			"%s has no tba section, so matches are published with scores and winners but no score breakdown", g.Name,
		)
		return validation
	}
	if g.TBA.Year == 0 {
		validation.errorf("set tba.year in %s to the year whose breakdown keys it uses", g.Name)
		return validation
	}

	official := TbaBreakdownKeys(g.TBA.Year)
	mapped := slices.Sorted(maps.Keys(g.TBA.Breakdown))
	if official == nil {
		validation.warnf(
			"the breakdown keys for %d are not known here, so they cannot be checked before publishing; The Blue "+
				"Alliance will reject the whole publish if any of them is wrong", g.TBA.Year,
		)
	} else {
		known := make(map[string]bool, len(official))
		for _, key := range official {
			known[key] = true
		}
		var unknown, unmapped []string
		for _, key := range mapped {
			if !known[key] {
				unknown = append(unknown, key)
			}
		}
		used := make(map[string]bool, len(mapped))
		for _, key := range mapped {
			used[key] = true
		}
		for _, key := range official {
			if !used[key] {
				unmapped = append(unmapped, key)
			}
		}
		if len(unknown) > 0 {
			validation.errorf(
				"The Blue Alliance does not know these %d keys and would reject the publish: %s",
				g.TBA.Year, strings.Join(unknown, ", "),
			)
		}
		if len(unmapped) > 0 {
			validation.warnf(
				"%d of The Blue Alliance's %d keys are not mapped and will be missing from its breakdown: %s",
				len(unmapped), g.TBA.Year, strings.Join(unmapped, ", "),
			)
		}
	}

	// Every mapping must evaluate, and nest without clashes, for an empty match.
	result, err := scoring.Compute(g, scoring.NewState(g), scoring.Options{})
	if err != nil {
		validation.errorf("scoring an empty match failed: %v", err)
		return validation
	}
	for _, alliance := range g.Alliances.List {
		if _, err := createTbaScoringBreakdown(g, result, alliance.ID); err != nil {
			validation.errorf("%v", err)
			break
		}
	}
	return validation
}

// checkPublishable returns an error naming every problem that would make The Blue Alliance reject a publish of
// results played with this game.
func checkPublishable(g *gamedef.Game) error {
	validation := ValidateTbaBreakdown(g)
	if len(validation.Errors) == 0 {
		return nil
	}
	return fmt.Errorf(
		"not publishing to The Blue Alliance: results of %s (%s) would be rejected: %s", g.Name,
		strconv.Quote(g.ID), strings.Join(validation.Errors, "; "),
	)
}
