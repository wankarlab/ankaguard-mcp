package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
)

// ConsentTemplateRow is one versioned legal-text template (C1.3). The body lives
// in the DB so the legal copy is auditable and updatable without a redeploy.
type ConsentTemplateRow struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	BodyMD  string `json:"body_md"`
}

// ConsentStatus is the per-user acceptance state for one consent kind.
type ConsentStatus struct {
	Kind            string `json:"kind"`
	Accepted        bool   `json:"accepted"`
	AcceptedVersion int    `json:"accepted_version"`
}

// ConsentRepository reads/seeds consent_template and records consent_record
// (migration 029). Append-only legal audit trail.
type ConsentRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewConsentRepository constructs a ConsentRepository.
func NewConsentRepository(db *sql.DB, log *logrus.Logger) *ConsentRepository {
	if log == nil {
		log = logrus.New()
	}
	return &ConsentRepository{db: db, log: log}
}

// LatestTemplate returns the highest-version template for a kind, or nil when
// none exists.
func (r *ConsentRepository) LatestTemplate(ctx context.Context, kind string) (*ConsentTemplateRow, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT kind, version, body_md
		  FROM consent_template
		 WHERE kind = ?
		 ORDER BY version DESC
		 LIMIT 1
	`, kind)

	t := &ConsentTemplateRow{}
	if err := row.Scan(&t.Kind, &t.Version, &t.BodyMD); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("latest consent_template %q: %w", kind, err)
	}
	return t, nil
}

// Status returns whether the user has accepted the given kind and, if so, the
// highest template_version they accepted.
func (r *ConsentRepository) Status(ctx context.Context, userID, kind string) (*ConsentStatus, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(template_version), 0)
		  FROM consent_record
		 WHERE user_id = ? AND kind = ?
	`, userID, kind)

	var maxVersion int
	if err := row.Scan(&maxVersion); err != nil {
		return nil, fmt.Errorf("consent status %q/%q: %w", userID, kind, err)
	}
	return &ConsentStatus{
		Kind:            kind,
		Accepted:        maxVersion > 0,
		AcceptedVersion: maxVersion,
	}, nil
}

// Accept records a consent acceptance idempotently. The UNIQUE(user_id, kind,
// template_version) constraint makes a repeated POST a no-op (INSERT OR IGNORE).
func (r *ConsentRepository) Accept(ctx context.Context, userID, tenantID, kind string, templateVersion int, ip string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO consent_record (user_id, tenant_id, kind, template_version, ip)
		VALUES (?, ?, ?, ?, ?)
	`, userID, nullString(tenantID), kind, templateVersion, nullString(ip))
	if err != nil {
		return fmt.Errorf("accept consent %q/%q v%d: %w", userID, kind, templateVersion, err)
	}
	return nil
}

// SeedIfEmpty inserts the canonical copy_trading template when no template for
// that kind exists. Idempotent. The legal text is real Spanish copy stored in
// the DB so it can be updated (bumping the version) without redeploying.
func (r *ConsentRepository) SeedIfEmpty(ctx context.Context) error {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM consent_template WHERE kind = 'copy_trading'`).Scan(&count); err != nil {
		return fmt.Errorf("count consent_template: %w", err)
	}
	if count > 0 {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO consent_template (kind, version, body_md)
		VALUES ('copy_trading', 1, ?)
		ON CONFLICT(kind, version) DO NOTHING
	`, copyTradingConsentV1)
	if err != nil {
		return fmt.Errorf("seed consent_template copy_trading: %w", err)
	}
	r.log.Info("storage: seeded consent_template copy_trading v1")
	return nil
}

// copyTradingConsentV1 is the versioned legal text (Spanish) for the
// copy-trading informed-consent gate. Stored in the DB on first boot; bump the
// version with a new row to update it.
const copyTradingConsentV1 = `# Consentimiento informado — Copy Trading

Al copiar una estrategia, declaro entender y aceptar lo siguiente:

1. **Riesgo de pérdida total.** El trading de criptoactivos con apalancamiento
   puede generar la pérdida total del capital asignado. El rendimiento pasado de
   una estrategia **no garantiza** resultados futuros.

2. **Operación automática.** Al activar la copia, las órdenes se ejecutan de
   forma automática en mi cuenta, sin confirmación manual por operación.

3. **Sin asesoría financiera.** La plataforma no presta asesoría de inversión.
   Las estrategias son creadas por terceros y se publican con fines informativos.

4. **Comisión de desempeño.** Acepto la comisión de desempeño vigente publicada
   en la plataforma, calculada únicamente sobre ganancias netas (high-water mark).

5. **Control del usuario.** Puedo detener la copia en cualquier momento; las
   posiciones abiertas seguirán las reglas de salida de la estrategia hasta su
   cierre o hasta que las cierre manualmente.

Confirmo que asigno únicamente capital que estoy dispuesto a perder.`
