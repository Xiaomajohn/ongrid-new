# 2026-07-06 Monitor / Logs 设备下拉回填 device 事实

## 背景

上一轮（`2026-07-06-edge-task-name-and-precise-query.md`）修完 Edges.tsx 后，遗留同类 Q3 问题：
`/monitor` 和 `/logs` 两个高频页面的"设备"下拉框（option 文本 + onChange 同步 `deviceInput`）直接渲染
`edge.name`（探针自动生成的 `auto-install-{id}-{unix}`），而用户期望看到的是
`device.name` / `device.hostname` / `device.ip_address`。

本轮只修这两处，不动其它页面、不动后端、不动 edge agent。

## 范围

- 仅修改 2 个前端文件
- 不新增 lib / module
- 不修改 Edges.tsx（保留其内部 deviceFacts 实现）
- 不修改后端 / proto / 数据库 / edge agent / install.sh

## 改动 1：`web/src/pages/Monitor.tsx`

### 1.1 新增 import

第 21 行后追加：

```ts
import { listDevices, type Device } from '@/api/devices';
```

### 1.2 新增 state + effect

紧跟现有 `listEdges()` effect 之后（第 339 行后）追加：

```tsx
// mount 时拉一次全量 devices，构造 deviceId → Device 的 map。
// 设备下拉 option 文本和进程面板标题用这里回填 device.name / hostname /
// ip_address 替代 e.name（探针自动生成的 auto-install-{id}-{unix}）。
// 不走 Edges.tsx 的 module-level 缓存：本轮最小改动，不抽 lib。
const [deviceMap, setDeviceMap] = useState<Map<number, Device>>(new Map());
useEffect(() => {
  let cancelled = false;
  listDevices({ limit: 1000 })
    .then((r) => {
      if (cancelled) return;
      const m = new Map<number, Device>();
      for (const d of r.items ?? []) m.set(d.id, d);
      setDeviceMap(m);
    })
    .catch(() => {
      /* best-effort：失败时 option 文本回退到 e.name，行为与现状一致 */
    });
  return () => {
    cancelled = true;
  };
}, []);
```

### 1.3 改设备下拉 option 文本

第 549-566 行的下拉 `options` 数组 `.map` 改为：

```tsx
.map((e) => {
  const d = deviceMap.get(Number(e.device_id));
  const name = d?.name || d?.hostname || d?.ip_address;
  return {
    value: String(e.device_id),
    // Show display name + device_id so collisions on the
    // same hostname stay distinguishable. deviceMap 还没
    // 就绪时回退到 e.name，行为与改动前一致。
    label: `${name || e.name || tr('(未命名)', '(unnamed)')} (#${e.device_id})`,
  };
}),
```

### 1.4 改进程面板标题

第 596-611 行 `ProcessTopPanel` 渲染块，在 `if (!e) return null;` 之后插入：

```tsx
// 用 device 事实回填：deviceMap 命中走 device.name / hostname /
// ip_address，都没就回退 e.name / e.id。
const d = deviceMap.get(did);
const displayName =
  d?.name || d?.hostname || d?.ip_address || e.name || `#${e.id}`;
```

并把 `edgeName={e.name || \`#${e.id}\`}` 改为 `edgeName={displayName}`。

---

## 改动 2：`web/src/pages/Logs.tsx`

### 2.1 新增 import

第 17 行后追加：

```ts
import { listDevices, type Device } from '@/api/devices';
```

### 2.2 新增 state + effect

紧跟现有 `loadEdges` effect 之后（第 425 行后）追加，与 Monitor.tsx 1.2 同结构。

### 2.3 改设备下拉 onChange 同步 deviceInput

第 610-621 行 `<select onChange={...}>`，把：

```tsx
const match = edges.find((d) => String(d.device_id) === v);
setDeviceInput(match ? `${match.name} (#${match.device_id})` : v);
```

改为：

```tsx
const match = edges.find((d) => String(d.device_id) === v);
if (!match) {
  setDeviceInput(v);
  return;
}
// deviceMap 命中走 device 事实，未就绪回退 match.name。
const dev = deviceMap.get(Number(match.device_id));
const display = dev?.name || dev?.hostname || dev?.ip_address || match.name;
setDeviceInput(`${display} (#${match.device_id})`);
```

### 2.4 改设备下拉 option 文本

第 625-631 行的 `.map` 改为：

```tsx
.map((d) => {
  // deviceMap 命中走 device 事实，未就绪回退 d.name。
  const dev = deviceMap.get(Number(d.device_id));
  const name = dev?.name || dev?.hostname || dev?.ip_address || d.name;
  return (
    <option key={d.id} value={String(d.device_id)}>
      {name} (#{d.device_id})
    </option>
  );
})
```

---

## 关键决策

- **不抽 lib**：用户在确认环节明确要求"不引入新模块、不动后端/edge/db"。代价是两份 deviceMap 各自独立（Monitor 一份、Logs 一份），同一 deviceId 在两个页面各发一次 listDevices。可接受：典型部署 < 1000 设备、单次列表请求。
- **listDevices({ limit: 1000 })**：当前典型部署的设备上限；如 > 1000 设备需调大 limit。
- **fallback 链 `device.name || device.hostname || device.ip_address || edge.name`**：与 Edges.tsx 内部实现对齐，device 任何字段可用就用，都没有再退化到 edge.name（探针名），保留现状兜底行为。
- **listDevices 失败静默回退**：best-effort，UI 不会因为这次新加的拉取出现"未定义"或空白。
- **不改 Edges.tsx**：保留其内部 deviceFacts 实现。后续如需统一，可单独开一轮把 Edges.tsx 内部实现切到 lib/deviceFacts 共享模块；本轮不做。
- **未触动 Alerts / Dashboard / Webshell**：这些页面也有同类问题（Target 列、下钻标题、会话审计），但属"事后查看"影响面小，本轮不动，留待后续。

## 编译验证

```bash
cd /root/builder/ongrid-new/web
./node_modules/.bin/tsc --noEmit
# 无新错误（与本次改动相关的 0 错误；其它错误为项目原有：xterm / @xyflow/react /
# @dagrejs/dagre 缺 type 定义，FlowEditor.tsx / XTerminal.tsx 原有 any，与本轮无关）
```

## 不动的清单

- `web/src/pages/Edges.tsx`（保留内部 deviceFacts 实现）
- `web/src/pages/Alerts.tsx` / `Dashboard.tsx` / `settings/Webshell.tsx`（Q3 同类问题，本轮不动）
- `web/src/pages/Tasks.tsx` / `Approvals.tsx` / `Hosts.tsx` + `HostsFilterBar.tsx`（缺搜索/过滤，本轮不动）
- 后端 / proto / 数据库 / edge agent / install.sh 全部 0 改动
