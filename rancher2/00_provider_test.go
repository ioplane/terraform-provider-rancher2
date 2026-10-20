package rancher2

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/stretchr/testify/assert"
)

func TestProvider(t *testing.T) {
	assert.NoError(t, Provider().InternalValidate())
}

func TestProvider_impl(t *testing.T) {
	var _ *schema.Provider = Provider()
}
