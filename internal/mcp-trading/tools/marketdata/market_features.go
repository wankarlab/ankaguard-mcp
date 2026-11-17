package marketdata

// MarketFeatures describes the capabilities and characteristics of a market type
type MarketFeatures struct {
	HasLeverage     bool `json:"has_leverage"`
	HasFundingRate  bool `json:"has_funding_rate"`
	HasLiquidation  bool `json:"has_liquidation"`
	HasInterestRate bool `json:"has_interest_rate"`
	HasExpiration   bool `json:"has_expiration,omitempty"`
}

// GetMarketFeatures returns the features for a specific market type
func GetMarketFeatures(marketType string) MarketFeatures {
	switch marketType {
	case "spot":
		return MarketFeatures{
			HasLeverage:     false,
			HasFundingRate:  false,
			HasLiquidation:  false,
			HasInterestRate: false,
		}
	case "futures":
		return MarketFeatures{
			HasLeverage:     true,
			HasFundingRate:  true,
			HasLiquidation:  true,
			HasInterestRate: false,
		}
	case "margin":
		return MarketFeatures{
			HasLeverage:     true,
			HasFundingRate:  false,
			HasLiquidation:  true,
			HasInterestRate: true,
		}
	case "options":
		return MarketFeatures{
			HasLeverage:     false,
			HasFundingRate:  false,
			HasLiquidation:  false,
			HasInterestRate: false,
			HasExpiration:   true,
		}
	default:
		return MarketFeatures{}
	}
}

// GetAllMarketTypes returns a list of all supported market types
func GetAllMarketTypes() []string {
	return []string{"spot", "futures", "margin", "options"}
}

// GetMarketTypeDescription returns a human-readable description of a market type
func GetMarketTypeDescription(marketType string) string {
	descriptions := map[string]string{
		"spot":    "Spot trading - direct buy/sell without leverage",
		"futures": "Futures trading - contracts with leverage and expiration",
		"margin":  "Margin trading - trading with borrowed funds",
		"options": "Options trading - call/put contracts",
	}

	if desc, ok := descriptions[marketType]; ok {
		return desc
	}
	return ""
}
