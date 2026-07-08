package podkiller

import (
	"testing"

	coreconfig "github.com/kubewharf/katalyst-core/pkg/config"
)

func newRegistryTestRuleInitializer(*coreconfig.Configuration, KillerFactory) (KillerRule, error) {
	return testKillerRule{name: "registry-test-rule", priority: 1, matched: false}, nil
}

func TestRegisterKillerRuleInitializer(t *testing.T) {
	name := "registry-test-rule"
	t.Cleanup(func() {
		killerRuleInitializerLock.Lock()
		delete(killerRuleInitializers, name)
		killerRuleInitializerLock.Unlock()
	})

	RegisterKillerRuleInitializer(name, newRegistryTestRuleInitializer)
	initializers := GetRegisteredKillerRuleInitializers()
	if initializers[name] == nil {
		t.Fatalf("expected rule initializer to be registered")
	}
}

func TestRegisterKillerRuleInitializerPanicsOnDuplicate(t *testing.T) {
	name := "registry-duplicate-rule"
	t.Cleanup(func() {
		killerRuleInitializerLock.Lock()
		delete(killerRuleInitializers, name)
		killerRuleInitializerLock.Unlock()
	})

	RegisterKillerRuleInitializer(name, newRegistryTestRuleInitializer)
	defer func() {
		if recover() == nil {
			t.Fatalf("expected duplicate registration panic")
		}
	}()
	RegisterKillerRuleInitializer(name, newRegistryTestRuleInitializer)
}

func TestGetRegisteredKillerRuleInitializersReturnsCopy(t *testing.T) {
	initializers := GetRegisteredKillerRuleInitializers()
	initializers["mutated-rule"] = newRegistryTestRuleInitializer
	if GetRegisteredKillerRuleInitializers()["mutated-rule"] != nil {
		t.Fatalf("expected registry snapshot to be immutable")
	}
}
