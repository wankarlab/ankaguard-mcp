package auth

import "context"

// AuthLevel represents different authentication levels in the system
type AuthLevel int

const (
	// BASIC represents basic authentication level (Level 1)
	BASIC AuthLevel = iota + 1
	// VERIFIED represents verified authentication level (Level 2)
	VERIFIED
	// PREMIUM represents premium authentication level (Level 3)
	PREMIUM
	// ENTERPRISE represents enterprise authentication level (Level 4)
	ENTERPRISE
)

// HasMinimumAuthLevel checks if the context has the minimum required auth level
// This is a placeholder function that would normally check JWT claims
func HasMinimumAuthLevel(ctx context.Context, required AuthLevel) bool {
	// In a real implementation, this would extract the auth level from JWT claims
	// For now, we return true as this is handled by Kong Gateway
	return true
}

// GetLevelFromContext extracts the auth level from context
// This is a placeholder function that would normally extract from JWT claims
func GetLevelFromContext(ctx context.Context) (AuthLevel, bool) {
	// In a real implementation, this would extract the auth level from JWT claims
	// For now, we return BASIC as this is handled by Kong Gateway
	return BASIC, true
}
