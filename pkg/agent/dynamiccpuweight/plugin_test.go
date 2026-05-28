/*
Copyright 2022 The Katalyst Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dynamiccpuweight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic"
	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic/adminqos"
	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic/adminqos/finegrainedresource"
	"github.com/kubewharf/katalyst-core/pkg/metaserver"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent/pod"
)

func generateTestMetaServer(pods []*v1.Pod) *metaserver.MetaServer {
	podFetcher := &pod.PodFetcherStub{
		PodList: pods,
	}

	return &metaserver.MetaServer{
		MetaAgent: &agent.MetaAgent{
			PodFetcher: podFetcher,
		},
	}
}

func generateTestDynamicConfig(rules []finegrainedresource.DynamicCPUWeightRule) *dynamic.DynamicAgentConfiguration {
	dac := dynamic.NewDynamicAgentConfiguration()
	conf := dac.GetDynamicConfiguration()
	conf.AdminQoSConfiguration = adminqos.NewAdminQoSConfiguration()
	conf.AdminQoSConfiguration.FineGrainedResourceConfiguration.DynamicCPUWeightConfiguration = &finegrainedresource.DynamicCPUWeightConfiguration{
		Rules: rules,
	}
	return dac
}

func TestCPUWeightPlugin_Name(t *testing.T) {
	t.Parallel()

	metaServer := generateTestMetaServer(nil)
	dynamicConfig := generateTestDynamicConfig(nil)
	plugin := NewDynamicCPUWeightPlugin(metaServer, dynamicConfig, nil, 0)

	assert.Equal(t, "dynamic-cpu-weight", plugin.Name())
}

func TestCPUWeightPlugin_findMatchingRule(t *testing.T) {
	t.Parallel()

	rules := []finegrainedresource.DynamicCPUWeightRule{
		{
			Name: "rule-1",
			PodLabels: map[string]string{
				"app": "test-app",
			},
			Trigger: finegrainedresource.CPUWeightTrigger{
				NodeLabels: map[string]string{
					"scenario": "lending",
				},
			},
			TargetCPUWeight: 64,
		},
		{
			Name: "rule-2",
			PodLabels: map[string]string{
				"app": "other-app",
			},
			Trigger: finegrainedresource.CPUWeightTrigger{
				NodeLabels: map[string]string{
					"scenario": "reserved",
				},
			},
			TargetCPUWeight: 32,
		},
	}

	plugin := &DynamicCPUWeightPlugin{}

	tests := []struct {
		name        string
		pod         *v1.Pod
		nodeLabels  map[string]string
		expectedNil bool
		expected    *finegrainedresource.DynamicCPUWeightRule
	}{
		{
			name: "match rule-1",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test-app",
					},
				},
			},
			nodeLabels: map[string]string{
				"scenario": "lending",
			},
			expectedNil: false,
			expected:    &rules[0],
		},
		{
			name: "match rule-2",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "other-app",
					},
				},
			},
			nodeLabels: map[string]string{
				"scenario": "reserved",
			},
			expectedNil: false,
			expected:    &rules[1],
		},
		{
			name: "pod labels not match",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "unknown-app",
					},
				},
			},
			nodeLabels: map[string]string{
				"scenario": "lending",
			},
			expectedNil: true,
		},
		{
			name: "node labels not match",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test-app",
					},
				},
			},
			nodeLabels: map[string]string{
				"scenario": "other",
			},
			expectedNil: true,
		},
		{
			name: "empty pod labels",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			nodeLabels: map[string]string{
				"scenario": "lending",
			},
			expectedNil: true,
		},
		{
			name: "nil pod labels",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: nil,
				},
			},
			nodeLabels: map[string]string{
				"scenario": "lending",
			},
			expectedNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := plugin.findMatchingRule(tt.pod, rules, tt.nodeLabels)
			if tt.expectedNil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, tt.expected.Name, result.Name)
				assert.Equal(t, tt.expected.TargetCPUWeight, result.TargetCPUWeight)
			}
		})
	}
}

func TestCPUWeightPlugin_getRules(t *testing.T) {
	t.Parallel()

	expectedRules := []finegrainedresource.DynamicCPUWeightRule{
		{
			Name:            "test-rule",
			PodLabels:       map[string]string{"app": "test"},
			Trigger:         finegrainedresource.CPUWeightTrigger{NodeLabels: map[string]string{"key": "value"}},
			TargetCPUWeight: 128,
		},
	}

	dynamicConfig := generateTestDynamicConfig(expectedRules)
	metaServer := generateTestMetaServer(nil)
	plugin := NewDynamicCPUWeightPlugin(metaServer, dynamicConfig, nil, 0)

	rules := plugin.getRules()
	assert.Equal(t, 1, len(rules))
	assert.Equal(t, "test-rule", rules[0].Name)
	assert.Equal(t, int64(128), rules[0].TargetCPUWeight)
}

func TestCPUWeightPlugin_getRules_NilConfig(t *testing.T) {
	t.Parallel()

	dac := dynamic.NewDynamicAgentConfiguration()
	conf := dac.GetDynamicConfiguration()
	conf.AdminQoSConfiguration = nil

	metaServer := generateTestMetaServer(nil)
	plugin := NewDynamicCPUWeightPlugin(metaServer, dac, nil, 0)

	rules := plugin.getRules()
	assert.Nil(t, rules)
}

func TestCPUWeightPlugin_getRules_EmptyRules(t *testing.T) {
	t.Parallel()

	dynamicConfig := generateTestDynamicConfig(nil)
	metaServer := generateTestMetaServer(nil)
	plugin := NewDynamicCPUWeightPlugin(metaServer, dynamicConfig, nil, 0)

	rules := plugin.getRules()
	assert.Equal(t, 0, len(rules))
}

func TestCPUWeightPlugin_matchPodLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		podLabels  map[string]string
		ruleLabels map[string]string
		expected   bool
	}{
		{
			name: "empty rule labels",
			podLabels: map[string]string{
				"app": "test",
			},
			ruleLabels: map[string]string{},
			expected:   true,
		},
		{
			name: "nil rule labels",
			podLabels: map[string]string{
				"app": "test",
			},
			ruleLabels: nil,
			expected:   true,
		},
		{
			name:       "match single label",
			podLabels:  map[string]string{"app": "test"},
			ruleLabels: map[string]string{"app": "test"},
			expected:   true,
		},
		{
			name: "match multiple labels",
			podLabels: map[string]string{
				"app":     "test",
				"version": "v1",
			},
			ruleLabels: map[string]string{
				"app":     "test",
				"version": "v1",
			},
			expected: true,
		},
		{
			name: "pod has extra labels",
			podLabels: map[string]string{
				"app":     "test",
				"version": "v1",
				"extra":   "value",
			},
			ruleLabels: map[string]string{
				"app": "test",
			},
			expected: true,
		},
		{
			name: "value mismatch",
			podLabels: map[string]string{
				"app": "test",
			},
			ruleLabels: map[string]string{
				"app": "other",
			},
			expected: false,
		},
		{
			name:       "pod label missing",
			podLabels:  map[string]string{"version": "v1"},
			ruleLabels: map[string]string{"app": "test"},
			expected:   false,
		},
		{
			name:       "nil pod labels",
			podLabels:  nil,
			ruleLabels: map[string]string{"app": "test"},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: tt.podLabels,
				},
			}
			result := matchPodLabels(pod, tt.ruleLabels)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCPUWeightPlugin_matchNodeLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		nodeLabels     map[string]string
		requiredLabels map[string]string
		expected       bool
	}{
		{
			name:           "empty required labels",
			nodeLabels:     map[string]string{"key": "value"},
			requiredLabels: map[string]string{},
			expected:       true,
		},
		{
			name:           "nil required labels",
			nodeLabels:     map[string]string{"key": "value"},
			requiredLabels: nil,
			expected:       true,
		},
		{
			name:           "match single label",
			nodeLabels:     map[string]string{"scenario": "lending"},
			requiredLabels: map[string]string{"scenario": "lending"},
			expected:       true,
		},
		{
			name: "match multiple labels",
			nodeLabels: map[string]string{
				"scenario": "lending",
				"region":   "us-east",
			},
			requiredLabels: map[string]string{
				"scenario": "lending",
				"region":   "us-east",
			},
			expected: true,
		},
		{
			name: "node has extra labels",
			nodeLabels: map[string]string{
				"scenario": "lending",
				"region":   "us-east",
				"extra":    "value",
			},
			requiredLabels: map[string]string{
				"scenario": "lending",
			},
			expected: true,
		},
		{
			name:           "value mismatch",
			nodeLabels:     map[string]string{"scenario": "lending"},
			requiredLabels: map[string]string{"scenario": "reserved"},
			expected:       false,
		},
		{
			name:           "node label missing",
			nodeLabels:     map[string]string{"region": "us-east"},
			requiredLabels: map[string]string{"scenario": "lending"},
			expected:       false,
		},
		{
			name:           "nil node labels",
			nodeLabels:     nil,
			requiredLabels: map[string]string{"scenario": "lending"},
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchNodeLabels(tt.nodeLabels, tt.requiredLabels)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCPUWeightPlugin_cpuDemandToShares(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cpuDemand int64
		expected  uint64
	}{
		{
			name:      "1 core",
			cpuDemand: 1,
			expected:  1024,
		},
		{
			name:      "2 cores",
			cpuDemand: 2,
			expected:  2048,
		},
		{
			name:      "64 cores",
			cpuDemand: 64,
			expected:  65536,
		},
		{
			name:      "128 cores",
			cpuDemand: 128,
			expected:  131072,
		},
		{
			name:      "256 cores",
			cpuDemand: 256,
			expected:  262144,
		},
		{
			name:      "512 cores (exceeds max)",
			cpuDemand: 512,
			expected:  262144,
		},
		{
			name:      "0 cores (minimum)",
			cpuDemand: 0,
			expected:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cpuDemandToShares(tt.cpuDemand)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCPUWeightPlugin_podUIDContainerKey(t *testing.T) {
	t.Parallel()

	key := podUIDContainerKey("pod-uid-123", "container-1")
	assert.Equal(t, "pod-uid-123_container-1", key)
}

func TestCPUWeightPlugin_applyAndRestore(t *testing.T) {
	t.Parallel()

	pods := []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:  "test-pod-uid",
				Name: "test-pod",
				Labels: map[string]string{
					"app": "test-app",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name: "test-container",
					},
				},
			},
			Status: v1.PodStatus{
				Phase: v1.PodRunning,
				ContainerStatuses: []v1.ContainerStatus{
					{
						Name:        "test-container",
						ContainerID: "containerd://test-container-id",
					},
				},
			},
		},
	}

	rules := []finegrainedresource.DynamicCPUWeightRule{
		{
			Name: "test-rule",
			PodLabels: map[string]string{
				"app": "test-app",
			},
			Trigger: finegrainedresource.CPUWeightTrigger{
				NodeLabels: map[string]string{
					"scenario": "lending",
				},
			},
			TargetCPUWeight: 64,
		},
	}

	metaServer := generateTestMetaServer(pods)
	dynamicConfig := generateTestDynamicConfig(rules)
	plugin := NewDynamicCPUWeightPlugin(metaServer, dynamicConfig, nil, 0)

	assert.Equal(t, 1, len(plugin.getRules()))

	dynamicConfigWithoutRule := generateTestDynamicConfig(nil)
	plugin2 := NewDynamicCPUWeightPlugin(metaServer, dynamicConfigWithoutRule, nil, 0)

	assert.Equal(t, 0, len(plugin2.getRules()))

	plugin.originalWeights["test-pod-uid_test-container"] = OriginalWeight{CGV1Shares: 512}
	assert.Equal(t, 1, len(plugin.originalWeights))

	key := "test-pod-uid_test-container"
	_, exists := plugin.originalWeights[key]
	assert.True(t, exists)

	delete(plugin.originalWeights, key)
	assert.Equal(t, 0, len(plugin.originalWeights))
}
