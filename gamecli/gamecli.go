// Package gamecli implements the "galaxia-fms game ..." commands for working on game definitions from a terminal:
// validating them, running their worked examples, and scoring a raw state.
package gamecli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"go.yaml.in/yaml/v3"
)

const usage = `Work with game definitions.

Usage:
  galaxia-fms game validate <path>...        Check game definitions. A path is a game.yaml, a game's
                                             directory, or a directory of games.
  galaxia-fms game test <path>...            Run each game's worked examples (tests.yaml).
  galaxia-fms game score [-json] <game> <state.yaml>
                                             Compute the score of a raw state written in the compact
                                             form used by tests.yaml. The file may hold "state",
                                             "params" and "playoff".
  galaxia-fms game schema                    Print the JSON Schema for game definitions.
`

// CaseFile is the name of the worked examples inside a game's directory.
const CaseFile = "tests.yaml"

// Run executes a game command and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "validate":
		return validate(args[1:], stdout, stderr)
	case "test":
		return test(args[1:], stdout, stderr)
	case "score":
		return score(args[1:], stdout, stderr)
	case "schema":
		stdout.Write(gamedef.SchemaJSON())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "unknown game command %q\n\n%s", args[0], usage)
	return 2
}

// gameFiles expands the given paths into game definition files.
func gameFiles(paths []string) ([]string, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		if _, err := os.Stat(filepath.Join(path, gamedef.GameFile)); err == nil {
			files = append(files, filepath.Join(path, gamedef.GameFile))
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		found := false
		for _, entry := range entries {
			candidate := filepath.Join(path, entry.Name(), gamedef.GameFile)
			if _, err := os.Stat(candidate); entry.IsDir() && err == nil {
				files = append(files, candidate)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s contains no %s", path, gamedef.GameFile)
		}
	}
	sort.Strings(files)
	return files, nil
}

// load parses one game file and prints its diagnostics. It returns nil if the game has errors.
func load(file string, stdout io.Writer) *gamedef.Game {
	game, diagnostics := gamedef.LoadFile(file)
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(stdout, "%s: %s\n", file, diagnostic)
	}
	if game != nil {
		if dir := filepath.Base(filepath.Dir(file)); filepath.Base(file) == gamedef.GameFile && dir != game.ID {
			fmt.Fprintf(stdout, "%s: error: id %q must match the directory name %q\n", file, game.ID, dir)
			return nil
		}
	}
	return game
}

func validate(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	files, err := gameFiles(paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	failed := 0
	for _, file := range files {
		game := load(file, stdout)
		if game == nil {
			failed++
			continue
		}
		fmt.Fprintf(
			stdout, "%s: ok (%s: %d alliances of %d, %d phases, %d elements, %s)\n", file, game.Name,
			len(game.Alliances.List), game.Alliances.Size, len(game.Match.Phases), len(game.Elements),
			formatDuration(game.Duration()),
		)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func formatDuration(seconds float64) string {
	whole := int(seconds)
	return fmt.Sprintf("%d:%02d", whole/60, whole%60)
}

func test(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	files, err := gameFiles(paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	failed := 0
	for _, file := range files {
		game := load(file, stdout)
		if game == nil {
			failed++
			continue
		}
		caseFile := filepath.Join(filepath.Dir(file), CaseFile)
		src, err := os.ReadFile(caseFile)
		if err != nil {
			fmt.Fprintf(stdout, "%s: no %s, nothing to run\n", filepath.Dir(file), CaseFile)
			continue
		}
		outcomes, err := scoring.RunCases(game, src)
		if err != nil {
			fmt.Fprintf(stdout, "%s: %s\n", caseFile, err)
			failed++
			continue
		}
		for _, outcome := range outcomes {
			if len(outcome.Failures) == 0 {
				fmt.Fprintf(stdout, "  pass  %s: %s\n", game.ID, outcome.Name)
				continue
			}
			failed++
			fmt.Fprintf(stdout, "  FAIL  %s: %s\n", game.ID, outcome.Name)
			for _, failure := range outcome.Failures {
				fmt.Fprintf(stdout, "          %s\n", failure)
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "%d failed\n", failed)
		return 1
	}
	return 0
}

func score(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	if len(args) > 0 && args[0] == "-json" {
		asJSON, args = true, args[1:]
	}
	if len(args) != 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	files, err := gameFiles(args[:1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	game := load(files[0], stderr)
	if game == nil {
		return 1
	}
	src, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var input struct {
		State   map[string]map[string]any `yaml:"state"`
		Params  map[string]float64        `yaml:"params"`
		Playoff bool                      `yaml:"playoff"`
	}
	if err := yaml.Unmarshal(src, &input); err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", args[1], err)
		return 1
	}
	state, err := scoring.BuildState(game, input.State)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result, err := scoring.Compute(game, state, scoring.Options{Params: input.Params, Playoff: input.Playoff})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	printResult(stdout, game, result)
	return 0
}

func printResult(w io.Writer, game *gamedef.Game, result *scoring.Result) {
	ids := game.AllianceIDs()
	header := fmt.Sprintf("%-28s", "")
	for _, alliance := range game.Alliances.List {
		header += fmt.Sprintf("%12s", alliance.Name)
	}
	fmt.Fprintln(w, header)
	line := func(label string, value func(*scoring.AllianceResult) string) {
		row := fmt.Sprintf("%-28s", label)
		for _, id := range ids {
			row += fmt.Sprintf("%12s", value(result.Alliances[id]))
		}
		fmt.Fprintln(w, strings.TrimRight(row, " "))
	}
	for i, breakdownRow := range game.Breakdown {
		line(breakdownRow.Name, func(a *scoring.AllianceResult) string { return number(a.Breakdown[i].Value) })
	}
	line("Score", func(a *scoring.AllianceResult) string { return number(a.Score) })
	line("Result", func(a *scoring.AllianceResult) string { return a.Result })
	for _, rankingPoint := range game.Ranking.Points {
		line("RP: "+rankingPoint.Name, func(a *scoring.AllianceResult) string { return number(a.RP[rankingPoint.ID]) })
	}
	if len(game.Ranking.Points) > 0 {
		line("Ranking points", func(a *scoring.AllianceResult) string { return number(a.RPTotal) })
	}
	switch {
	case result.Winner == "":
		fmt.Fprintln(w, "\nTie.")
	case result.Tiebreak != "":
		fmt.Fprintf(w, "\n%s wins on the tiebreaker: %s.\n", game.Alliance(result.Winner).Name, result.Tiebreak)
	default:
		fmt.Fprintf(w, "\n%s wins.\n", game.Alliance(result.Winner).Name)
	}
}

func number(value float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}
