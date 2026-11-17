package xsec

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// ErrLTRUnavailable is returned (wrapped) by LTRScorer.Scores for any failure
// that should make the caller fall back to the momentum baseline: ml-server
// down, model missing (503), malformed response, or not enough scorable
// pairs. Check with errors.Is(err, ErrLTRUnavailable).
var ErrLTRUnavailable = errors.New("ltr scorer unavailable")

// DefaultLTRURL is the default ml-server base URL for the /rank endpoint.
const DefaultLTRURL = "http://localhost:10601"

// LTRScorer implements Scorer using the learning-to-rank model served by
// ml-server's /rank endpoint. It computes the SAME features as the dataset
// builder (shared code in features.go), z-scores them cross-sectionally over
// the input pairs, and asks the model for scores.
type LTRScorer struct {
	candles *storage.CandleArchiveRepository
	funding *storage.FundingArchiveRepository
	db      *sql.DB
	tf      string
	url     string
	client  *http.Client
}

// NewLTRScorer creates an LTRScorer reading candles of the given timeframe
// (typically "1d") from db and scoring via the ml-server at baseURL (empty
// means DefaultLTRURL).
func NewLTRScorer(db *sql.DB, timeframe, baseURL string) *LTRScorer {
	if baseURL == "" {
		baseURL = DefaultLTRURL
	}
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return &LTRScorer{
		candles: storage.NewCandleArchiveRepository(db, log),
		funding: storage.NewFundingArchiveRepository(db, log),
		db:      db,
		tf:      timeframe,
		url:     baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

type rankRow struct {
	Pair     string             `json:"pair"`
	Features map[string]float64 `json:"features"`
}

type rankRequest struct {
	Rows []rankRow `json:"rows"`
}

type rankResponse struct {
	Scores       map[string]float64 `json:"scores"`
	ModelVersion string             `json:"model_version"`
}

// Scores implements Scorer. Features are computed from trailing windows
// ending at each pair's latest archived candle. Any failure returns an error
// wrapping ErrLTRUnavailable so callers fall back to momentum.
func (s *LTRScorer) Scores(pairs []string) (map[string]float64, time.Time, error) {
	now := time.Now()
	names := FeatureNames()

	// Market bars for beta (best effort — beta degrades to 0 without them).
	// Same benchmark preference order as the dataset builder.
	var btcBars []ai.Candle
	for _, cand := range BetaBenchmarkCandidates {
		bars, err := s.candles.LoadRange(cand, s.tf, time.Time{}, now)
		if err == nil && len(bars) >= 2 {
			btcBars = bars
			break
		}
	}

	fundingOK := s.fundingTableExists()

	var (
		scorable []string
		mat      [][]float64
		asOf     time.Time
	)
	for _, p := range pairs {
		bars, err := s.candles.LoadRange(p, s.tf, time.Time{}, now)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("%w: load candles %s: %v", ErrLTRUnavailable, p, err)
		}
		if len(bars) < MinFeatureBars {
			continue
		}
		var fr []storage.FundingRate
		if fundingOK {
			fr, _ = s.funding.LoadRange(p, time.Time{}, now)
		}
		feats, ok := ComputeFeatures(bars, btcBars, fr)
		if !ok {
			continue
		}
		scorable = append(scorable, p)
		mat = append(mat, feats)
		if last := bars[len(bars)-1].Timestamp; last.After(asOf) {
			asOf = last
		}
	}
	if len(scorable) < 2 {
		return nil, time.Time{}, fmt.Errorf("%w: only %d of %d pairs scorable", ErrLTRUnavailable, len(scorable), len(pairs))
	}

	// Cross-sectional z-score over the input universe — must mirror the
	// per-week z-scoring in the dataset builder.
	ZScoreColumns(mat)

	req := rankRequest{Rows: make([]rankRow, len(scorable))}
	for i, p := range scorable {
		fm := make(map[string]float64, len(names))
		for j, name := range names {
			fm[name] = mat[i][j]
		}
		req.Rows[i] = rankRow{Pair: p, Features: fm}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: marshal: %v", ErrLTRUnavailable, err)
	}

	resp, err := s.client.Post(s.url+"/rank", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: POST /rank: %v", ErrLTRUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("%w: /rank returned HTTP %d", ErrLTRUnavailable, resp.StatusCode)
	}
	var rr rankResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: decode /rank response: %v", ErrLTRUnavailable, err)
	}
	if len(rr.Scores) == 0 {
		return nil, time.Time{}, fmt.Errorf("%w: /rank returned no scores", ErrLTRUnavailable)
	}
	return rr.Scores, asOf, nil
}

func (s *LTRScorer) fundingTableExists() bool {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='funding_archive'`).Scan(&n)
	return err == nil && n > 0
}
