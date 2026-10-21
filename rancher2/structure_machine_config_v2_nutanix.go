package rancher2

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	norman "github.com/rancher/norman/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	machineConfigV2NutanixKind       = "NutanixConfig"
	machineConfigV2NutanixAPIVersion = "rke-machine-config.cattle.io/v1"
	machineConfigV2NutanixAPIType    = "rke-machine-config.cattle.io.nutanixconfig"
)

type machineConfigV2Nutanix struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Endpoint          string   `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Username          string   `json:"username,omitempty" yaml:"username,omitempty"`
	Password          string   `json:"password,omitempty" yaml:"password,omitempty"`
	Port              string   `json:"port,omitempty" yaml:"port,omitempty"`
	Insecure          bool     `json:"insecure,omitempty" yaml:"insecure,omitempty"`
	Cluster           string   `json:"cluster,omitempty" yaml:"cluster,omitempty"`
	VMMem             string   `json:"vmMem,omitempty" yaml:"vmMem,omitempty"`
	VMCPUs            string   `json:"vmCpus,omitempty" yaml:"vmCpus,omitempty"`
	VMCores           string   `json:"vmCores,omitempty" yaml:"vmCores,omitempty"`
	VMCPUPassthrough  bool     `json:"vmCpuPassthrough,omitempty" yaml:"vmCpuPassthrough,omitempty"`
	VMNetwork         []string `json:"vmNetwork,omitempty" yaml:"vmNetwork,omitempty"`
	VMImage           string   `json:"vmImage,omitempty" yaml:"vmImage,omitempty"`
	VMImageSize       string   `json:"vmImageSize,omitempty" yaml:"vmImageSize,omitempty"`
	VMCategories      []string `json:"vmCategories,omitempty" yaml:"vmCategories,omitempty"`
	StorageContainer  string   `json:"storageContainer,omitempty" yaml:"storageContainer,omitempty"`
	DiskSize          string   `json:"diskSize,omitempty" yaml:"diskSize,omitempty"`
	CloudInit         string   `json:"cloudInit,omitempty" yaml:"cloudInit,omitempty"`
	VMSerialPort      bool     `json:"vmSerialPort,omitempty" yaml:"vmSerialPort,omitempty"`
	Project           string   `json:"project,omitempty" yaml:"project,omitempty"`
	BootType          string   `json:"bootType,omitempty" yaml:"bootType,omitempty"`
	Timeout           string   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	VMGPU             []string `json:"vmGpu,omitempty" yaml:"vmGpu,omitempty"`

	VMNicQueues         string `json:"vmNicQueues,omitempty" yaml:"vmNicQueues,omitempty"`
	VMManagementNetwork string `json:"vmManagementNetwork,omitempty" yaml:"vmManagementNetwork,omitempty"`
}

type MachineConfigV2Nutanix struct {
	norman.Resource
	machineConfigV2Nutanix
}

func flattenMachineConfigV2Nutanix(in *MachineConfigV2Nutanix) ([]any, error) {
	if in == nil {
		return nil, nil
	}
	queues, err := parseNutanixNICQueues(in.VMNicQueues)
	if err != nil {
		return nil, err
	}

	obj := make(map[string]any)

	if len(in.Endpoint) > 0 {
		obj["endpoint"] = in.Endpoint
	}
	if len(in.Username) > 0 {
		obj["username"] = in.Username
	}
	if len(in.Password) > 0 {
		obj["password"] = in.Password
	}
	if len(in.Port) > 0 {
		obj["port"] = in.Port
	}
	obj["insecure"] = in.Insecure
	if len(in.Cluster) > 0 {
		obj["cluster"] = in.Cluster
	}
	if len(in.VMMem) > 0 {
		obj["vm_mem"] = in.VMMem
	}
	if len(in.VMCPUs) > 0 {
		obj["vm_cpus"] = in.VMCPUs
	}
	if len(in.VMCores) > 0 {
		obj["vm_cores"] = in.VMCores
	}
	obj["vm_cpu_passthrough"] = in.VMCPUPassthrough
	if len(in.VMNetwork) > 0 {
		obj["vm_network"] = toArrayInterface(in.VMNetwork)
	}
	if queues > 0 {
		obj["vm_nic_queues"] = queues
	}
	if in.VMManagementNetwork != "" {
		obj["vm_management_network"] = in.VMManagementNetwork
	}
	if len(in.VMImage) > 0 {
		obj["vm_image"] = in.VMImage
	}
	if len(in.VMImageSize) > 0 {
		obj["vm_image_size"] = in.VMImageSize
	}
	if len(in.VMCategories) > 0 {
		obj["vm_categories"] = toArrayInterface(in.VMCategories)
	}
	if len(in.StorageContainer) > 0 {
		obj["storage_container"] = in.StorageContainer
	}
	if len(in.DiskSize) > 0 {
		obj["disk_size"] = in.DiskSize
	}
	if len(in.CloudInit) > 0 {
		obj["cloud_init"] = in.CloudInit
	}
	obj["vm_serial_port"] = in.VMSerialPort
	if len(in.Project) > 0 {
		obj["project"] = in.Project
	}
	if len(in.BootType) > 0 {
		obj["boot_type"] = in.BootType
	}
	if len(in.Timeout) > 0 {
		obj["timeout"] = in.Timeout
	}
	if len(in.VMGPU) > 0 {
		obj["vm_gpu"] = toArrayInterface(in.VMGPU)
	}

	return []any{obj}, nil
}

func expandMachineConfigV2Nutanix(p []any, source *MachineConfigV2) *MachineConfigV2Nutanix {
	if p == nil || len(p) == 0 || p[0] == nil {
		return nil
	}

	obj := &MachineConfigV2Nutanix{}
	if len(source.ID) > 0 {
		obj.ID = source.ID
	}

	in := p[0].(map[string]any)

	obj.TypeMeta.Kind = machineConfigV2NutanixKind
	obj.TypeMeta.APIVersion = machineConfigV2NutanixAPIVersion
	source.TypeMeta = obj.TypeMeta
	obj.ObjectMeta = source.ObjectMeta

	if v, ok := in["endpoint"].(string); ok && len(v) > 0 {
		obj.Endpoint = v
	}
	if v, ok := in["username"].(string); ok && len(v) > 0 {
		obj.Username = v
	}
	if v, ok := in["password"].(string); ok && len(v) > 0 {
		obj.Password = v
	}
	if v, ok := in["port"].(string); ok && len(v) > 0 {
		obj.Port = v
	}
	if v, ok := in["insecure"].(bool); ok {
		obj.Insecure = v
	}
	if v, ok := in["cluster"].(string); ok && len(v) > 0 {
		obj.Cluster = v
	}
	if v, ok := in["vm_mem"].(string); ok && len(v) > 0 {
		obj.VMMem = v
	}
	if v, ok := in["vm_cpus"].(string); ok && len(v) > 0 {
		obj.VMCPUs = v
	}
	if v, ok := in["vm_cores"].(string); ok && len(v) > 0 {
		obj.VMCores = v
	}
	if v, ok := in["vm_cpu_passthrough"].(bool); ok {
		obj.VMCPUPassthrough = v
	}
	if v, ok := in["vm_network"].([]any); ok && len(v) > 0 {
		obj.VMNetwork = toArrayString(v)
	}
	if v, ok := in["vm_nic_queues"].(int); ok && v > 0 {
		obj.VMNicQueues = strconv.Itoa(v)
	}
	if v, ok := in["vm_management_network"].(string); ok {
		obj.VMManagementNetwork = v
	}
	if v, ok := in["vm_image"].(string); ok && len(v) > 0 {
		obj.VMImage = v
	}
	if v, ok := in["vm_image_size"].(string); ok && len(v) > 0 {
		obj.VMImageSize = v
	}
	if v, ok := in["vm_categories"].([]any); ok && len(v) > 0 {
		obj.VMCategories = toArrayString(v)
	}
	if v, ok := in["storage_container"].(string); ok && len(v) > 0 {
		obj.StorageContainer = v
	}
	if v, ok := in["disk_size"].(string); ok && len(v) > 0 {
		obj.DiskSize = v
	}
	if v, ok := in["cloud_init"].(string); ok && len(v) > 0 {
		obj.CloudInit = v
	}
	if v, ok := in["vm_serial_port"].(bool); ok {
		obj.VMSerialPort = v
	}
	if v, ok := in["project"].(string); ok && len(v) > 0 {
		obj.Project = v
	}
	if v, ok := in["boot_type"].(string); ok && len(v) > 0 {
		obj.BootType = v
	}
	if v, ok := in["timeout"].(string); ok && len(v) > 0 {
		obj.Timeout = v
	}
	if v, ok := in["vm_gpu"].([]any); ok && len(v) > 0 {
		obj.VMGPU = toArrayString(v)
	}

	return obj
}

func parseNutanixNICQueues(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	queues, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid Nutanix vm_nic_queues: %w", err)
	}
	if queues < 1 {
		return 0, fmt.Errorf("Nutanix vm_nic_queues must be positive")
	}
	return queues, nil
}

func validateNutanixMachineHardware(in *MachineConfigV2Nutanix) error {
	if in == nil {
		return nil
	}
	queues, err := parseNutanixNICQueues(in.VMNicQueues)
	if err != nil {
		return err
	}
	if queues > 0 {
		cpus, err := strconv.ParseInt(in.VMCPUs, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid Nutanix vm_cpus for queues: %w", err)
		}
		cores, err := strconv.ParseInt(in.VMCores, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid Nutanix vm_cores for queues: %w", err)
		}
		if cpus < 1 || cores < 1 || cpus > math.MaxInt64/cores || int64(queues) > cpus*cores {
			return fmt.Errorf("Nutanix vm_nic_queues requires positive, nonoverflowing vm_cpus * vm_cores with capacity for queues")
		}
	}
	if in.VMManagementNetwork == "" {
		return nil
	}
	if _, errs := validation.IsUUID(in.VMManagementNetwork, "vm_management_network"); len(errs) > 0 {
		return fmt.Errorf("Nutanix vm_management_network must be a subnet UUID")
	}
	allUUIDs := true
	for _, network := range in.VMNetwork {
		if strings.EqualFold(network, in.VMManagementNetwork) {
			return nil
		}
		if _, errs := validation.IsUUID(network, "vm_network"); len(errs) > 0 {
			allUUIDs = false
		}
	}
	if allUUIDs {
		return fmt.Errorf("Nutanix vm_management_network must be among vm_network UUIDs")
	}
	// Named attachments are resolved and checked by the driver before VM allocation.
	return nil
}

func requireNutanixMachineHardware(in *MachineConfigV2Nutanix, installed norman.Schema, update bool) error {
	if in == nil || (in.VMNicQueues == "" && in.VMManagementNetwork == "") {
		return nil
	}
	for _, field := range []struct{ name, value string }{
		{"vmNicQueues", in.VMNicQueues},
		{"vmManagementNetwork", in.VMManagementNetwork},
	} {
		if field.value == "" {
			continue
		}
		definition, ok := installed.ResourceFields[field.name]
		writable := definition.Create
		if update {
			writable = definition.Update
		}
		if !ok || definition.Type != "string" || !writable {
			return fmt.Errorf("Nutanix installed schema lacks writable string field %s", field.name)
		}
	}
	return nil
}

func checkNutanixMachineHardwareEcho(want, got *MachineConfigV2Nutanix) error {
	if want == nil || (want.VMNicQueues == "" && want.VMManagementNetwork == "") {
		return nil
	}
	if got == nil ||
		(want.VMNicQueues != "" && want.VMNicQueues != got.VMNicQueues) ||
		(want.VMManagementNetwork != "" && !strings.EqualFold(want.VMManagementNetwork, got.VMManagementNetwork)) {
		return fmt.Errorf("Nutanix response did not retain requested hardware fields; preserve MachineConfig identity and verify installed schema")
	}
	return nil
}
