package exchange

import (
	"fmt"
	"sync"
)

// NetworkCost is a withdrawal network and its flat fee in USD.
type NetworkCost struct {
	Network string  `json:"network"`
	FeeUSD  float64 `json:"fee_usd"`
}

// TransferProposal describes a suggested capital move between two exchanges to
// correct an inventory imbalance. It is advisory only — see Inventory.Rebalance.
type TransferProposal struct {
	FromExchange string  `json:"from_exchange"`
	ToExchange   string  `json:"to_exchange"`
	AmountUSD    float64 `json:"amount_usd"`
	Network      string  `json:"network"`
	FeeUSD       float64 `json:"fee_usd"`
	Reason       string  `json:"reason"`
}

// Inventory (DM-8.4) tracks free capital (USD) per exchange and proposes
// rebalancing transfers when the spread between the most- and least-funded
// venue is large enough to justify a network fee.
//
// TODO: actual on-chain withdrawals are OUT OF SCOPE. Executing a proposal must
// be opt-in and require explicit operator confirmation; this type only computes
// suggestions and never moves funds.
type Inventory struct {
	balances map[string]float64 // exchange -> USD
	mu       sync.Mutex
}

// NewInventory builds an empty Inventory.
func NewInventory() *Inventory {
	return &Inventory{balances: make(map[string]float64)}
}

// Set records the current USD balance for an exchange.
func (i *Inventory) Set(exchange string, usd float64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.balances[exchange] = usd
}

// Get returns the recorded USD balance for an exchange (0 if unknown).
func (i *Inventory) Get(exchange string) float64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.balances[exchange]
}

// Rebalance proposes moving funds from the most-overfunded exchange to the
// most-underfunded one when (max-min)/total exceeds imbalanceThreshold. It
// picks the cheapest network from networks. The proposed amount is half the
// gap (max-min), which moves both venues toward the mean.
//
// It returns nil when:
//   - fewer than 2 exchanges are tracked,
//   - total balance is 0,
//   - the imbalance is at/under the threshold (already balanced), or
//   - the cheapest network fee would equal or exceed the rebalanced benefit
//     (the amount being moved), making the transfer not worthwhile.
func (i *Inventory) Rebalance(imbalanceThreshold float64, networks []NetworkCost) []TransferProposal {
	i.mu.Lock()
	defer i.mu.Unlock()

	if len(i.balances) < 2 {
		return nil
	}

	var (
		maxEx, minEx     string
		maxBal, minBal   float64
		total            float64
		first            = true
	)
	for ex, bal := range i.balances {
		total += bal
		if first {
			maxEx, minEx = ex, ex
			maxBal, minBal = bal, bal
			first = false
			continue
		}
		if bal > maxBal {
			maxBal, maxEx = bal, ex
		}
		if bal < minBal {
			minBal, minEx = bal, ex
		}
	}

	if total <= 0 {
		return nil
	}

	gap := maxBal - minBal
	if gap/total <= imbalanceThreshold {
		return nil // balanced
	}

	cheapest, ok := cheapestNetwork(networks)
	if !ok {
		return nil
	}

	amount := gap / 2 // move halfway toward the mean

	// The benefit of rebalancing is the capital moved; if the fee eats it (or
	// more), the transfer is not worthwhile.
	if cheapest.FeeUSD >= amount {
		return nil
	}

	return []TransferProposal{{
		FromExchange: maxEx,
		ToExchange:   minEx,
		AmountUSD:    amount,
		Network:      cheapest.Network,
		FeeUSD:       cheapest.FeeUSD,
		Reason: fmt.Sprintf(
			"imbalance %.1f%% > threshold %.1f%%: move %.2f USD from %s (%.2f) to %s (%.2f) via %s (fee %.2f)",
			gap/total*100, imbalanceThreshold*100, amount, maxEx, maxBal, minEx, minBal, cheapest.Network, cheapest.FeeUSD,
		),
	}}
}

// cheapestNetwork returns the lowest-fee network. ok is false when the list is
// empty.
func cheapestNetwork(networks []NetworkCost) (NetworkCost, bool) {
	if len(networks) == 0 {
		return NetworkCost{}, false
	}
	best := networks[0]
	for _, n := range networks[1:] {
		if n.FeeUSD < best.FeeUSD {
			best = n
		}
	}
	return best, true
}
