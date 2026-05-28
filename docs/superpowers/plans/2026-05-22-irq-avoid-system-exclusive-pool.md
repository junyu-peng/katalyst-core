# IRQ Avoid System Exclusive Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make IRQ binding and `interrupt` pool allocation strictly avoid `system exclusive pool`, and reject conflicting system pool updates before state is persisted.

**Architecture:** Extend the IRQ forbidden CPU source in `DynamicPolicy` so IRQ-exclusive CPU selection treats system exclusive pools as forbidden. Add a validation step in system exclusive pool reconciliation that checks the post-update state against the current interrupt pool before persisting machine state.

**Tech Stack:** Go, Katalyst CPU dynamic policy, Go test

---

### Task 1: Extend IRQ forbidden CPU calculation

**Files:**
- Modify: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_irq_tuner.go`
- Test: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_irq_tuner_test.go`

- [ ] Add a failing test asserting `GetIRQForbiddenCores()` includes reserved CPUs and system exclusive pool CPUs.
- [ ] Run `go test ./pkg/agent/qrm-plugins/cpu/dynamicpolicy -run 'TestDynamicPolicy_GetIRQForbiddenCores|TestDynamicPolicy_SetExclusiveIRQCPUSet' -count=1` and verify the new test fails for the expected reason.
- [ ] Update `GetIRQForbiddenCores()` to union reserved CPUs with all pool entries matching `commonstate.IsSystemPool`.
- [ ] Re-run the focused IRQ tests and verify they pass.

### Task 2: Reject conflicting system exclusive pool changes

**Files:**
- Modify: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_async_handler.go`
- Test: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_async_handler_test.go`

- [ ] Add a failing test covering `applySystemExclusivePoolChanges()` when the resulting system pool overlaps with the current interrupt pool.
- [ ] Run `go test ./pkg/agent/qrm-plugins/cpu/dynamicpolicy -run 'TestApplySystemExclusivePoolChanges' -count=1` and verify the conflict test fails for the expected reason.
- [ ] Add `validateSystemExclusivePoolsAgainstInterruptPool()` and call it from `applySystemExclusivePoolChanges()` before adjusting system pods or persisting machine state.
- [ ] Re-run the focused async-handler tests and verify they pass.

### Task 3: Final verification

**Files:**
- Modify: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_irq_tuner.go`
- Modify: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_async_handler.go`
- Test: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_irq_tuner_test.go`
- Test: `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_async_handler_test.go`

- [ ] Run `go test ./pkg/agent/qrm-plugins/cpu/dynamicpolicy -run 'TestDynamicPolicy_GetIRQForbiddenCores|TestDynamicPolicy_SetExclusiveIRQCPUSet|TestApplySystemExclusivePoolChanges' -count=1`.
- [ ] Run diagnostics for the edited Go files and fix any introduced issues.
