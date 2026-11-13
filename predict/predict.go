// Package predict estimates who will win a match: before it starts, from the teams' ratings, and while it runs,
// from the score and the time left. Predictors sit behind one interface so others can be added.
package predict

import (
	"math"
)

// Prediction is the chance of each alliance winning, and the score each is expected to finish with.
type Prediction struct {
	WinProbability map[string]float64 `json:"winProbability"` // Alliance id -> probability; they sum to 1.
	Expected       map[string]float64 `json:"expected"`       // Alliance id -> expected final score.
	Source         string             `json:"source"`         // What the prediction is based on.
}

// Alliance is what a predictor knows about one alliance.
type Alliance struct {
	ID string
	// Rating is the alliance's expected score over a whole match: the sum of its teams' ratings. Zero when unknown.
	Rating float64
	// Score is the alliance's score so far. Zero before the match.
	Score float64
}

// Input is everything a predictor may use.
type Input struct {
	Alliances []Alliance
	// ScoreSD is the standard deviation of one alliance's score over a match, which sets how sure a predictor can
	// be. Ratings sources publish one per season; without one, use the spread of this event's own results.
	ScoreSD float64
	// Remaining is the fraction of the match still to play: 1 before it starts, 0 at the end.
	Remaining float64
}

// Predictor turns an Input into a Prediction.
type Predictor interface {
	Predict(input Input) Prediction
}

// statboticsK is the slope Statbotics uses to turn a rating difference, in score standard deviations, into a
// win probability: p = 1 / (1 + 10^(k × difference)).
const statboticsK = -5.0 / 8.0

// PreMatch predicts from ratings only, as Statbotics does.
type PreMatch struct{}

func (PreMatch) Predict(input Input) Prediction {
	prediction := Prediction{
		WinProbability: map[string]float64{}, Expected: map[string]float64{}, Source: "ratings",
	}
	for _, alliance := range input.Alliances {
		prediction.Expected[alliance.ID] = alliance.Rating
	}
	switch {
	case len(input.Alliances) == 0:
		return prediction
	case len(input.Alliances) == 2 && input.ScoreSD > 0:
		// Exactly Statbotics' formula, so predictions agree with the site teams already know.
		a, b := input.Alliances[0], input.Alliances[1]
		difference := (a.Rating - b.Rating) / input.ScoreSD
		pa := 1 / (1 + math.Pow(10, statboticsK*difference))
		prediction.WinProbability[a.ID], prediction.WinProbability[b.ID] = pa, 1-pa
	default:
		means := make([]float64, len(input.Alliances))
		for i, alliance := range input.Alliances {
			means[i] = alliance.Rating
		}
		setProbabilities(prediction.WinProbability, input.Alliances, highestOfNormals(means, input.ScoreSD))
	}
	return prediction
}

// Live predicts during a match: each alliance finishes with its score so far plus the share of its rating still to
// come, give or take a spread that shrinks as the match runs out. With no ratings it is the score so far plus an
// equal share for everyone, which still favours the leader more and more as time runs out.
type Live struct{}

func (Live) Predict(input Input) Prediction {
	prediction := Prediction{
		WinProbability: map[string]float64{}, Expected: map[string]float64{}, Source: "live",
	}
	if len(input.Alliances) == 0 {
		return prediction
	}
	remaining := math.Max(0, math.Min(1, input.Remaining))
	means := make([]float64, len(input.Alliances))
	for i, alliance := range input.Alliances {
		means[i] = alliance.Score + alliance.Rating*remaining
		prediction.Expected[alliance.ID] = means[i]
	}
	setProbabilities(prediction.WinProbability, input.Alliances, highestOfNormals(means, input.ScoreSD*math.Sqrt(remaining)))
	return prediction
}

func setProbabilities(out map[string]float64, alliances []Alliance, probabilities []float64) {
	for i, alliance := range alliances {
		out[alliance.ID] = probabilities[i]
	}
}

// highestOfNormals returns, for independent normal scores with the given means and a common standard deviation,
// the probability that each is the highest. Ties (only possible with no spread) are shared equally.
func highestOfNormals(means []float64, sd float64) []float64 {
	n := len(means)
	probabilities := make([]float64, n)
	if n == 1 {
		probabilities[0] = 1
		return probabilities
	}
	if sd <= 1e-9 {
		best := math.Inf(-1)
		for _, mean := range means {
			best = math.Max(best, mean)
		}
		var leaders []int
		for i, mean := range means {
			if mean == best {
				leaders = append(leaders, i)
			}
		}
		for _, i := range leaders {
			probabilities[i] = 1 / float64(len(leaders))
		}
		return probabilities
	}
	// P(i is highest) = ∫ φ(x; μi, σ) × Π_{j≠i} Φ(x; μj, σ) dx, by the trapezoid rule over ±8σ around the means.
	low, high := means[0], means[0]
	for _, mean := range means {
		low, high = math.Min(low, mean), math.Max(high, mean)
	}
	low -= 8 * sd
	high += 8 * sd
	const steps = 4000
	step := (high - low) / steps
	total := 0.0
	for i := range means {
		sum := 0.0
		for k := 0; k <= steps; k++ {
			x := low + float64(k)*step
			value := normalDensity(x, means[i], sd)
			for j, mean := range means {
				if j != i {
					value *= normalCumulative(x, mean, sd)
				}
			}
			if k == 0 || k == steps {
				value /= 2
			}
			sum += value
		}
		probabilities[i] = sum * step
		total += probabilities[i]
	}
	for i := range probabilities {
		probabilities[i] /= total
	}
	return probabilities
}

func normalDensity(x, mean, sd float64) float64 {
	z := (x - mean) / sd
	return math.Exp(-z*z/2) / (sd * math.Sqrt(2*math.Pi))
}

func normalCumulative(x, mean, sd float64) float64 {
	return 0.5 * math.Erfc(-(x-mean)/(sd*math.Sqrt2))
}

// EventScoreSD estimates the standard deviation of an alliance's score from this event's own results, for when no
// ratings source is available. It returns 0 with fewer than four scores, which predictors treat as "no spread
// known" and fall back to the leader.
func EventScoreSD(scores []float64) float64 {
	if len(scores) < 4 {
		return 0
	}
	mean := 0.0
	for _, score := range scores {
		mean += score
	}
	mean /= float64(len(scores))
	variance := 0.0
	for _, score := range scores {
		variance += (score - mean) * (score - mean)
	}
	return math.Sqrt(variance / float64(len(scores)-1))
}
