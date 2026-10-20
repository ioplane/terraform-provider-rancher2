package rancher2

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	norman "github.com/rancher/norman/types"
	"k8s.io/api/core/v1"
)

const (
	secretV2Kind         = "Secret"
	secretV2APIVersion   = "v1"
	secretV2APIType      = "secret"
	secretV2ClusterIDsep = "."
)

//Types

type SecretV2 struct {
	norman.Resource `json:"-"`
	v1.Secret
	K8SType string `json:"_type,omitempty"`
}

type secretV2Alias SecretV2

// secretV2JSON restores Norman fields and discards any valid JSON type value.
type secretV2JSON struct {
	*secretV2Alias
	*norman.Resource
	Type json.RawMessage `json:"type,omitempty"`
}

func (s SecretV2) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(secretV2JSON{
		secretV2Alias: (*secretV2Alias)(&s),
		Resource:      &s.Resource,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Secret V2: %w", err)
	}
	return data, nil
}

func (s *SecretV2) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &secretV2JSON{
		secretV2Alias: (*secretV2Alias)(s),
		Resource:      &s.Resource,
	}); err != nil {
		return fmt.Errorf("unmarshal Secret V2: %w", err)
	}
	return nil
}

func secretV2Fields() map[string]*schema.Schema {
	s := map[string]*schema.Schema{
		"cluster_id": {
			Type:        schema.TypeString,
			Required:    true,
			ForceNew:    true,
			Description: "K8s cluster ID",
		},
		"data": {
			Type:        schema.TypeMap,
			Required:    true,
			Sensitive:   true,
			Description: "Secret data map",
		},
		"name": {
			Type:        schema.TypeString,
			Required:    true,
			Description: "K8s Secret name",
		},
		"namespace": {
			Type:        schema.TypeString,
			Optional:    true,
			ForceNew:    true,
			Default:     "default",
			Description: "K8s Secret namespace",
		},
		"immutable": {
			Type:        schema.TypeBool,
			Optional:    true,
			Default:     false,
			Description: "If set to true, ensures that data stored in the Secret cannot be updated",
		},
		"type": {
			Type:        schema.TypeString,
			Optional:    true,
			Default:     string(v1.SecretTypeOpaque),
			Description: "Used to facilitate programmatic handling of Secret data",
		},
		"resource_version": {
			Type:     schema.TypeString,
			Computed: true,
		},
	}

	for k, v := range commonAnnotationLabelFields() {
		s[k] = v
	}

	return s
}
