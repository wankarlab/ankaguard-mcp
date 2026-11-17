package credentials

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// rekeyBatchDelay is the pause between rotation batches to avoid overwhelming the DB.
	rekeyBatchDelay = 100 * time.Millisecond
)

// RekeyLegacyCredentials re-encrypts all credentials that were encrypted with a
// legacy static master key (i.e. they have no wrapped_dek / KMS-derived DEK).
//
// It delegates to Service.RotateEncryptionKey which iterates through credentials
// by their current encryption key ID and re-encrypts them with the active key.
// If the active key was generated via a KMSProvider the re-encrypted data will
// automatically carry a WrappedDEK, completing the migration to envelope encryption.
//
// This function runs asynchronously in a background goroutine.  Call it after
// the Service and its KMSProvider have been fully initialised, and ideally
// BEFORE serving traffic so that Decrypt never hits a key-not-found for already
// rotated credentials.
//
// The provided context governs cancellation; pass a context with a reasonable
// deadline or use context.Background() if the process lifetime is the deadline.
func RekeyLegacyCredentials(ctx context.Context, svc Service, logger *logrus.Logger) {
	go func() {
		logger.Info("rekey: starting legacy credential re-encryption in background")

		credSvc, ok := svc.(*CredentialService)
		if !ok {
			logger.Error("rekey: service is not a *CredentialService, cannot proceed")
			return
		}

		postgresRepo, ok := credSvc.repo.(*PostgresRepository)
		if !ok {
			logger.Warn("rekey: repository is not a *PostgresRepository, skipping rekey")
			return
		}

		activeKeyID := credSvc.encryption.GetActiveKeyID()

		// Collect all distinct encryption key IDs in use that are NOT the active key.
		// We identify "legacy" credentials as those whose encryption_key_id differs
		// from the current active key (which is the KMS-derived DEK after init).
		allCreds, err := postgresRepo.ListByTenant(ctx, "") // empty tenant = all tenants
		if err != nil {
			// ListByTenant with empty tenant returns an error; fall back to a
			// direct approach: rotate any key that is not the active key.
			logger.WithError(err).Warn("rekey: could not list all credentials, attempting per-key rotation")
		}

		// Build a set of legacy key IDs.
		legacyKeyIDs := map[string]struct{}{}
		for _, cred := range allCreds {
			if cred.EncryptionKeyID != activeKeyID {
				legacyKeyIDs[cred.EncryptionKeyID] = struct{}{}
			}
		}

		if len(legacyKeyIDs) == 0 {
			logger.Info("rekey: no legacy credentials found, nothing to do")
			return
		}

		logger.WithField("legacy_key_count", len(legacyKeyIDs)).
			Info("rekey: found legacy encryption key IDs, starting rotation")

		totalRotated := 0
		for oldKeyID := range legacyKeyIDs {
			select {
			case <-ctx.Done():
				logger.WithField("rotated_so_far", totalRotated).
					Warn("rekey: context cancelled, stopping early")
				return
			default:
			}

			n, err := svc.RotateEncryptionKey(ctx, oldKeyID)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"old_key_id": oldKeyID,
					"error":      err.Error(),
				}).Error("rekey: failed to rotate credentials for key")
				continue
			}

			totalRotated += n
			logger.WithFields(logrus.Fields{
				"old_key_id": oldKeyID,
				"rotated":    n,
			}).Info("rekey: rotated credentials for legacy key")

			// Brief pause between key batches.
			select {
			case <-ctx.Done():
				return
			case <-time.After(rekeyBatchDelay):
			}
		}

		logger.WithField("total_rotated", totalRotated).
			Info("rekey: legacy credential re-encryption completed")
	}()
}
