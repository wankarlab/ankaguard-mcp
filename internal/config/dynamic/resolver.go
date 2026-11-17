package dynamic

import "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"

// resolve walks the scope chain from most-specific to least-specific and returns the first
// matching value. Falls back to the registry default. Returns "" if the key is unknown.
func (s *ScopedSnapshot) resolve(key string) string {
	val, _ := s.resolveOk(key)
	return val
}

// resolveOk is the presence-aware variant of resolve. It returns the resolved raw value and
// found=true when the key has either an explicit scope override OR a registry/manifest default.
// found is false only for keys that are not registered anywhere (truly unknown). This lets
// callers distinguish a deliberately configured value (including 0 / "") from "not configured",
// which a plain != 0 / != "" check cannot do.
//
// Empty-string values stored in the DB are treated as "not set" and skipped so that the
// registry default (from the manifest) is used instead. This prevents log spam when rows
// exist in parameter_values with value="" (e.g. keys added to the manifest before the
// corresponding DB rows were backfilled with real values).
func (s *ScopedSnapshot) resolveOk(key string) (value string, found bool) {
	for _, ref := range s.chain {
		if val, ok := s.values[scopeKey{Key: key, ScopeType: ref.Type, ScopeID: ref.ID}]; ok {
			// Treat an explicitly stored empty string as "not configured at this scope level"
			// and fall through to the next scope or the registry default.
			if val != "" {
				return val, true
			}
		}
	}
	if reg, ok := s.registry[key]; ok {
		return reg.DefaultValue, true
	}
	return "", false
}

// BotScopeChain constructs the standard scope resolution chain for a running bot.
// Precedence (most specific first): bot_instance → pair → exchange → global.
// Empty strings are skipped so callers can omit levels they do not have.
func BotScopeChain(botID, pair, exchange string) []ScopeRef {
	chain := make([]ScopeRef, 0, 4)
	if botID != "" {
		chain = append(chain, ScopeRef{Type: domain.ScopeBotInstance, ID: botID})
	}
	if pair != "" {
		chain = append(chain, ScopeRef{Type: domain.ScopePair, ID: pair})
	}
	if exchange != "" {
		chain = append(chain, ScopeRef{Type: domain.ScopeExchange, ID: exchange})
	}
	chain = append(chain, ScopeRef{Type: domain.ScopeGlobal, ID: "*"})
	return chain
}
