package server

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// Escenario "Tool sin clasificar": el arranque registra un error que nombra la
// tool (la parte de no listarla y rechazarla ya la cubre
// TestUnclassifiedToolIsNeverListedNorExecuted).
func TestAuditToolClassificationLogsAnErrorNamingTheTool(t *testing.T) {
	h := newPermHarness(t)
	var buf bytes.Buffer
	h.srv.log.SetOutput(&buf)
	h.srv.log.SetLevel(logrus.ErrorLevel)

	missing := h.srv.AuditToolClassification()

	assert.Equal(t, []string{"mystery_tool"}, missing)
	out := buf.String()
	assert.Contains(t, out, "level=error")
	assert.Contains(t, out, "mystery_tool")
	// Las tools clasificadas no generan ruido.
	assert.NotContains(t, out, toolRead)
	assert.NotContains(t, out, toolIncrease)
}

// Escenario "Contenido del rechazo": el error nombra tool, clase y perfil y no
// arrastra ningún otro dato (mercado, cuenta, argumentos de la llamada).
func TestPermissionDeniedCarriesOnlyToolClassAndProfile(t *testing.T) {
	h := newPermHarness(t)
	args := map[string]interface{}{"pair": "BTC-USDT", "balance": 123456.78}
	resp := h.call(t, mintToken(t, 1, "read", "jti-keys"), toolIncrease, args)
	if resp.Error == nil {
		t.Fatal("expected a PERMISSION_DENIED error")
	}
	data, ok := resp.Error.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("error data has type %T", resp.Error.Data)
	}
	keys := map[string]bool{}
	for k := range data {
		keys[k] = true
	}
	for k := range keys {
		switch k {
		case "code", "tool", "class", "profile", "message", "reason":
		default:
			t.Errorf("rejection carries unexpected field %q", k)
		}
	}
	for _, k := range []string{"tool", "class", "profile"} {
		if !keys[k] {
			t.Errorf("rejection misses %q", k)
		}
	}
	raw := resp.Error.Message
	for _, v := range data {
		raw += " " + stringify(v)
	}
	for _, leak := range []string{"BTC-USDT", "123456"} {
		if strings.Contains(raw, leak) {
			t.Errorf("rejection leaks call data %q: %s", leak, raw)
		}
	}
}

func stringify(v interface{}) string { return fmt.Sprint(v) }
