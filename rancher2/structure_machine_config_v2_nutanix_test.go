package rancher2

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	"github.com/rancher/norman/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNutanixMachineHardwarePlan(t *testing.T) {
	ctx := t.Context()
	rpc := schema.NewGRPCProviderServer(&schema.Provider{ResourcesMap: map[string]*schema.Resource{
		"rancher2_machine_config_v2": resourceRancher2MachineConfigV2(),
	}})
	s, err := rpc.GetProviderSchema(ctx, &tfprotov5.GetProviderSchemaRequest{})
	require.NoError(t, err)
	ty := s.ResourceSchemas["rancher2_machine_config_v2"].ValueType()
	root, ok := ty.(tftypes.Object)
	require.True(t, ok)
	list, ok := root.AttributeTypes["nutanix_config"].(tftypes.List)
	require.True(t, ok)
	block, ok := list.ElementType.(tftypes.Object)
	require.True(t, ok)
	for _, name := range []string{"unknown_cpu", "unknown_network", "known_excess"} {
		t.Run(name, func(t *testing.T) {
			attrs := make(map[string]tftypes.Value, len(root.AttributeTypes))
			for key, typ := range root.AttributeTypes {
				attrs[key] = tftypes.NewValue(typ, nil)
			}
			fields := make(map[string]tftypes.Value, len(block.AttributeTypes))
			for key, typ := range block.AttributeTypes {
				fields[key] = tftypes.NewValue(typ, nil)
			}
			for key, value := range map[string]string{
				"cluster": "cluster", "vm_image": "image", "vm_cpus": "1", "vm_cores": "1",
				"vm_management_network": "11111111-2222-3333-4444-555555555555",
			} {
				fields[key] = tftypes.NewValue(tftypes.String, value)
			}
			fields["vm_nic_queues"] = tftypes.NewValue(tftypes.Number, 2)
			network := tftypes.NewValue(tftypes.String, "11111111-2222-3333-4444-555555555555")
			if name == "unknown_cpu" {
				fields["vm_cpus"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
			}
			if name == "unknown_network" {
				fields["vm_cpus"] = tftypes.NewValue(tftypes.String, "2")
				network = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
			}
			fields["vm_network"] = tftypes.NewValue(block.AttributeTypes["vm_network"], []tftypes.Value{network})
			attrs["generate_name"] = tftypes.NewValue(tftypes.String, "hardware")
			attrs["nutanix_config"] = tftypes.NewValue(list, []tftypes.Value{tftypes.NewValue(block, fields)})
			config, err := tfprotov5.NewDynamicValue(ty, tftypes.NewValue(ty, attrs))
			require.NoError(t, err)
			prior, err := tfprotov5.NewDynamicValue(ty, tftypes.NewValue(ty, nil))
			require.NoError(t, err)
			resp, err := rpc.PlanResourceChange(ctx, &tfprotov5.PlanResourceChangeRequest{
				TypeName: "rancher2_machine_config_v2", PriorState: &prior, Config: &config, ProposedNewState: &config,
			})
			require.NoError(t, err)
			var errors []string
			for _, diagnostic := range resp.Diagnostics {
				if diagnostic.Severity == tfprotov5.DiagnosticSeverityError {
					errors = append(errors, diagnostic.Summary)
				}
			}
			if name == "known_excess" {
				require.Contains(t, strings.Join(errors, " "), "vm_cpus * vm_cores", "known invalid hardware must still fail capacity validation")
			} else {
				require.Empty(t, errors, "unknown leaves must defer validation")
				require.NotNil(t, resp.PlannedState)
			}
		})
	}
}

func TestNutanixMachineHardwareWire(t *testing.T) {
	legacy, err := json.Marshal(testMachineConfigV2NutanixConf)
	require.NoError(t, err)
	for _, queues := range []int{0, 1} {
		t.Run(fmt.Sprint(queues), func(t *testing.T) {
			original, ok := testMachineConfigV2NutanixInterface[0].(map[string]any)
			require.True(t, ok)
			input := maps.Clone(original)
			if queues > 0 {
				input["vm_nic_queues"] = queues
				input["vm_management_network"] = "11111111-2222-3333-4444-555555555555"
			}
			source := &MachineConfigV2{machineConfigV2: machineConfigV2{ObjectMeta: testMachineConfigV2NutanixConf.ObjectMeta}}
			expanded := expandMachineConfigV2Nutanix([]any{input}, source)
			wire, err := json.Marshal(expanded)
			require.NoError(t, err)
			if queues == 0 {
				require.Equal(t, legacy, wire, "omission must retain legacy fixture bytes and hash")
			} else {
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(wire, &fields))
				require.Equal(t, json.RawMessage(`"1"`), fields["vmNicQueues"])
				require.Equal(t, json.RawMessage(`"11111111-2222-3333-4444-555555555555"`), fields["vmManagementNetwork"])
			}
			var decoded MachineConfigV2Nutanix
			require.NoError(t, json.Unmarshal(wire, &decoded))
			flat, err := flattenMachineConfigV2Nutanix(&decoded)
			require.NoError(t, err)
			require.Equal(t, []any{input}, flat)
		})
	}
}

func TestNutanixMachineHardwareValidation(t *testing.T) {
	for _, tc := range []struct {
		name, queues, cpus, cores, management string
		networks                              []string
	}{
		{name: "zero", queues: "0", cpus: "2", cores: "1"},
		{name: "capacity", queues: "3", cpus: "2", cores: "1"},
		{name: "overflow", queues: "2", cpus: "9223372036854775807", cores: "2"},
		{name: "selector", management: "11111111-2222-3333-4444-555555555555", networks: []string{"22222222-2222-3333-4444-555555555555"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &MachineConfigV2Nutanix{machineConfigV2Nutanix: machineConfigV2Nutanix{
				VMNicQueues: tc.queues, VMCPUs: tc.cpus, VMCores: tc.cores,
				VMManagementNetwork: tc.management, VMNetwork: tc.networks,
			}}
			require.Error(t, validateNutanixMachineHardware(input))
		})
	}
	warnings, errs := machineConfigV2NutanixFields()["vm_nic_queues"].ValidateFunc(0, "vm_nic_queues")
	require.Empty(t, warnings)
	require.NotEmpty(t, errs, "explicit zero is invalid, separately from omission")
}

func TestNutanixMachineHardwareBoundary(t *testing.T) {
	const id = "fleet-default/nc-hardware"
	for _, tc := range []struct {
		name                              string
		update, missing, prune, malformed bool
	}{
		{name: "create"},
		{name: "update", update: true},
		{name: "missing_create", missing: true},
		{name: "missing_update", missing: true, update: true},
		{name: "pruned_create", prune: true},
		{name: "pruned_update", prune: true, update: true},
		{name: "malformed_read", malformed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := schema.TestResourceDataRaw(t, machineConfigV2Fields(), map[string]any{
				"generate_name": "hardware", "kind": machineConfigV2NutanixKind,
				"nutanix_config": []any{map[string]any{"cluster": "fixture", "vm_image": "fixture", "vm_network": []any{"fixture"}, "vm_nic_queues": 2}},
			})
			require.NoError(t, d.Set("kind", machineConfigV2NutanixKind))
			if tc.update || tc.malformed {
				d.SetId(id)
			}
			obj := expandMachineConfigV2(d)
			response := *obj.NutanixConfig
			response.ID = id
			response.Links = map[string]string{"self": "https://hardware.invalid/configs/" + id}
			var calls []string
			transport := catalogContractTransport(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.Method)
				out := response
				if r.Method != http.MethodGet {
					var received MachineConfigV2Nutanix
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						return nil, fmt.Errorf("decode hardware fixture: %w", err)
					}
					if received.VMNicQueues != "2" {
						return nil, fmt.Errorf("queue field was not forwarded as a string")
					}
					if tc.prune {
						out.VMNicQueues = ""
					}
				}
				if tc.malformed {
					out.VMNicQueues = "invalid"
				}
				body, err := json.Marshal(out)
				if err != nil {
					return nil, fmt.Errorf("encode hardware fixture: %w", err)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
			})
			fields := map[string]types.Field{}
			if !tc.missing {
				fields["vmNicQueues"] = types.Field{Type: "string", Create: !tc.update, Update: tc.update}
			}
			ops := &clientbase.APIOperations{Opts: &clientbase.ClientOpts{}, Client: &http.Client{Transport: transport, Timeout: time.Second}, Types: map[string]types.Schema{
				machineConfigV2NutanixAPIType: {ID: machineConfigV2NutanixAPIType, ResourceMethods: []string{http.MethodGet, http.MethodPut}, CollectionMethods: []string{http.MethodPost}, ResourceFields: fields, Links: map[string]string{"collection": "https://hardware.invalid/configs"}},
			}}
			client := &clientbase.APIBaseClient{Ops: ops, Types: ops.Types, Opts: ops.Opts}
			config := &Config{Timeout: time.Second, Client: Client{CatalogV2: map[string]*clientbase.APIBaseClient{"local": client}}}
			config.contextReady = map[string]*contextClientState{"local": {client: client}}
			var err error
			switch {
			case tc.malformed:
				err = resourceRancher2MachineConfigV2Read(d, config)
			case tc.prune || tc.missing:
				if tc.update {
					err = resourceRancher2MachineConfigV2Update(d, config)
				} else {
					err = resourceRancher2MachineConfigV2Create(d, config)
				}
			case tc.update:
				_, err = updateMachineConfigV2(config, obj)
			default:
				_, err = createMachineConfigV2(config, obj)
			}
			if tc.missing {
				require.Error(t, err)
				require.Empty(t, calls, "unsupported schema must reject before reads or mutations")
				return
			}
			want := []string{http.MethodPost}
			if tc.update {
				want = []string{http.MethodGet, http.MethodPut}
			}
			if tc.malformed {
				want = []string{http.MethodGet}
			}
			require.Equal(t, want, calls, "no replay or post-pruning wait")
			if tc.prune || tc.malformed {
				require.Error(t, err)
				require.Equal(t, id, d.Id())
				require.NotNil(t, d.State())
				require.Equal(t, id, d.State().ID)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

var (
	testMachineConfigV2NutanixConf = &MachineConfigV2Nutanix{
		machineConfigV2Nutanix: machineConfigV2Nutanix{
			TypeMeta: metav1.TypeMeta{
				Kind:       machineConfigV2NutanixKind,
				APIVersion: machineConfigV2NutanixAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nc-test",
				Namespace: "fleet-default",
			},
			Endpoint:         "pc.example.com",
			Username:         "X-ntnx-api-key",
			Password:         "secret",
			Port:             "9440",
			Insecure:         true,
			Cluster:          "cluster-a",
			VMMem:            "4096",
			VMCPUs:           "4",
			VMCores:          "2",
			VMCPUPassthrough: true,
			VMNetwork:        []string{"subnet-a", "subnet-b"},
			VMImage:          "ubuntu-cloudinit",
			VMImageSize:      "80",
			VMCategories:     []string{"app:tf", "env:dev"},
			StorageContainer: "container-uuid",
			DiskSize:         "40",
			CloudInit:        "#cloud-config",
			VMSerialPort:     true,
			Project:          "project-a",
			BootType:         "uefi",
			Timeout:          "600",
			VMGPU:            []string{"gpu-a"},
		},
	}
	testMachineConfigV2NutanixInterface = []any{
		map[string]any{
			"endpoint":           "pc.example.com",
			"username":           "X-ntnx-api-key",
			"password":           "secret",
			"port":               "9440",
			"insecure":           true,
			"cluster":            "cluster-a",
			"vm_mem":             "4096",
			"vm_cpus":            "4",
			"vm_cores":           "2",
			"vm_cpu_passthrough": true,
			"vm_network":         []any{"subnet-a", "subnet-b"},
			"vm_image":           "ubuntu-cloudinit",
			"vm_image_size":      "80",
			"vm_categories":      []any{"app:tf", "env:dev"},
			"storage_container":  "container-uuid",
			"disk_size":          "40",
			"cloud_init":         "#cloud-config",
			"vm_serial_port":     true,
			"project":            "project-a",
			"boot_type":          "uefi",
			"timeout":            "600",
			"vm_gpu":             []any{"gpu-a"},
		},
	}
)

func TestFlattenMachineConfigV2Nutanix(t *testing.T) {
	cases := []struct {
		Input          *MachineConfigV2Nutanix
		ExpectedOutput []any
	}{
		{
			Input:          testMachineConfigV2NutanixConf,
			ExpectedOutput: testMachineConfigV2NutanixInterface,
		},
	}

	for _, tc := range cases {
		output, err := flattenMachineConfigV2Nutanix(tc.Input)
		assert.NoError(t, err)
		assert.Equal(t, tc.ExpectedOutput, output, "Unexpected output from flattener.")
	}
}

func TestExpandMachineConfigV2Nutanix(t *testing.T) {
	cases := []struct {
		Input          []any
		Source         *MachineConfigV2
		ExpectedOutput *MachineConfigV2Nutanix
	}{
		{
			Input: testMachineConfigV2NutanixInterface,
			Source: &MachineConfigV2{
				machineConfigV2: machineConfigV2{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nc-test",
						Namespace: "fleet-default",
					},
				},
			},
			ExpectedOutput: testMachineConfigV2NutanixConf,
		},
	}

	for _, tc := range cases {
		output := expandMachineConfigV2Nutanix(tc.Input, tc.Source)
		assert.Equal(t, tc.ExpectedOutput.machineConfigV2Nutanix, output.machineConfigV2Nutanix, "Unexpected output from expander.")
	}
}
