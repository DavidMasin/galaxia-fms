package predict

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreMatchMatchesStatbotics(t *testing.T) {
	// Statbotics: p(red) = 1 / (1 + 10^(-5/8 × (red - blue) / sd)).
	prediction := PreMatch{}.Predict(Input{
		Alliances: []Alliance{{ID: "red", Rating: 120}, {ID: "blue", Rating: 100}}, ScoreSD: 40,
	})
	want := 1 / (1 + math.Pow(10, -5.0/8.0*0.5))
	assert.InDelta(t, want, prediction.WinProbability["red"], 1e-12)
	assert.InDelta(t, 1-want, prediction.WinProbability["blue"], 1e-12)
	assert.Equal(t, map[string]float64{"red": 120, "blue": 100}, prediction.Expected)
	assert.Equal(t, "ratings", prediction.Source)

	// Equal ratings are a coin flip.
	prediction = PreMatch{}.Predict(Input{Alliances: []Alliance{{ID: "a"}, {ID: "b"}}, ScoreSD: 40})
	assert.InDelta(t, 0.5, prediction.WinProbability["a"], 1e-12)
}

func TestPreMatchWithThreeAlliances(t *testing.T) {
	prediction := PreMatch{}.Predict(Input{
		Alliances: []Alliance{{ID: "red", Rating: 60}, {ID: "blue", Rating: 50}, {ID: "green", Rating: 50}},
		ScoreSD:   15,
	})
	sum := 0.0
	for _, p := range prediction.WinProbability {
		sum += p
	}
	assert.InDelta(t, 1, sum, 1e-9)
	assert.Greater(t, prediction.WinProbability["red"], prediction.WinProbability["blue"])
	assert.InDelta(t, prediction.WinProbability["blue"], prediction.WinProbability["green"], 1e-9)

	// Three equal alliances: a third each.
	prediction = PreMatch{}.Predict(Input{Alliances: []Alliance{{ID: "a"}, {ID: "b"}, {ID: "c"}}, ScoreSD: 10})
	for _, p := range prediction.WinProbability {
		assert.InDelta(t, 1.0/3, p, 1e-6)
	}
}

func TestHighestOfNormalsMatchesTheClosedForm(t *testing.T) {
	// For two normals, P(A > B) = Φ((μA − μB) / (σ√2)).
	for _, difference := range []float64{-30, -5, 0, 7, 25} {
		got := highestOfNormals([]float64{100 + difference, 100}, 12)
		want := normalCumulative(difference/(12*math.Sqrt2), 0, 1)
		assert.InDelta(t, want, got[0], 1e-6, "difference %v", difference)
	}
}

func TestLive(t *testing.T) {
	alliances := []Alliance{{ID: "red", Rating: 100, Score: 30}, {ID: "blue", Rating: 100, Score: 20}}

	// Before the match only the ratings matter; at the end only the score.
	start := Live{}.Predict(Input{Alliances: alliances, ScoreSD: 30, Remaining: 1})
	assert.InDelta(t, 130, start.Expected["red"], 1e-9)
	middle := Live{}.Predict(Input{Alliances: alliances, ScoreSD: 30, Remaining: 0.5})
	assert.InDelta(t, 80, middle.Expected["red"], 1e-9)
	late := Live{}.Predict(Input{Alliances: alliances, ScoreSD: 30, Remaining: 0.05})
	end := Live{}.Predict(Input{Alliances: alliances, ScoreSD: 30, Remaining: 0})
	assert.Equal(t, 1.0, end.WinProbability["red"])
	assert.Equal(t, 0.0, end.WinProbability["blue"])

	// A ten-point lead counts for more and more as time runs out.
	assert.Less(t, start.WinProbability["red"], middle.WinProbability["red"])
	assert.Less(t, middle.WinProbability["red"], late.WinProbability["red"])

	// A tie at the end is shared.
	tied := Live{}.Predict(Input{Alliances: []Alliance{{ID: "a", Score: 5}, {ID: "b", Score: 5}}, ScoreSD: 30})
	assert.Equal(t, 0.5, tied.WinProbability["a"])

	// A one-alliance game always "wins".
	solo := Live{}.Predict(Input{Alliances: []Alliance{{ID: "home", Score: 3}}, ScoreSD: 10, Remaining: 0.5})
	assert.Equal(t, 1.0, solo.WinProbability["home"])
	assert.Empty(t, Live{}.Predict(Input{}).WinProbability)
}

func TestEventScoreSD(t *testing.T) {
	assert.Equal(t, 0.0, EventScoreSD([]float64{1, 2, 3}))
	assert.InDelta(t, 12.9099, EventScoreSD([]float64{10, 20, 30, 40}), 1e-4)
}

func TestRatingsFetchAndCache(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.URL.Path == "/v3/year/2026":
			fmt.Fprint(w, `{"score_sd": 41.5, "score_mean": 98}`)
		case r.URL.Path == "/v3/team_year/254/2026":
			fmt.Fprint(w, `{"epa": {"total_points": 40.5, "breakdown": {"total_points": 40.5}}}`)
		case r.URL.Path == "/v3/team_year/1114/2026":
			fmt.Fprint(w, `{"epa": {"total_points": {"mean": 33.25, "sd": 6}}}`) // The shape before June 2026.
		case r.URL.Path == "/v3/team_year/971/2026":
			fmt.Fprint(w, `{"epa": {"breakdown": {"total_points": 21}}}`)
		default:
			http.Error(w, "{}", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "ratings.json")
	ratings := NewRatings(server.URL+"/v3", 2026, cachePath)
	err := ratings.Refresh([]int{254, 1114, 971, 9999, 0}, time.Hour)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "9999")
		assert.Contains(t, err.Error(), "500")
	}
	known, sd := ratings.Cached([]int{254, 1114, 971, 9999})
	assert.Equal(t, map[int]float64{254: 40.5, 1114: 33.25, 971: 21}, known)
	assert.Equal(t, 41.5, sd)

	// Fresh entries are not fetched again.
	before := requests.Load()
	assert.NoError(t, ratings.Refresh([]int{254, 1114}, time.Hour))
	assert.Equal(t, before, requests.Load())

	// Offline: a new instance reads the file and needs no network at all.
	server.Close()
	offline := NewRatings(server.URL+"/v3", 2026, cachePath)
	known, sd = offline.Cached([]int{254, 971})
	assert.Equal(t, map[int]float64{254: 40.5, 971: 21}, known)
	assert.Equal(t, 41.5, sd)
	err = offline.Refresh([]int{118}, time.Hour)
	assert.Error(t, err)
	known, _ = offline.Cached([]int{254})
	assert.Equal(t, 40.5, known[254], "a failed refresh keeps what was cached")

	// A cache made for another year or source is not used.
	otherYear := NewRatings(server.URL+"/v3", 2027, cachePath)
	known, _ = otherYear.Cached([]int{254})
	assert.Empty(t, known)
	assert.True(t, strings.HasSuffix(cachePath, "ratings.json"))
}
