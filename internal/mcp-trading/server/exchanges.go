package server

import (
	"fmt"
	"strings"
)

// ExchangeManager maneja la validación y configuración de exchanges
type ExchangeManager struct {
	supportedExchanges map[string]ExchangeInfo
	enabledExchanges   map[string]bool
}

// ExchangeInfo contiene información de un exchange
type ExchangeInfo struct {
	Name        string
	DisplayName string
	RateLimit   int // requests por segundo
	Sandbox     bool
}

// NewExchangeManager crea un nuevo exchange manager
func NewExchangeManager() *ExchangeManager {
	return &ExchangeManager{
		supportedExchanges: map[string]ExchangeInfo{
			"kucoin": {
				Name:        "kucoin",
				DisplayName: "KuCoin",
				RateLimit:   10,
				Sandbox:     true,
			},
			"binance": {
				Name:        "binance",
				DisplayName: "Binance",
				RateLimit:   20,
				Sandbox:     true,
			},
			"coinbase": {
				Name:        "coinbase",
				DisplayName: "Coinbase",
				RateLimit:   10,
				Sandbox:     true,
			},
			"kraken": {
				Name:        "kraken",
				DisplayName: "Kraken",
				RateLimit:   15,
				Sandbox:     false,
			},
			"bitfinex": {
				Name:        "bitfinex",
				DisplayName: "Bitfinex",
				RateLimit:   10,
				Sandbox:     false,
			},
		},
		enabledExchanges: map[string]bool{
			"kucoin":   true,
			"binance":  true,
			"coinbase": true,
		},
	}
}

// IsSupported verifica si un exchange está soportado
func (em *ExchangeManager) IsSupported(exchange string) bool {
	exchange = strings.ToLower(exchange)
	_, exists := em.supportedExchanges[exchange]
	return exists
}

// IsEnabled verifica si un exchange está habilitado
func (em *ExchangeManager) IsEnabled(exchange string) bool {
	exchange = strings.ToLower(exchange)
	return em.enabledExchanges[exchange]
}

// ValidateExchange valida que un exchange sea soportado y esté habilitado
func (em *ExchangeManager) ValidateExchange(exchange string) error {
	exchange = strings.ToLower(exchange)

	if !em.IsSupported(exchange) {
		return fmt.Errorf("exchange '%s' is not supported", exchange)
	}

	if !em.IsEnabled(exchange) {
		return fmt.Errorf("exchange '%s' is not enabled", exchange)
	}

	return nil
}

// GetExchangeInfo obtiene información de un exchange
func (em *ExchangeManager) GetExchangeInfo(exchange string) (ExchangeInfo, error) {
	exchange = strings.ToLower(exchange)

	info, exists := em.supportedExchanges[exchange]
	if !exists {
		return ExchangeInfo{}, fmt.Errorf("exchange '%s' not found", exchange)
	}

	return info, nil
}

// GetSupportedExchanges retorna lista de exchanges soportados
func (em *ExchangeManager) GetSupportedExchanges() []string {
	exchanges := make([]string, 0, len(em.supportedExchanges))
	for name := range em.supportedExchanges {
		exchanges = append(exchanges, name)
	}
	return exchanges
}

// GetEnabledExchanges retorna lista de exchanges habilitados
func (em *ExchangeManager) GetEnabledExchanges() []string {
	exchanges := make([]string, 0, len(em.enabledExchanges))
	for name, enabled := range em.enabledExchanges {
		if enabled {
			exchanges = append(exchanges, name)
		}
	}
	return exchanges
}

// EnableExchange habilita un exchange
func (em *ExchangeManager) EnableExchange(exchange string) error {
	exchange = strings.ToLower(exchange)

	if !em.IsSupported(exchange) {
		return fmt.Errorf("exchange '%s' is not supported", exchange)
	}

	em.enabledExchanges[exchange] = true
	return nil
}

// DisableExchange deshabilita un exchange
func (em *ExchangeManager) DisableExchange(exchange string) {
	exchange = strings.ToLower(exchange)
	em.enabledExchanges[exchange] = false
}
