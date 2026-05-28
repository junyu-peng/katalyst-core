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
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic"
	"github.com/kubewharf/katalyst-core/pkg/config/agent/dynamic/adminqos/finegrainedresource"
	"github.com/kubewharf/katalyst-core/pkg/metaserver"
	"github.com/kubewharf/katalyst-core/pkg/metrics"
	"github.com/kubewharf/katalyst-core/pkg/util/cgroup/common"
	cgroupmgr "github.com/kubewharf/katalyst-core/pkg/util/cgroup/manager"
)

const (
	metricsNameApplyCPUWeight   = "dynamic_cpu_weight_apply"
	metricsNameRestoreCPUWeight = "dynamic_cpu_weight_restore"

	maxCPUShares = 262144
	minCPUShares = 2
	sharesPerCPU = 1024
	milliCPUPerCPU = 1000
)

type DynamicCPUWeightPlugin struct {
	sync.RWMutex
	metaServer      *metaserver.MetaServer
	dynamicConfig   *dynamic.DynamicAgentConfiguration
	emitter         metrics.MetricEmitter
	interval        time.Duration
	originalWeights map[string]OriginalWeight
}

type OriginalWeight struct {
	CGV1Shares uint64
}

func NewDynamicCPUWeightPlugin(metaServer *metaserver.MetaServer, dynamicConfig *dynamic.DynamicAgentConfiguration,
	emitter metrics.MetricEmitter, interval time.Duration,
) *DynamicCPUWeightPlugin {
	return &DynamicCPUWeightPlugin{
		metaServer:      metaServer,
		dynamicConfig:   dynamicConfig,
		emitter:         emitter,
		interval:        interval,
		originalWeights: make(map[string]OriginalWeight),
	}
}

func (p *DynamicCPUWeightPlugin) Name() string {
	return "dynamic-cpu-weight"
}

func (p *DynamicCPUWeightPlugin) Run(ctx context.Context) {
	if p.interval <= 0 {
		klog.Infof("[dynamic-cpu-weight] plugin disabled (interval <= 0)")
		return
	}

	klog.Infof("[dynamic-cpu-weight] starting plugin with interval %v", p.interval)

	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
		p.execute(ctx)
	}, p.interval, 0.1, true)

	<-ctx.Done()
	klog.Infof("[dynamic-cpu-weight] plugin stopped")
}

func (p *DynamicCPUWeightPlugin) execute(ctx context.Context) {
	pods, err := p.metaServer.GetPodList(ctx, nil)
	if err != nil {
		klog.Errorf("[dynamic-cpu-weight] failed to get pods: %v", err)
		return
	}

	node, err := p.metaServer.GetNode(ctx)
	if err != nil {
		klog.Errorf("[dynamic-cpu-weight] failed to get node: %v", err)
		return
	}

	rules := p.getRules()
	if len(rules) == 0 {
		return
	}

	p.processPods(ctx, pods, rules, node)
}

func (p *DynamicCPUWeightPlugin) processPods(ctx context.Context, pods []*v1.Pod,
	rules []finegrainedresource.DynamicCPUWeightRule, node *v1.Node,
) {
	for _, pod := range pods {
		if pod.Status.Phase != v1.PodRunning {
			continue
		}

		rule := p.findMatchingRule(pod, rules, node.Labels)

		for _, container := range pod.Spec.Containers {
			key := podUIDContainerKey(string(pod.UID), container.Name)

			if rule != nil {
				p.applyTarget(ctx, pod, container, rule.TargetCPUWeight, key)
			} else {
				p.restoreOriginal(ctx, pod, container, key)
			}
		}
	}
}

func (p *DynamicCPUWeightPlugin) applyTarget(ctx context.Context, pod *v1.Pod,
	container v1.Container, cpuDemand int64, key string,
) {
	cpuShares := cpuDemandToShares(cpuDemand)

	p.Lock()
	if _, exists := p.originalWeights[key]; !exists {
		original, err := p.getOriginalWeight(pod, container)
		if err != nil {
			klog.Warningf("[dynamic-cpu-weight] failed to get original weight for %s: %v", key, err)
			p.Unlock()
			return
		}
		p.originalWeights[key] = original
		klog.V(4).Infof("[dynamic-cpu-weight] saved original weight for %s: shares=%d",
			key, original.CGV1Shares)
	}
	p.Unlock()

	if err := p.setCPUWeight(pod, container, cpuShares); err != nil {
		klog.Errorf("[dynamic-cpu-weight] failed to apply target weight for %s: %v", key, err)
		_ = p.emitter.StoreInt64(metricsNameApplyCPUWeight, 1, metrics.MetricTypeNameCount,
			metrics.MetricTag{Key: "status", Val: "failed"})
		return
	}

	klog.V(2).Infof("[dynamic-cpu-weight] applied target weight for %s: cpuDemand=%d, shares=%d",
		key, cpuDemand, cpuShares)
	_ = p.emitter.StoreInt64(metricsNameApplyCPUWeight, 1, metrics.MetricTypeNameCount,
		metrics.MetricTag{Key: "status", Val: "success"})
}

func (p *DynamicCPUWeightPlugin) restoreOriginal(ctx context.Context, pod *v1.Pod,
	container v1.Container, key string,
) {
	p.Lock()
	original, exists := p.originalWeights[key]
	if !exists {
		p.Unlock()
		return
	}
	delete(p.originalWeights, key)
	p.Unlock()

	if err := p.setCPUWeight(pod, container, original.CGV1Shares); err != nil {
		klog.Errorf("[dynamic-cpu-weight] failed to restore original weight for %s: %v", key, err)
		_ = p.emitter.StoreInt64(metricsNameRestoreCPUWeight, 1, metrics.MetricTypeNameCount,
			metrics.MetricTag{Key: "status", Val: "failed"})
		return
	}

	klog.V(2).Infof("[dynamic-cpu-weight] restored original weight for %s", key)
	_ = p.emitter.StoreInt64(metricsNameRestoreCPUWeight, 1, metrics.MetricTypeNameCount,
		metrics.MetricTag{Key: "status", Val: "success"})
}

func (p *DynamicCPUWeightPlugin) setCPUWeight(pod *v1.Pod, container v1.Container, shares uint64) error {
	containerID := p.getContainerID(pod, container.Name)
	if containerID == "" {
		return fmt.Errorf("container %s not found in pod %s status", container.Name, pod.Name)
	}

	absCgroupPath, err := common.GetContainerAbsCgroupPath(common.CgroupSubsysCPU, string(pod.UID), containerID)
	if err != nil {
		return fmt.Errorf("failed to get cgroup path: %v", err)
	}

	cpuData := &common.CPUData{Shares: shares}
	return cgroupmgr.ApplyCPUWithAbsolutePath(absCgroupPath, cpuData)
}

func (p *DynamicCPUWeightPlugin) getOriginalWeight(pod *v1.Pod, container v1.Container) (OriginalWeight, error) {
	containerID := p.getContainerID(pod, container.Name)
	if containerID == "" {
		return OriginalWeight{}, fmt.Errorf("container %s not found in pod %s status", container.Name, pod.Name)
	}

	absCgroupPath, err := common.GetContainerAbsCgroupPath(common.CgroupSubsysCPU, string(pod.UID), containerID)
	if err != nil {
		return OriginalWeight{}, fmt.Errorf("failed to get cgroup path: %v", err)
	}

	var currentShares uint64
	if common.CheckCgroup2UnifiedMode() {
		currentShares, err = readCgroupParamUint(absCgroupPath, "cpu.weight")
		if err != nil {
			return OriginalWeight{}, fmt.Errorf("failed to read cpu.weight: %v", err)
		}
	} else {
		currentShares, err = readCgroupParamUint(absCgroupPath, "cpu.shares")
		if err != nil {
			return OriginalWeight{}, fmt.Errorf("failed to read cpu.shares: %v", err)
		}
	}

	return OriginalWeight{
		CGV1Shares: currentShares,
	}, nil
}

func (p *DynamicCPUWeightPlugin) getContainerID(pod *v1.Pod, containerName string) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == containerName {
			return cs.ContainerID
		}
	}
	return ""
}

func podUIDContainerKey(podUID string, containerName string) string {
	return fmt.Sprintf("%s_%s", podUID, containerName)
}

func (p *DynamicCPUWeightPlugin) findMatchingRule(pod *v1.Pod,
	rules []finegrainedresource.DynamicCPUWeightRule, nodeLabels map[string]string,
) *finegrainedresource.DynamicCPUWeightRule {
	for i := range rules {
		rule := &rules[i]
		if !matchPodLabels(pod, rule.PodLabels) {
			continue
		}
		if matchNodeLabels(nodeLabels, rule.Trigger.NodeLabels) {
			return &rules[i]
		}
	}
	return nil
}

func (p *DynamicCPUWeightPlugin) getRules() []finegrainedresource.DynamicCPUWeightRule {
	dynamicConf := p.dynamicConfig.GetDynamicConfiguration()
	if dynamicConf == nil || dynamicConf.AdminQoSConfiguration == nil ||
		dynamicConf.AdminQoSConfiguration.FineGrainedResourceConfiguration == nil {
		return nil
	}
	return dynamicConf.AdminQoSConfiguration.FineGrainedResourceConfiguration.DynamicCPUWeightConfiguration.Rules
}

func matchPodLabels(pod *v1.Pod, labels map[string]string) bool {
	if len(labels) == 0 {
		return true
	}
	if pod.Labels == nil {
		return false
	}
	for k, v := range labels {
		if pod.Labels[k] != v {
			return false
		}
	}
	return true
}

func matchNodeLabels(nodeLabels map[string]string, requiredLabels map[string]string) bool {
	if len(requiredLabels) == 0 {
		return true
	}
	for k, v := range requiredLabels {
		if nodeLabels[k] != v {
			return false
		}
	}
	return true
}

func cpuDemandToShares(cpuDemand int64) uint64 {
	milliCPU := cpuDemand * milliCPUPerCPU
	shares := (milliCPU * sharesPerCPU) / milliCPUPerCPU
	if shares < minCPUShares {
		shares = minCPUShares
	}
	if shares > maxCPUShares {
		shares = maxCPUShares
	}
	return uint64(shares)
}

func readCgroupParamUint(cgroupPath, cgroupFile string) (uint64, error) {
	fileName := filepath.Join(cgroupPath, cgroupFile)
	contents, err := ioutil.ReadFile(fileName)
	if err != nil {
		return 0, err
	}

	trimmed := strings.TrimSpace(string(contents))
	res, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to parse %q as a uint from Cgroup file %q", string(contents), fileName)
	}
	return res, nil
}
