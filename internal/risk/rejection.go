package risk

import (
	"errors"
	"regexp"
	"strings"
)

// CodeRiskRejected is the generic code of a sentinel rejection that carries no
// specific code (kill switch, exposure, leverage, ...).
const CodeRiskRejected = "RISK_REJECTED"

// rejectionPrefix is the message prefix every sentinel rejection keeps, so
// logs and callers that match on text keep working.
const rejectionPrefix = "order rejected by risk sentinel: "

// RejectedError is returned when the RiskSentinel blocks an order. Code is a
// stable machine-readable code (ORDER_CAP_EXCEEDED, ORDER_NOTIONAL_UNKNOWN or
// RISK_REJECTED) and Reason the human explanation.
type RejectedError struct {
	Code   string
	Reason string
}

// NewRejectedError builds the error from a sentinel decision.
func NewRejectedError(d *RiskDecision) *RejectedError {
	code := d.Code
	if code == "" {
		code = CodeRiskRejected
	}
	return &RejectedError{Code: code, Reason: d.Reason}
}

func (e *RejectedError) Error() string { return rejectionPrefix + e.Reason }

var leadingCode = regexp.MustCompile(`^([A-Z][A-Z0-9_]{2,}):`)

// RejectionFrom extracts a sentinel rejection from err: the typed error when
// it survived wrapping, otherwise the text form (a caller that wrapped it with
// %v instead of %w). ok is false when err is not a sentinel rejection.
func RejectionFrom(err error) (*RejectedError, bool) {
	if err == nil {
		return nil, false
	}
	var re *RejectedError
	if errors.As(err, &re) {
		return re, true
	}
	msg := err.Error()
	i := strings.Index(msg, rejectionPrefix)
	if i < 0 {
		return nil, false
	}
	reason := msg[i+len(rejectionPrefix):]
	code := CodeRiskRejected
	if m := leadingCode.FindStringSubmatch(reason); m != nil {
		code = m[1]
	}
	return &RejectedError{Code: code, Reason: reason}, true
}
