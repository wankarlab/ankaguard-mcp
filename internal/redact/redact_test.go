package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMapRedactsSensitiveKeysAtAnyDepth(t *testing.T) {
	in := map[string]interface{}{
		"pair":           "BTC-USDT",
		"api_key":        "key-123",
		"apiSecret":      "sec-456",
		"Passphrase":     "pp",
		"exchange_creds": map[string]interface{}{"password": "pw", "note": "ok"},
		"list":           []interface{}{map[string]interface{}{"license_key": "L-1"}, "plain"},
		"note":           "Authorization: Bearer abcdefgh12345678 end",
		"jwt":            "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig",
	}
	out := Map(in)
	raw, _ := json.Marshal(out)
	for _, leaked := range []string{"key-123", "sec-456", "\"pp\"", "\"pw\"", "L-1", "abcdefgh12345678", "eyJzdWIiOiJ4In0"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("secret %q leaked: %s", leaked, raw)
		}
	}
	if out["pair"] != "BTC-USDT" {
		t.Errorf("non sensitive value changed: %v", out["pair"])
	}
	if in["api_key"] != "key-123" {
		t.Error("input map must not be modified")
	}
}

func TestMapNil(t *testing.T) {
	if Map(nil) != nil {
		t.Error("nil map must stay nil")
	}
}
