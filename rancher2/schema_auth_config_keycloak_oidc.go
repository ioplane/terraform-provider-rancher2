package rancher2

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const AuthConfigKeyCloakOIDCName = "keycloakoidc"

//Schemas

func authConfigKeyCloakOIDCFields() map[string]*schema.Schema {
	return oidcSchemaFields()
}
