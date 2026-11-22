package scoring

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"path/filepath"
	"testing"
)

// The cost of working out a score once, for the smallest example game and for the one with the most elements. The
// arena does this after every change to the score, so it bounds how long a change holds the score lock.
func BenchmarkCompute(b *testing.B) {
	for _, id := range []string{"ball-count", "rebuilt-style", "reefscape"} {
		b.Run(id, func(b *testing.B) {
			game, diagnostics := gamedef.LoadFile(filepath.Join(gamesDir, id, gamedef.GameFile))
			if game == nil {
				b.Fatal(diagnostics.Error())
			}
			state := NewState(game)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Compute(game, state, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
