// SPDX-License-Identifier: Apache-2.0

// Package types contains the Rancher catalog wire contracts used by this provider.
// The original package name preserves encoding/json type error messages.
// See README.md for upstream provenance.
package types

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ChartInstall struct {
	ChartName   string                `json:"chartName,omitempty"`
	Version     string                `json:"version,omitempty"`
	ReleaseName string                `json:"releaseName,omitempty"`
	Description string                `json:"description,omitempty"`
	Values      v3.MapStringInterface `json:"values,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

// ChartInstallAction represents the input received when installing the charts received in the charts field
type ChartInstallAction struct {
	Timeout                  *metav1.Duration    `json:"timeout,omitempty"`
	Wait                     bool                `json:"wait,omitempty"`
	DisableHooks             bool                `json:"noHooks,omitempty"`
	DisableOpenAPIValidation bool                `json:"disableOpenAPIValidation,omitempty"`
	SkipSchemaValidation     bool                `json:"skipSchemaValidation,omitempty"`
	Namespace                string              `json:"namespace,omitempty"`
	ProjectID                string              `json:"projectId,omitempty"`
	OperationTolerations     []corev1.Toleration `json:"operationTolerations,omitempty"`
	AutomaticCPTolerations   bool                `json:"automaticCPTolerations,omitempty"`
	Charts                   []ChartInstall      `json:"charts,omitempty"`
}

type ChartInfo struct {
	Readme    string                `json:"readme,omitempty"`
	APPReadme string                `json:"appReadme,omitempty"`
	Values    v3.MapStringInterface `json:"values,omitempty"`
	Questions v3.MapStringInterface `json:"questions,omitempty"`
	Chart     v3.MapStringInterface `json:"chart,omitempty"`
}

// ChartUpgradeAction represents the input received when upgrading the charts received in the charts field
type ChartUpgradeAction struct {
	Timeout                  *metav1.Duration    `json:"timeout,omitempty"`
	Wait                     bool                `json:"wait,omitempty"`
	DisableHooks             bool                `json:"noHooks,omitempty"`
	DisableOpenAPIValidation bool                `json:"disableOpenAPIValidation,omitempty"`
	SkipSchemaValidation     bool                `json:"skipSchemaValidation,omitempty"`
	Force                    bool                `json:"force,omitempty"`
	TakeOwnership            bool                `json:"takeOwnership,omitempty"`
	MaxHistory               int                 `json:"historyMax,omitempty"`
	Install                  bool                `json:"install,omitempty"`
	Namespace                string              `json:"namespace,omitempty"`
	CleanupOnFail            bool                `json:"cleanupOnFail,omitempty"`
	Charts                   []ChartUpgrade      `json:"charts,omitempty"`
	OperationTolerations     []corev1.Toleration `json:"operationTolerations,omitempty"`
	AutomaticCPTolerations   bool                `json:"automaticCPTolerations,omitempty"`
}

type ChartUpgrade struct {
	ChartName   string                `json:"chartName,omitempty"`
	Version     string                `json:"version,omitempty"`
	ReleaseName string                `json:"releaseName,omitempty"`
	Force       bool                  `json:"force,omitempty"`
	ResetValues bool                  `json:"resetValues,omitempty"`
	Description string                `json:"description,omitempty"`
	Values      v3.MapStringInterface `json:"values,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

type ChartActionOutput struct {
	OperationName      string `json:"operationName,omitempty"`
	OperationNamespace string `json:"operationNamespace,omitempty"`
}
