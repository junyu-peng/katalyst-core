package podkiller

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	coreconfig "github.com/kubewharf/katalyst-core/pkg/config"
	"github.com/kubewharf/katalyst-core/pkg/config/generic"
)

const (
	KillerRuleNameQoSAware = "qos-aware-rule"
	KillerRulePriorityQoS  = 100
)

func init() {
	RegisterKillerRuleInitializer(KillerRuleNameQoSAware, NewQoSAwareRule)
}

type qosAwareRule struct {
	qosConfig *generic.QoSConfiguration
	killerMap map[string]Killer
}

func NewQoSAwareRule(conf *coreconfig.Configuration, factory KillerFactory) (KillerRule, error) {
	killerMap := make(map[string]Killer, len(conf.QoSPodKillers))
	for qosLevel, killerName := range conf.QoSPodKillers {
		killer, err := factory(killerName)
		if err != nil {
			return nil, err
		}
		killerMap[qosLevel] = killer
	}
	return &qosAwareRule{
		qosConfig: conf.QoSConfiguration,
		killerMap: killerMap,
	}, nil
}

func (q *qosAwareRule) Name() string {
	return KillerRuleNameQoSAware
}

func (q *qosAwareRule) Priority() int {
	return KillerRulePriorityQoS
}

func (q *qosAwareRule) Match(pod *v1.Pod) (bool, Killer) {
	qosLevel, err := q.qosConfig.GetQoSLevelForPod(pod)
	if err != nil {
		if pod != nil {
			klog.Warningf("Failed to get QoS level for pod %s/%s: %v, using default killer", pod.Namespace, pod.Name, err)
		} else {
			klog.Warningf("Failed to get QoS level for nil pod: %v, using default killer", err)
		}
		return false, nil
	}

	killer, ok := q.killerMap[qosLevel]
	if !ok {
		return false, nil
	}
	return true, killer
}
