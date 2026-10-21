package rancher2

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-mux/tf5to6server"
	"github.com/stretchr/testify/require"
)

func TestSDKOwnerProtocol6(t *testing.T) {
	ctx := t.Context()
	provider := Provider()
	legacy := provider.GRPCProvider()
	server, err := tf5to6server.UpgradeServer(ctx, func() tfprotov5.ProviderServer { return legacy })
	require.NoError(t, err)
	before, err := legacy.GetProviderSchema(ctx, &tfprotov5.GetProviderSchemaRequest{})
	require.NoError(t, err)
	after, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	for _, diagnostic := range after.Diagnostics {
		require.NotEqual(t, tfprotov6.DiagnosticSeverityError, diagnostic.Severity, "%s", diagnostic.Summary)
	}
	require.NotEmpty(t, before.ResourceSchemas)
	require.Len(t, after.ResourceSchemas, len(before.ResourceSchemas))
	require.Len(t, after.DataSourceSchemas, len(before.DataSourceSchemas))
	for name, original := range before.ResourceSchemas {
		converted := after.ResourceSchemas[name]
		require.NotNil(t, converted, name)
		require.Equal(t, original.Version, converted.Version, name)
		require.True(t, original.ValueType().Equal(converted.ValueType()), name)
	}
	for name, original := range before.DataSourceSchemas {
		converted := after.DataSourceSchemas[name]
		require.NotNil(t, converted, name)
		require.True(t, original.ValueType().Equal(converted.ValueType()), name)
	}
	protected := false
	for _, attribute := range after.Provider.Block.Attributes {
		if attribute.Name == "token_key" {
			require.True(t, attribute.Sensitive)
			protected = true
		}
	}
	require.True(t, protected, "provider token must remain sensitive")

	const kind = "rancher2_setting"
	ty := after.ResourceSchemas[kind].ValueType()
	state, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
		TypeName: kind, Version: 0,
		RawState: &tfprotov6.RawState{JSON: []byte(`{"id":"pilot","name":"pilot","value":"stable"}`)},
	})
	require.NoError(t, err)
	require.Empty(t, state.Diagnostics)
	require.NotNil(t, state.UpgradedState)
	value, err := state.UpgradedState.Unmarshal(ty)
	require.NoError(t, err)
	var attributes map[string]tftypes.Value
	require.NoError(t, value.As(&attributes))
	var id string
	require.NoError(t, attributes["id"].As(&id))
	require.Equal(t, "pilot", id)

	object, ok := ty.(tftypes.Object)
	require.True(t, ok)
	config := make(map[string]tftypes.Value, len(object.AttributeTypes))
	for name, attributeType := range object.AttributeTypes {
		config[name] = tftypes.NewValue(attributeType, nil)
	}
	config["name"] = tftypes.NewValue(tftypes.String, "pilot")
	config["value"] = tftypes.NewValue(tftypes.String, "stable")
	configuration, err := tfprotov6.NewDynamicValue(ty, tftypes.NewValue(ty, config))
	require.NoError(t, err)
	prior, err := tfprotov6.NewDynamicValue(ty, tftypes.NewValue(ty, nil))
	require.NoError(t, err)
	plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: kind, PriorState: &prior, Config: &configuration, ProposedNewState: &configuration,
	})
	require.NoError(t, err)
	require.Empty(t, plan.Diagnostics)
	require.NotNil(t, plan.PlannedState)
	value, err = plan.PlannedState.Unmarshal(ty)
	require.NoError(t, err)
	require.NoError(t, value.As(&attributes))
	var planned string
	require.NoError(t, attributes["value"].As(&planned))
	require.Equal(t, "stable", planned)
	t.Logf("protocol6: %d resources, %d data sources, preserved state ID and configured plan value", len(after.ResourceSchemas), len(after.DataSourceSchemas))
}
