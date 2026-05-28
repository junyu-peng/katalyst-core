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

package cpuweight

import (
	"testing"

	"github.com/bytedance/mockey"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic/adminqos/finegrainedresource"
	"github.com/kubewharf/katalyst-core/pkg/metaserver"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent/pod"
	"github.com/kubewharf/katalyst-core/pkg/util/cgroup/common"
	cgroupmgr "github.com/kubewharf/katalyst-core/pkg/util/cgroup/manager"
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

func TestManager_processRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		pods         []*v1.Pod
		node         *v1.Node
		expectShares []uint64
	}{
		{
			name: "override",
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID:    "pod-1",
						Name:   "pod-1",
						Labels: map[string]string{"app": "test-app"},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{{Name: "c1"}},
					},
					Status: v1.PodStatus{
						Phase: v1.PodRunning,
						ContainerStatuses: []v1.ContainerStatus{{
							Name:        "c1",
							ContainerID: "containerd://cid-1",
						}},
					},
				},
			},
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"scenario": "scheduled-lending"},
				},
			},
			expectShares: []uint64{4096},
		},
		{
			name: "restore",
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID:    "pod-1",
						Name:   "pod-1",
						Labels: map[string]string{"app": "test-app"},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name: "c1",
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU: resource.MustParse("2"),
									},
								},
							},
							{
								Name: "c2",
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU: resource.MustParse("4"),
									},
								},
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodRunning,
						ContainerStatuses: []v1.ContainerStatus{
							{Name: "c1", ContainerID: "containerd://cid-1"},
							{Name: "c2", ContainerID: "containerd://cid-2"},
						},
					},
				},
			},
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"scenario": "other"},
				},
			},
			expectShares: []uint64{6144},
		},
	}

	podCgroupPath := t.TempDir()

	for _, tt := range tests {
		mockey.PatchConvey(tt.name, t, func() {
			var gotShares []uint64
			mockey.Mock(common.GetPodAbsCgroupPath).Return(podCgroupPath, nil).Build()
			mockey.Mock(cgroupmgr.ApplyCPUWithAbsolutePath).To(func(_ string, cpuData *common.CPUData) error {
				gotShares = append(gotShares, cpuData.Shares)
				return nil
			}).Build()

			manager := &managerImpl{metaServer: generateTestMetaServer(tt.pods)}
			rule := finegrainedresource.CPUWeightRule{
				Name:         "rule-1",
				PodSelector:  "app=test-app",
				NodeSelector: "scenario=scheduled-lending",
				PodCPUDemand: 4,
			}

			assert.NoError(t, manager.processRule(rule, tt.node))
			assert.Equal(t, tt.expectShares, gotShares)
		})
	}
}

func TestManager_getPodOriginalCPUCount(t *testing.T) {
	t.Parallel()

	manager := &managerImpl{}

	tests := []struct {
		name     string
		pod      *v1.Pod
		expected int64
	}{
		{
			name: "from annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						CPUDemandCoresAnnotationKey: "8",
					},
				},
			},
			expected: 8,
		},
		{
			name: "from pod request sum",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
						{
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU: resource.MustParse("3"),
								},
							},
						},
					},
				},
			},
			expected: 4,
		},
		{
			name: "annotation invalid falls back to spec",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						CPUDemandCoresAnnotationKey: "invalid",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			expected: 2,
		},
		{
			name: "no annotation and no request",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expected: 0,
		},
		{
			name: "annotation with milli value",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						CPUDemandCoresAnnotationKey: "500m",
					},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := manager.getPodOriginalCPUCount(tt.pod)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManager_getCPUCountFromAnnotation(t *testing.T) {
	t.Parallel()

	manager := &managerImpl{}

	tests := []struct {
		name        string
		annotations map[string]string
		expected    int64
	}{
		{
			name:        "nil annotations",
			annotations: nil,
			expected:    0,
		},
		{
			name:        "annotation not present",
			annotations: map[string]string{"other": "value"},
			expected:    0,
		},
		{
			name: "empty annotation value",
			annotations: map[string]string{
				CPUDemandCoresAnnotationKey: "",
			},
			expected: 0,
		},
		{
			name: "valid integer value",
			annotations: map[string]string{
				CPUDemandCoresAnnotationKey: "8",
			},
			expected: 8,
		},
		{
			name: "valid milli value",
			annotations: map[string]string{
				CPUDemandCoresAnnotationKey: "4000m",
			},
			expected: 4,
		},
		{
			name: "invalid value",
			annotations: map[string]string{
				CPUDemandCoresAnnotationKey: "abc",
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testPod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			result := manager.getCPUCountFromAnnotation(testPod)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCPUWeight_cpuDemandToShares(t *testing.T) {
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := cpuDemandToShares(tt.cpuDemand)
			assert.Equal(t, tt.expected, result)
		})
	}
}
