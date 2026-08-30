package oauth

import (
	"context"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Provider defines the interface for OAuth providers
type Provider interface {
	// GetName returns the display name of the provider (e.g., "GitHub", "Discord")
	GetName() string

	// IsEnabled returns whether this OAuth provider is enabled
	IsEnabled() bool

	// ExchangeToken exchanges the authorization code for an access token
	// The gin.Context is passed for providers that need request info (e.g., for redirect_uri)
	ExchangeToken(ctx context.Context, code string, c *gin.Context) (*OAuthToken, error)

	// GetUserInfo retrieves user information using the access token
	GetUserInfo(ctx context.Context, token *OAuthToken) (*OAuthUser, error)

	// IsUserIDTaken checks if the provider user ID is already associated with an account
	IsUserIDTaken(providerUserID string) bool

	// FillUserByProviderID fills the user model by provider user ID
	FillUserByProviderID(user *model.User, providerUserID string) error

	// SetProviderUserID sets the provider user ID on the user model
	SetProviderUserID(user *model.User, providerUserID string)

	// GetProviderPrefix returns the prefix for auto-generated usernames (e.g., "github_")
	GetProviderPrefix() string

	// ProviderUserIDColumn returns the users-table column that stores this provider's
	// user ID, used by bind flows to update only the binding column instead of
	// writing back a full user snapshot. Providers that persist bindings elsewhere
	// (e.g. the user_oauth_bindings table) return an empty string.
	ProviderUserIDColumn() string
}

// RegistrationBypassProvider is an optional interface implemented by providers
// that may provision a new account even when global registration is disabled
// (common.RegisterEnabled == false). Providers that do not implement it fall
// back to the global registration gate.
type RegistrationBypassProvider interface {
	// AllowRegistration reports whether this provider may create new users
	// independently of the global registration setting.
	AllowRegistration() bool
}
