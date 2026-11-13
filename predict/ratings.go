package predict

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultRatingsURL is Statbotics' API. Any service answering the same two requests can be used instead.
const DefaultRatingsURL = "https://api.statbotics.io/v3"

// Ratings fetches team ratings from a Statbotics-compatible API and keeps them in a file, so predictions keep
// working with no internet once the ratings have been fetched once.
//
// The contract it relies on is small:
//
//	GET {base}/team_year/{team}/{year}  ->  {"epa": {"total_points": <number or {"mean": number}>,
//	                                                 "breakdown": {"total_points": number}}}
//	GET {base}/year/{year}              ->  {"score_sd": number, "score_mean": number}
//
// (In June 2026 Statbotics changed epa.total_points from an object to a number; both are accepted.)
type Ratings struct {
	BaseURL   string
	Year      int
	CachePath string
	Client    *http.Client

	mu    sync.Mutex
	cache ratingsCache
	ready bool
}

type ratingsCache struct {
	BaseURL string                 `json:"baseUrl"`
	Year    int                    `json:"year"`
	ScoreSD float64                `json:"scoreSd"`
	Teams   map[string]cachedEntry `json:"teams"` // Team number -> rating.
}

type cachedEntry struct {
	Rating    float64   `json:"rating"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// NewRatings creates a ratings source. The cache file is read lazily.
func NewRatings(baseURL string, year int, cachePath string) *Ratings {
	if baseURL == "" {
		baseURL = DefaultRatingsURL
	}
	return &Ratings{
		BaseURL: baseURL, Year: year, CachePath: cachePath, Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// load reads the cache file once; a cache for a different source or year is ignored.
func (r *Ratings) load() {
	if r.ready {
		return
	}
	r.ready = true
	r.cache = ratingsCache{BaseURL: r.BaseURL, Year: r.Year, Teams: map[string]cachedEntry{}}
	data, err := os.ReadFile(r.CachePath)
	if err != nil {
		return
	}
	var cached ratingsCache
	if json.Unmarshal(data, &cached) == nil && cached.BaseURL == r.BaseURL && cached.Year == r.Year &&
		cached.Teams != nil {
		r.cache = cached
	}
}

func (r *Ratings) save() error {
	data, err := json.MarshalIndent(r.cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.CachePath), 0755); err != nil {
		return err
	}
	temporary := r.CachePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0644); err != nil {
		return err
	}
	return os.Rename(temporary, r.CachePath)
}

// Cached returns the ratings already known, without any network access: the alliance ratings to predict with, and
// the season's score spread. Teams with no rating are left out of the map.
func (r *Ratings) Cached(teams []int) (map[int]float64, float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	out := make(map[int]float64, len(teams))
	for _, team := range teams {
		if entry, ok := r.cache.Teams[fmt.Sprint(team)]; ok {
			out[team] = entry.Rating
		}
	}
	return out, r.cache.ScoreSD
}

// Refresh fetches the ratings of the given teams (and the season's spread) that are missing or older than maxAge,
// and stores them. Network failures leave the cache as it was and are returned together.
func (r *Ratings) Refresh(teams []int, maxAge time.Duration) error {
	r.mu.Lock()
	r.load()
	needSD := r.cache.ScoreSD == 0
	var stale []int
	for _, team := range teams {
		if team <= 0 {
			continue
		}
		entry, ok := r.cache.Teams[fmt.Sprint(team)]
		if !ok || time.Since(entry.FetchedAt) > maxAge {
			stale = append(stale, team)
		}
	}
	r.mu.Unlock()

	var problems []error
	fetched := map[string]cachedEntry{}
	scoreSD := 0.0
	if needSD {
		var year struct {
			ScoreSD float64 `json:"score_sd"`
		}
		if err := r.getJSON(fmt.Sprintf("%s/year/%d", r.BaseURL, r.Year), &year); err != nil {
			problems = append(problems, err)
		} else {
			scoreSD = year.ScoreSD
		}
	}
	for _, team := range stale {
		var teamYear struct {
			EPA struct {
				TotalPoints json.RawMessage `json:"total_points"`
				Breakdown   struct {
					TotalPoints *float64 `json:"total_points"`
				} `json:"breakdown"`
			} `json:"epa"`
		}
		if err := r.getJSON(fmt.Sprintf("%s/team_year/%d/%d", r.BaseURL, team, r.Year), &teamYear); err != nil {
			problems = append(problems, err)
			continue
		}
		rating, ok := parseTotalPoints(teamYear.EPA.TotalPoints)
		if !ok && teamYear.EPA.Breakdown.TotalPoints != nil {
			rating, ok = *teamYear.EPA.Breakdown.TotalPoints, true
		}
		if !ok {
			problems = append(problems, fmt.Errorf("team %d: no rating in the response", team))
			continue
		}
		fetched[fmt.Sprint(team)] = cachedEntry{Rating: rating, FetchedAt: time.Now().UTC()}
	}

	if len(fetched) > 0 || scoreSD > 0 {
		r.mu.Lock()
		for team, entry := range fetched {
			r.cache.Teams[team] = entry
		}
		if scoreSD > 0 {
			r.cache.ScoreSD = scoreSD
		}
		err := r.save()
		r.mu.Unlock()
		if err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// parseTotalPoints accepts both shapes Statbotics has used for epa.total_points: a number, or {"mean": number}.
func parseTotalPoints(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return number, true
	}
	var object struct {
		Mean *float64 `json:"mean"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Mean != nil {
		return *object.Mean, true
	}
	return 0, false
}

func (r *Ratings) getJSON(url string, out any) error {
	response, err := r.Client.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, response.Body)
		return fmt.Errorf("%s: %s", url, response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
}
