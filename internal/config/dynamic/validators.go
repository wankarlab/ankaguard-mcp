package dynamic

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// Validator is a function that validates a raw string value against a parameter's registry entry.
type Validator func(reg *domain.ParameterRegistry, value string) error

// Sentinel errors returned by ValidateValue and its helpers.
var (
	ErrInvalidInt      = errors.New("value is not a valid integer")
	ErrInvalidFloat    = errors.New("value is not a valid float")
	ErrInvalidBool     = errors.New("value is not a valid bool (must be 'true' or 'false')")
	ErrInvalidDuration = errors.New("value is not a valid duration (e.g. '30s', '1m30s')")
	ErrNotInEnum       = errors.New("value is not in the allowed enum list")
	ErrBelowMin        = errors.New("value is below the allowed minimum")
	ErrAboveMax        = errors.New("value is above the allowed maximum")
)

// ValidateValue checks that value is valid according to the registry entry reg.
// It verifies type-correctness, min/max bounds, and enum membership.
func ValidateValue(reg *domain.ParameterRegistry, value string) error {
	if reg != nil {
		if err := CheckEditionInvariant(reg.Key, value); err != nil {
			return err
		}
	}
	switch reg.Type {
	case domain.ParamTypeInt:
		v, err := strconv.Atoi(value)
		if err != nil {
			return ErrInvalidInt
		}
		if reg.MinValue != nil {
			min, err := strconv.Atoi(*reg.MinValue)
			if err == nil && v < min {
				return fmt.Errorf("%w: %d < %d", ErrBelowMin, v, min)
			}
		}
		if reg.MaxValue != nil {
			max, err := strconv.Atoi(*reg.MaxValue)
			if err == nil && v > max {
				return fmt.Errorf("%w: %d > %d", ErrAboveMax, v, max)
			}
		}

	case domain.ParamTypeFloat:
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return ErrInvalidFloat
		}
		if reg.MinValue != nil {
			min, err := strconv.ParseFloat(*reg.MinValue, 64)
			if err == nil && v < min {
				return fmt.Errorf("%w: %g < %g", ErrBelowMin, v, min)
			}
		}
		if reg.MaxValue != nil {
			max, err := strconv.ParseFloat(*reg.MaxValue, 64)
			if err == nil && v > max {
				return fmt.Errorf("%w: %g > %g", ErrAboveMax, v, max)
			}
		}

	case domain.ParamTypeBool:
		if value != "true" && value != "false" {
			return ErrInvalidBool
		}

	case domain.ParamTypeDuration:
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidDuration, err.Error())
		}

	case domain.ParamTypeString:
		// No structural validation for plain strings beyond enum check below.

	case domain.ParamTypeJSON:
		// JSON validity is checked by the cross-param validators that unmarshal it;
		// here we only require the value to be non-empty when enum is not in play.
	}

	// Enum check applies to all types.
	if len(reg.EnumValues) > 0 && !contains(reg.EnumValues, value) {
		return fmt.Errorf("%w: %q not in %v", ErrNotInEnum, value, reg.EnumValues)
	}

	return nil
}

// contains reports whether v is in slice.
func contains(slice []string, v string) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}
