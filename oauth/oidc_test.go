package oauth

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
)

func TestOIDCProvider_GetName(t *testing.T) {
	settings := system_setting.GetOIDCSettings()
	originalDisplayName := settings.DisplayName
	defer func() { settings.DisplayName = originalDisplayName }()

	p := &OIDCProvider{}

	settings.DisplayName = ""
	assert.Equal(t, "OIDC", p.GetName())

	settings.DisplayName = "  Acme SSO  "
	assert.Equal(t, "Acme SSO", p.GetName())
}

func TestOIDCProvider_AllowRegistration(t *testing.T) {
	settings := system_setting.GetOIDCSettings()
	original := settings.AllowRegistration
	defer func() { settings.AllowRegistration = original }()

	p := &OIDCProvider{}

	settings.AllowRegistration = false
	assert.False(t, p.AllowRegistration())

	settings.AllowRegistration = true
	assert.True(t, p.AllowRegistration())
}
