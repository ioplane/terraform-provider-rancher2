package rancher2

import (
	"fmt"
	"testing"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/stretchr/testify/assert"
)

func TestAuthConfigGenericOIDCResourceValidateNoData(t *testing.T) {
	r := resourceRancher2AuthConfigGenericOIDC()
	d := terraform.NewResourceConfigRaw(map[string]any{})
	expected := make(diag.Diagnostics, 0, 4)
	for _, field := range []string{"issuer", "client_id", "client_secret", "rancher_url"} {
		expected = append(expected, diag.Diagnostic{
			Severity:      diag.Error,
			Summary:       "Missing required argument",
			Detail:        fmt.Sprintf("The argument %q is required, but no definition was found.", field),
			AttributePath: cty.GetAttrPath(field),
		})
	}
	assert.ElementsMatch(t, expected, r.Validate(d))
}

func TestAuthConfigGenericOIDCResourceEndSessionEndpointValidation(t *testing.T) {
	r := resourceRancher2AuthConfigGenericOIDC()
	d := terraform.NewResourceConfigRaw(map[string]any{
		"client_id":            "client-id",
		"client_secret":        "client-secret",
		"issuer":               "https://issuer.example.com",
		"rancher_url":          "https://rancher.example.com/verify-auth",
		"end_session_endpoint": "test",
	})
	assert.Equal(t, diag.Diagnostics{{
		Severity:      diag.Error,
		Summary:       `expected "end_session_endpoint" to have a host, got test`,
		AttributePath: cty.GetAttrPath("end_session_endpoint"),
	}}, r.Validate(d))
}

func TestAuthConfigGenericOIDCResourcePKCEMethodValidation(t *testing.T) {
	r := resourceRancher2AuthConfigGenericOIDC()
	d := terraform.NewResourceConfigRaw(map[string]any{
		"client_id":     "client-id",
		"client_secret": "client-secret",
		"issuer":        "https://issuer.example.com",
		"rancher_url":   "https://rancher.example.com/verify-auth",
		"pkce_method":   "invalid",
	})
	assert.Equal(t, diag.Diagnostics{{
		Severity:      diag.Error,
		Summary:       `"pkce_method": only supported value is "S256", got "invalid"`,
		AttributePath: cty.GetAttrPath("pkce_method"),
	}}, r.Validate(d))
}

func TestAuthConfigGenericOIDCResourcePKCEMethodEmpty(t *testing.T) {
	r := resourceRancher2AuthConfigGenericOIDC()
	d := terraform.NewResourceConfigRaw(map[string]any{
		"client_id":     "client-id",
		"client_secret": "client-secret",
		"issuer":        "https://issuer.example.com",
		"rancher_url":   "https://rancher.example.com/verify-auth",
		"pkce_method":   "",
	})
	assert.Empty(t, r.Validate(d))
}
