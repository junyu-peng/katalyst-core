# `adjustAllocationEntries` 方法上下游调用链路梳理

## 1. 函数定位

`adjustAllocationEntries` 定义在：

- `pkg/agent/qrm-plugins/cpu/dynamicpolicy/policy_allocation_handlers.go`

核心职责可以概括为一句话：

> 在 `podEntries` / `machineState` 发生变化后，重新计算 shared pool、isolated container、reclaim pool 等 CPU 分配结果，并把最新结果回写到本地 state/checkpoint。

对应实现入口：

- `adjustAllocationEntries(persistCheckpoint bool)`

它本身不做“新容器选核”这类初始分配，而是做一次 **全局重整**：

- 重新计算各 pool 应该占多少核
- 重新计算 dedicated 非 NUMA-binding 容器是否可以被隔离
- 重新生成 reclaim pool / share pool / reserve pool 的 cpuset
- 把新的 `podEntries` 和 `machineState` 写回 state
- 清理已经没有容器引用的空 pool

## 2. 上游调用链路

当前 CPU dynamic policy 中，`adjustAllocationEntries` 只有 3 个直接调用点。

### 2.1 Allocate 分配链路

主链路：

```text
Allocate
  -> allocationHandlers[qosLevel]
  -> dedicatedCoresAllocationHandler
  -> dedicatedCoresWithNUMABindingAllocationHandler
  -> generateMachineStateFromPodEntries
  -> adjustAllocationEntries
  -> PackAllocationResponse
```

说明：

- `Allocate` 是 Resource Plugin 的主分配入口，会先识别 QoS、请求量、pod/container 元数据。
- `allocationHandlers[qosLevel]` 按 QoS 分发到具体分配函数。
- `dedicatedCoresAllocationHandler` 负责 dedicated_cores 的大类分流。
- `dedicatedCoresWithNUMABindingAllocationHandler` 为 NUMA binding dedicated 容器做实际选核，并先把新的 `AllocationInfo` 写入 state。
- `generateMachineStateFromPodEntries` 基于新 `podEntries` 重建 `machineState`。
- `adjustAllocationEntries` 再对全局 pool / reclaim / isolated 结果做一次统一收敛，避免只改了单个容器而没有同步刷新其他共享结构。

这个链路的触发场景是：

- 新容器第一次分配 CPU
- 已有 dedicated NUMA-binding 容器重新分配

### 2.2 RemovePod 删除链路

主链路：

```text
RemovePod
  -> removePod
  -> adjustAllocationEntries
  -> StoreState
```

说明：

- `RemovePod` 是 pod 生命周期结束后的回收入口。
- `removePod` 先把 pod 从 `podEntries` 中删除，并重建 `machineState`。
- `adjustAllocationEntries` 再把 pod 删除后空出来的 CPU 重新分配给共享池、reclaim 池、isolated 容器等。
- 最后 `StoreState` 持久化最新 checkpoint。

这个链路的本质是：

- **删除 pod 后的全局 CPU 拓扑重整**

### 2.3 异步残留状态清理链路

主链路：

```text
clearResidualState
  -> 删除残留 podEntries
  -> generateMachineStateFromPodEntries
  -> adjustAllocationEntries
  -> StoreState
```

说明：

- `clearResidualState` 是异步周期任务，用来清理“状态里还有，但 pod watcher 已经看不到”的残留 Pod。
- 它先扫描 `metaServer.GetPodList()` 与本地 `podEntries` 的差异。
- 对命中次数超过阈值的残留 Pod，从 state 中删除。
- 删除后立即调用 `adjustAllocationEntries`，把残留 pod 占用过的 CPU 重新纳入统一分配。

这个链路的本质是：

- **异步纠偏后的全局状态重算**

## 3. `adjustAllocationEntries` 内部下游调用链路

主流程可以压缩成下面这条链：

```text
adjustAllocationEntries
  -> GetPodEntries / GetMachineState
  -> 计算 poolsQuantityMap
  -> GetIsolatedQuantityMapFromPodEntries
  -> adjustPoolsAndIsolatedEntries
       -> getReclaimOverlapShareRatio
       -> generatePoolsAndIsolation
            -> generateNUMABindingPoolsCPUSetInPlace
            -> takeCPUsForContainers
            -> takeCPUsForPoolsInPlace / generateProportionalPoolsCPUSetInPlace
            -> apportionReclaimedPool
       -> reclaimOverlapNUMABinding
       -> applyPoolsAndIsolatedInfo
            -> reviseReclaimPool
            -> getAllocationPoolEntry
            -> updateReclaimAllocationResultByPoolEntry
            -> generateMachineStateFromPodEntries
            -> SetPodEntries / SetMachineState / StoreState
       -> cleanPools
```

下面按执行顺序拆开说明。

### 3.1 读取当前状态

`adjustAllocationEntries` 开头先取两份全局状态：

- `entries := p.state.GetPodEntries()`
- `machineState := p.state.GetMachineState()`

作用：

- `podEntries` 描述“当前有哪些 pod/container/pool entry，以及它们的 AllocationInfo”
- `machineState` 描述“当前各 NUMA 节点上的 CPU 使用状态”

这两份数据是后续重算的输入基线。

### 3.2 计算 `poolsQuantityMap`

这一段有两条分支：

#### 分支 A：CPU Advisor 健康且启用

链路：

```text
entries.GetFilteredPoolsCPUSetMap
  -> machine.ParseCPUAssignmentQuantityMap
```

作用：

- 直接相信 sys-advisor 当前给出的 pool cpuset 大小
- 把 pool 的 cpuset 解析成“pool -> NUMA -> quantity”这类数量映射

适用语义：

- **pool 比例由 advisor 主导**

#### 分支 B：CPU Advisor 不可用或退化

链路：

```text
state.GetSharedQuantityMapFromPodEntries
  -> state.CountAllocationInfosToPoolsQuantityMap
```

作用：

- 遍历当前 shared 类容器的 `AllocationInfo`
- 按容器请求量汇总出每个 pool 期望占用的 CPU 数量

适用语义：

- **pool 比例由 qrm 根据容器 request 自行推导**

### 3.3 计算 `isolatedQuantityMap`

调用：

- `state.GetIsolatedQuantityMapFromPodEntries`

作用：

- 找出需要被“独占隔离”的容器
- 目前重点针对 `dedicated_cores` 且 **非 NUMA binding** 的容器
- 生成 `podUID -> containerName -> quantity` 的隔离需求图

这一步的意义是：

- 后续在总可分配 CPU 中，优先尝试给这些容器切出独立 cpuset

### 3.4 进入总调度函数 `adjustPoolsAndIsolatedEntries`

这是 `adjustAllocationEntries` 的核心执行器，负责把“数量需求”转成“真实 cpuset”。

它内部大致分 5 步。

#### 第 1 步：计算可分配 CPU

链路：

```text
machineState.GetFilteredAvailableCPUSet
  -> state.GetNotAllocatablePoolsCPUs
```

作用：

- 从 `machineState` 中拿到当前可用于用户容器分配的 CPU
- 过滤 reserved CPU
- 过滤 dedicated NUMA-exclusive 已占用 CPU
- 再扣掉不允许分给普通业务容器的 pool CPU

产物：

- `availableCPUs`

#### 第 2 步：计算 reclaim 与 shared pool 的重叠比例

调用：

- `getReclaimOverlapShareRatio`

作用：

- 当开启 `allowSharedCoresOverlapReclaimedCores` 时，尽量保留 shared pool 与 reclaim pool 之间已有的重叠关系
- 如果当前已经存在重叠，就按历史重叠比例继承
- 如果当前没有重叠，就根据 shared 容器请求量与其 cpuset 大小反推一个重叠比例

意义：

- 降低 pool cpuset 大幅跳变的概率

#### 第 3 步：生成 pool 和 isolated 的目标 cpuset

调用：

- `generatePoolsAndIsolation`

它是整个重算逻辑里最重要的“算图”函数，内部又分成几个子步骤。

##### 3.4.3.1 处理 NUMA-binding pool

调用：

- `generateNUMABindingPoolsCPUSetInPlace`

作用：

- 先把带 NUMA 约束的 pool 单独处理
- 按 NUMA 维度把 pool 请求量聚合
- 对每个 NUMA 节点，在本 NUMA 可用 CPU 中给对应 pool 分配 cpuset

进一步下钻：

- 若 NUMA 内 CPU 足够且 reclaim 开启：走 `takeCPUsForPoolsInPlace`
- 若 NUMA 内 CPU 不够，或者不允许严格独占：走 `generateProportionalPoolsCPUSetInPlace`

##### 3.4.3.2 处理 isolated 容器

调用：

- `takeCPUsForContainers`

作用：

- 给 dedicated 非 NUMA-binding 容器按数量切出独立 cpuset
- 分配成功后，这部分 CPU 将不再参与共享 pool 分配

##### 3.4.3.3 处理非 NUMA-binding shared pool

调用：

- `takeCPUsForPoolsInPlace`
- 或 `generateProportionalPoolsCPUSetInPlace`

作用：

- 如果 CPU 充足，就按请求量尽量满足每个 pool
- 如果 CPU 不足，就按比例缩放各 pool 的 cpuset 大小

其中：

- `takeCPUsForPoolsInPlace` 最终会调用 `takeCPUsForPools`
- `takeCPUsForPools` 用 `calculator.TakeByNUMABalance` 从可用 CPU 中真正取核

##### 3.4.3.4 处理 reserve / reclaim pool

作用：

- `reserve` pool 强制固定为 `p.reservedCPUs`
- 剩余 CPU 默认并入 `reclaim` pool
- 如果 reclaim 关闭但 reclaim pool 仍有多余 CPU，会调用 `apportionReclaimedPool` 把 reclaim CPU 按比例返还给其他非绑定 pool
- 如果允许 shared 和 reclaim overlap，还会按 `reclaimOverlapShareRatio` 反向构造 overlap CPU

最终产物：

- `poolsCPUSet`
- `isolatedCPUSet`

#### 第 4 步：修正 reclaim 与 NUMA-binding dedicated 的重叠

调用：

- `reclaimOverlapNUMABinding`

作用：

- reclaim pool 在某些场景下需要和“非 ramp-up 的 dedicated NUMA-binding 主容器”保持历史交集
- 这样可以避免 reclaim pool 在这些 NUMA 上被完全抽空，影响后续行为一致性

约束条件：

- 仅在 `enableCPUAdvisor && EnableReclaim` 时生效

#### 第 5 步：把计算结果写回 state

调用：

- `applyPoolsAndIsolatedInfo`

这是整个链路里的“落盘前组装器”，负责把 `poolsCPUSet` / `isolatedCPUSet` 真正翻译成新的 `PodEntries`。

它内部又分为三段：

##### A. 构造 isolated 容器 entry

作用：

- 为 dedicated 非 NUMA-binding 容器写入独立的 `AllocationResult`
- 同时生成对应 `TopologyAwareAssignments`

##### B. 构造所有 pool entry

作用：

- 为 `reserve` / `reclaim` / `share` / 其他 pool 统一创建或刷新 pool entry
- 同时上报 pool size metric

这里还会调用：

- `reviseReclaimPool`

其作用是：

- 确保 reclaim pool 一定存在
- 修正 reclaim pool 在不同 NUMA 上的分布，避免与实际 NUMA binding 语义冲突

##### C. 构造普通容器 entry

作用：

- shared 容器绑定到所属 pool 的 cpuset
- reclaimed 容器根据 pool entry 更新自身结果
- dedicated NUMA-binding 容器直接沿用已有独占结果
- dedicated 非 NUMA-binding 且本轮仍未被隔离时，临时落到 fallback/ramp-up cpuset

这里会继续依赖：

- `getAllocationPoolEntry`：获取目标 pool 的 entry
- `updateReclaimAllocationResultByPoolEntry`：用 pool entry 更新 reclaimed 容器的结果

最后：

- `generateMachineStateFromPodEntries`：用新的 `newPodEntries` 重新生成 `machineState`
- `p.state.SetPodEntries(...)`
- `p.state.SetMachineState(...)`
- `p.state.StoreState()`（在 `persistCheckpoint=true` 时）

#### 第 6 步：清理无效 pool

调用：

- `cleanPools`

作用：

- 遍历当前所有业务容器，统计仍被引用的 pool
- 删除那些“entry 还在，但已经没有任何容器引用”的 pool
- 再次重建 `machineState` 并写回 state

它的意义是：

- 防止历史遗留 pool entry 长时间残留在 checkpoint 中

## 4. 各核心函数作用汇总

### 4.1 上游入口函数

- `Allocate`：CPU 资源分配总入口，识别 QoS 后分发到具体 handler。
- `dedicatedCoresAllocationHandler`：dedicated_cores 的分类入口，决定是否进入 NUMA binding 分配。
- `dedicatedCoresWithNUMABindingAllocationHandler`：为 dedicated NUMA-binding 容器选核、写入 `AllocationInfo`，然后触发全局重整。
- `RemovePod`：Pod 删除入口，回收 state 后触发全局重整。
- `clearResidualState`：异步清理残留 Pod state，清理后触发全局重整。

### 4.2 `adjustAllocationEntries` 直接依赖函数

- `GetFilteredPoolsCPUSetMap`：抽取当前 pool 的 cpuset 视图。
- `ParseCPUAssignmentQuantityMap`：把 cpuset 解析为 pool 的数量需求映射。
- `GetSharedQuantityMapFromPodEntries`：从 shared 容器 request 聚合 pool 期望数量。
- `GetIsolatedQuantityMapFromPodEntries`：从 dedicated 非 NUMA-binding 容器聚合隔离需求。
- `adjustPoolsAndIsolatedEntries`：总控函数，把“数量”转换成“cpuset + state”。

### 4.3 `adjustPoolsAndIsolatedEntries` 核心依赖

- `getReclaimOverlapShareRatio`：计算 reclaim 与 shared 的历史重叠比例。
- `generatePoolsAndIsolation`：生成 pools/isolated 的目标 cpuset。
- `reclaimOverlapNUMABinding`：修正 reclaim 与 dedicated NUMA-binding 的重叠关系。
- `applyPoolsAndIsolatedInfo`：把目标 cpuset 回写成新的 `PodEntries` 和 `machineState`。
- `cleanPools`：清理孤儿 pool entry。

## 5. 关键数据流转

可以把这条链路理解成下面这个“输入 -> 中间态 -> 输出”模型：

```text
podEntries + machineState
  -> poolsQuantityMap + isolatedQuantityMap
  -> poolsCPUSet + isolatedCPUSet
  -> newPodEntries
  -> newMachineState
  -> persisted checkpoint
```

各数据结构语义如下：

- `podEntries`：当前所有 pod/container/pool 的分配记录
- `machineState`：NUMA 维度的 CPU 使用状态
- `poolsQuantityMap`：每个 pool 目标需要多少 CPU
- `isolatedQuantityMap`：哪些容器需要独占隔离、各自需要多少 CPU
- `poolsCPUSet`：各 pool 最终拿到的 cpuset
- `isolatedCPUSet`：各 isolated 容器最终拿到的 cpuset
- `newPodEntries`：应用本轮重算结果后的新 checkpoint 视图

## 6. 一句话总结

`adjustAllocationEntries` 不是“分配某个容器 CPU”的函数，而是 **在 state 变化后，对整个 CPU 动态策略做一次全局重平衡和 checkpoint 重建的核心收敛点**。

如果从职责上看，它更像：

- **CPU 动态策略中的全局 reconcile / rebalance 函数**

如果从调用时机上看，它主要发生在：

- 新的 dedicated NUMA-binding 容器完成初始分配之后
- Pod 删除之后
- 异步残留 state 清理之后
