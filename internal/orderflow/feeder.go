package orderflow

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultFeederInterval = 5 * time.Second
	defaultFeederDepth    = 20
)

// OrderBookFetcher abstracts how snapshots are obtained (REST polling, WS, mock).
type OrderBookFetcher interface {
	GetOrderBook(ctx context.Context, exchange, pair string, depth int) (*OrderBookSnapshot, error)
}

// FlowSubscriber receives every FlowAnalysis emitted by the Feeder.
// Implementations MUST NOT block: notify is called synchronously
// to preserve event ordering across subscribers.
type FlowSubscriber interface {
	OnFlowAnalysis(pair string, analysis *FlowAnalysis)
}

// Feeder polls order books on a fixed interval, runs them through the
// Analyzer, and broadcasts the resulting FlowAnalysis to all subscribers.
type Feeder struct {
	log      *logrus.Logger
	fetcher  OrderBookFetcher
	analyzer *Analyzer

	subscribers   []FlowSubscriber
	subscribersMu sync.RWMutex

	interval time.Duration
	depth    int

	pairs   map[string]string // pair -> exchange
	pairsMu sync.RWMutex

	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	runMu   sync.Mutex
}

// NewFeeder builds a Feeder with sane defaults if interval <= 0.
func NewFeeder(log *logrus.Logger, fetcher OrderBookFetcher, analyzer *Analyzer, interval time.Duration) *Feeder {
	if log == nil {
		log = logrus.New()
	}
	if interval <= 0 {
		interval = defaultFeederInterval
	}
	return &Feeder{
		log:      log,
		fetcher:  fetcher,
		analyzer: analyzer,
		interval: interval,
		depth:    defaultFeederDepth,
		pairs:    make(map[string]string),
	}
}

// Subscribe registers a consumer for FlowAnalysis events.
// Multiple subscribers are supported; they are invoked synchronously
// in registration order on each tick.
func (f *Feeder) Subscribe(subscriber FlowSubscriber) {
	if subscriber == nil {
		return
	}
	f.subscribersMu.Lock()
	defer f.subscribersMu.Unlock()
	f.subscribers = append(f.subscribers, subscriber)
}

// AddPair registers a pair to monitor on the given exchange.
// If the pair already exists its exchange is updated.
func (f *Feeder) AddPair(pair, exchange string) {
	if pair == "" || exchange == "" {
		return
	}
	f.pairsMu.Lock()
	defer f.pairsMu.Unlock()
	f.pairs[pair] = exchange
}

// RemovePair stops monitoring the given pair.
func (f *Feeder) RemovePair(pair string) {
	f.pairsMu.Lock()
	defer f.pairsMu.Unlock()
	delete(f.pairs, pair)
}

// Start launches the polling loop in a background goroutine.
// Returns an error if already running or misconfigured.
func (f *Feeder) Start(ctx context.Context) error {
	f.runMu.Lock()
	defer f.runMu.Unlock()

	if f.running {
		return errors.New("orderflow feeder: already running")
	}
	if f.fetcher == nil {
		return errors.New("orderflow feeder: fetcher is nil")
	}
	if f.analyzer == nil {
		return errors.New("orderflow feeder: analyzer is nil")
	}

	f.stopCh = make(chan struct{})
	f.doneCh = make(chan struct{})
	f.running = true

	go f.loop(ctx)

	f.log.WithFields(logrus.Fields{
		"interval": f.interval.String(),
		"depth":    f.depth,
	}).Info("orderflow feeder started")
	return nil
}

// Stop signals the loop to exit and waits for it to finish.
func (f *Feeder) Stop() error {
	f.runMu.Lock()
	if !f.running {
		f.runMu.Unlock()
		return nil
	}
	close(f.stopCh)
	done := f.doneCh
	f.running = false
	f.runMu.Unlock()

	if done != nil {
		<-done
	}
	f.log.Info("orderflow feeder stopped")
	return nil
}

// IsRunning reports whether the loop is active.
func (f *Feeder) IsRunning() bool {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	return f.running
}

func (f *Feeder) loop(ctx context.Context) {
	defer close(f.doneCh)

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.stopCh:
			return
		case <-ticker.C:
			f.tickAllPairs(ctx)
		}
	}
}

func (f *Feeder) tickAllPairs(ctx context.Context) {
	f.pairsMu.RLock()
	snapshot := make(map[string]string, len(f.pairs))
	for p, e := range f.pairs {
		snapshot[p] = e
	}
	f.pairsMu.RUnlock()

	if len(snapshot) == 0 {
		return
	}

	var wg sync.WaitGroup
	results := make(chan struct {
		pair     string
		analysis *FlowAnalysis
	}, len(snapshot))

	for pair, exchange := range snapshot {
		wg.Add(1)
		go func(p, e string) {
			defer wg.Done()
			ob, err := f.fetcher.GetOrderBook(ctx, e, p, f.depth)
			if err != nil {
				f.log.WithError(err).WithField("pair", p).Debug("orderflow feeder: fetch failed")
				return
			}
			analysis, err := f.analyzer.Analyze(ob)
			if err != nil {
				f.log.WithError(err).WithField("pair", p).Debug("orderflow feeder: analyze failed")
				return
			}
			results <- struct {
				pair     string
				analysis *FlowAnalysis
			}{p, analysis}
		}(pair, exchange)
	}

	wg.Wait()
	close(results)

	// Deliver synchronously after all fetches complete to preserve
	// per-tick ordering across subscribers.
	for r := range results {
		f.notify(r.pair, r.analysis)
	}
}

func (f *Feeder) notify(pair string, analysis *FlowAnalysis) {
	f.subscribersMu.RLock()
	subs := make([]FlowSubscriber, len(f.subscribers))
	copy(subs, f.subscribers)
	f.subscribersMu.RUnlock()

	for _, s := range subs {
		s.OnFlowAnalysis(pair, analysis)
	}
}
