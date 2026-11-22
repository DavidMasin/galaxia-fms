package gamecli

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"testing"
)

const gamesDir = "../games"

func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestValidateAndTestExampleGames(t *testing.T) {
	code, stdout, stderr := run("validate", gamesDir)
	assert.Equal(t, 0, code, stderr)
	for _, name := range []string{"Ball Count", "Orbital Rush", "Rebuilt-style", "Reefscape"} {
		assert.Contains(t, stdout, name)
	}

	code, stdout, stderr = run("test", gamesDir)
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "pass  reefscape: A qualification match with every kind of scoring")
	assert.NotContains(t, stdout, "FAIL")
}

func TestScore(t *testing.T) {
	game := filepath.Join(gamesDir, "ball-count")
	write := func(content string) string {
		file := filepath.Join(t.TempDir(), "state.yaml")
		assert.NoError(t, os.WriteFile(file, []byte(content), 0644))
		return file
	}

	code, stdout, stderr := run("score", game, write("state:\n  red: { balls: { auto: 3, teleop: 10 } }\n"))
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "Red wins.")

	// A state written at the top level of the file would be scored as an empty match; it is refused instead.
	code, stdout, stderr = run("score", game, write("red: { balls: { auto: 3, teleop: 10 } }\n"))
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, `there is no "state:" key`)

	// An empty state is a state: it has the key.
	code, _, stderr = run("score", game, write("state: {}\n"))
	assert.Equal(t, 0, code, stderr)

	code, _, stderr = run("score", game, write("state:\n  red: { marbles: 3 }\n"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, `there is no element "marbles"`)
}
