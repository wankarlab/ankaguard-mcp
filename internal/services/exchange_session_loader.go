package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/credentials"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// sessionCredentialLoader adapts credentials.Service to the
// gct.CredentialLoader interface. It bridges the credentials package (which
// deals in *credentials.DecryptedCredential) and the gct package (which deals
// in gct.DecryptedExchangeCredential) without creating an import cycle.
type sessionCredentialLoader struct {
	svc credentials.Service
}

// NewSessionCredentialLoader wraps a credentials.Service so it satisfies the
// gct.CredentialLoader interface required by gct.SessionManager.
func NewSessionCredentialLoader(svc credentials.Service) gct.CredentialLoader {
	if svc == nil {
		panic("NewSessionCredentialLoader: svc must not be nil")
	}
	return &sessionCredentialLoader{svc: svc}
}

// GetCredential implements gct.CredentialLoader. It fetches decrypted
// credentials via the credentials.Service and translates them into the
// gct-local DecryptedExchangeCredential type.
func (l *sessionCredentialLoader) GetCredential(ctx context.Context, userID, exchange string) (gct.DecryptedExchangeCredential, error) {
	if userID == "" {
		return gct.DecryptedExchangeCredential{}, fmt.Errorf("GetCredential: userID is required")
	}
	if exchange == "" {
		return gct.DecryptedExchangeCredential{}, fmt.Errorf("GetCredential: exchange is required")
	}

	dc, err := l.svc.GetCredential(ctx, userID, exchange)
	if err != nil {
		return gct.DecryptedExchangeCredential{}, fmt.Errorf("GetCredential (user=%s exchange=%s): %w", userID, exchange, err)
	}
	if dc == nil {
		return gct.DecryptedExchangeCredential{}, fmt.Errorf("GetCredential (user=%s exchange=%s): nil credential returned", userID, exchange)
	}

	return gct.DecryptedExchangeCredential{
		APIKey:     dc.APIKey,
		APISecret:  dc.APISecret,
		Passphrase: dc.Passphrase,
	}, nil
}

// ErrCredentialServiceUnavailable is returned by the stub loader when no
// credential service has been configured yet.
var ErrCredentialServiceUnavailable = errors.New("credential service not available; configure credentials storage first")

// unavailableCredentialLoader is used as a placeholder when the credential
// service is not yet wired (e.g. on first boot without a credential DB).
// BotManager.StartBot will return this error when a user tries to start a bot.
type unavailableCredentialLoader struct{}

// NewUnavailableCredentialLoader returns a gct.CredentialLoader stub that
// always returns ErrCredentialServiceUnavailable. Use it to construct a
// BotManager before the real credential service is initialised.
func NewUnavailableCredentialLoader() gct.CredentialLoader {
	return &unavailableCredentialLoader{}
}

func (u *unavailableCredentialLoader) GetCredential(_ context.Context, _, _ string) (gct.DecryptedExchangeCredential, error) {
	return gct.DecryptedExchangeCredential{}, ErrCredentialServiceUnavailable
}
