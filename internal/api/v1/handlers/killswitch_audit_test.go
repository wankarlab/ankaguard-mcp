package handlers

import (
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/rantipay/trading-mcp/internal/killswitch"
)

// The handler must create audit_log at construction so writeAudit never fails
// with "no such table" on a fresh local SQLite DB.
func TestKillSwitchHandler_CreatesAuditTable(t *testing.T) {
	t.Cleanup(func() { killswitch.SetRing0Active(false) })
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	h := NewKillSwitchHandler(db, nil, logrus.New())
	h.writeAudit("tester", "ring0_global_stop", "*")

	var n int64
	if err := db.Raw(`SELECT COUNT(*) FROM audit_log WHERE actor = ? AND action = ?`, "tester", "ring0_global_stop").Scan(&n).Error; err != nil {
		t.Fatalf("audit_log query failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 audit row, got %d", n)
	}
}
