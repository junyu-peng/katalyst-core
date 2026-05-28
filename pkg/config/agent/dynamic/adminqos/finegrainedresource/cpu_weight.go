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

package finegrainedresource

import (
	"k8s.io/klog/v2"

	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic/crd"
)

type CPUWeightConfiguration struct {
	Rules []CPUWeightRule
}

type CPUWeightRule struct {
	Name         string
	PodSelector  string
	NodeSelector string
	PodCPUDemand int64
}

func NewCPUWeightConfiguration() *CPUWeightConfiguration {
	return &CPUWeightConfiguration{}
}

func (c *CPUWeightConfiguration) ApplyConfiguration(conf *crd.DynamicConfigCRD) {
	if aqc := conf.AdminQoSConfiguration; aqc != nil &&
		aqc.Spec.Config.FineGrainedResourceConfig != nil &&
		aqc.Spec.Config.FineGrainedResourceConfig.CPUWeightConfig != nil {

		kccConfig := aqc.Spec.Config.FineGrainedResourceConfig.CPUWeightConfig

		var rules []CPUWeightRule
		for _, r := range kccConfig.Rules {
			rules = append(rules, CPUWeightRule{
				Name:         r.Name,
				PodSelector:  r.PodSelector,
				NodeSelector: r.NodeSelector,
				PodCPUDemand: r.PodCPUDemand,
			})
		}
		c.Rules = rules
		klog.V(4).Infof("[dynamic-cpu-weight] applied %d rules from KCC", len(rules))
	}
}
