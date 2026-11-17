package xsec

import (
	"reflect"
	"testing"
)

const sampleContracts = `{
  "code": "200000",
  "data": [
    {"symbol": "XBTUSDTM", "turnoverOf24h": 5000000.0},
    {"symbol": "ETHUSDTM", "turnoverOf24h": 3000000.0},
    {"symbol": "SOLUSDTM", "turnoverOf24h": 1000000.0},
    {"symbol": "TINYUSDTM", "turnoverOf24h": 500.0},
    {"symbol": "XBTUSDM", "turnoverOf24h": 9000000.0},
    {"symbol": "ETHUSDCM", "turnoverOf24h": 8000000.0}
  ]
}`

func TestParseLiquid_MappingFilterAndSort(t *testing.T) {
	got, err := parseLiquid([]byte(sampleContracts), 1_000_000)
	if err != nil {
		t.Fatalf("parseLiquid returned error: %v", err)
	}

	// Expected: only USDTM symbols with turnover >= 1M, XBT->BTC,
	// sorted by turnover descending. TINY (below threshold), XBTUSDM
	// (inverse, non-USDTM) and ETHUSDCM (USDC quote) must be excluded.
	want := []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pairs:\n got=%v\nwant=%v", got, want)
	}
}

func TestParseLiquid_XBTMapping(t *testing.T) {
	got, err := parseLiquid([]byte(`{"code":"200000","data":[{"symbol":"XBTUSDTM","turnoverOf24h":1.0}]}`), 0)
	if err != nil {
		t.Fatalf("parseLiquid returned error: %v", err)
	}
	want := []string{"BTC-USDT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("XBT mapping failed: got=%v want=%v", got, want)
	}
}

func TestParseLiquid_TurnoverFilter(t *testing.T) {
	got, err := parseLiquid([]byte(sampleContracts), 4_000_000)
	if err != nil {
		t.Fatalf("parseLiquid returned error: %v", err)
	}
	want := []string{"BTC-USDT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turnover filter failed: got=%v want=%v", got, want)
	}
}

func TestParseLiquid_SortDescending(t *testing.T) {
	// Provide out-of-order input to confirm descending sort by turnover.
	in := `{"code":"200000","data":[
		{"symbol":"SOLUSDTM","turnoverOf24h":1.0},
		{"symbol":"ETHUSDTM","turnoverOf24h":3.0},
		{"symbol":"XBTUSDTM","turnoverOf24h":2.0}
	]}`
	got, err := parseLiquid([]byte(in), 0)
	if err != nil {
		t.Fatalf("parseLiquid returned error: %v", err)
	}
	want := []string{"ETH-USDT", "BTC-USDT", "SOL-USDT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sort order failed: got=%v want=%v", got, want)
	}
}

func TestParseLiquid_InvalidJSON(t *testing.T) {
	if _, err := parseLiquid([]byte("not json"), 0); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestParseLiquid_EmptyData(t *testing.T) {
	got, err := parseLiquid([]byte(`{"code":"200000","data":[]}`), 0)
	if err != nil {
		t.Fatalf("parseLiquid returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
}
