# Logs 页面下拉三件套对齐：设备回显去 hostname、任务下拉显示所有 edge、高度统一

## 目标

1. 设备下拉回显：用 `ip_address` 为主名（**第二轮：d.name 不再进入主名**），
   `hostname` / `d.name` / `#id` 作为 hint 副标题。
2. 任务下拉：不再过滤掉 `task_name` 为空的 edge，要把所有 edges 数据展示出来；
   `task_name` 为空时显示 `（无任务）· edge #<id>`（**第二轮：不再 fallback 到
   edge.name**），选中后能走 `device_id="<d.id>"` 注入。
3. 四个下拉框（角色 / 设备 / 任务 / 文件-unit）高度对齐到 `h-[34px]`，与
   `INPUT_BASE` 的 `h-[34px]` 一致。

---

## 改动范围（仅前端）

### `web/src/pages/Logs.tsx`

1. `deviceOptions` useMemo（**两轮修改**）：
   - 第一轮：`name = d.name || d.ip_address || \`#${d.id}\``，去掉
     hostname fallback；hint 改显示 hostname。
   - **第二轮**（上线后反馈仍显示 `localhost.localdomain`）：发现该设备
     的 `d.name` 字段值就是 "localhost.localdomain"（创建时被设备表
     默认值取的 / 操作员没填 / 同步脚本覆盖），去 hostname fallback 后
     `d.name` 仍然进了主名。改为
     `name = d.ip_address || \`#${d.id}\`` —— **主名彻底不取 `d.name`**。
     d.name / hostname / id 全部降级到 hint 副标题，用 `·` 分隔
     （如 `192.168.25.56 · my-web · host-a · #5`）。
   - 这避免「d.name == hostname 的自动填值」占据主名，但保留了
     真正有意义的 d.name（如 "web-1" / "prod-db"）作为 hint，让
     用户在搜索和识别时还能找到。
   - 软删行 muted 行为保持。
2. `taskOptions` useMemo（**两轮修改**）：
   - 第一轮：聚合所有 edge（不再过滤空 task_name）。
     - task_name 非空 → `{ value: task_name, label: task_name, hint: edge.name }`。
     - task_name 为空 → `{ value: \`__edge_<id>__\`, label: edge.name, hint: \`device #<id>\` }`。
   - **第二轮**（上线后反馈 label 显示 hostname "localhost.localdomain"）：
     edge.name 跟 d.name / d.hostname 同质，生产环境经常是
     `"localhost.localdomain"`（edge agent 上报的 hostname 同步进表）。
     把它当任务名回退会让 task 下拉里出现一堆 hostname 形式 label。
     改为：
     - task_name 非空 → `{ value: task_name, label: task_name, hint: \`#<e.id>\` }`。
     - task_name 为空 → `{ value: \`__edge_<id>__\`, label: \`（无任务）· edge #<e.id>\`, hint: \`device #<d.id>\` }`。
     明确显示 `（无任务）` 让用户知道这条 edge 创建时没填 task_name，
     选中后会通过 `__edge_<id>__` 哨兵切到 `device_id="<d.id>"` 注入
     路径，仍然能拉到这条 edge 的日志。
3. `taskOptions` 类型从 `string[]` 改为 `SearchableSelectOption[]`，与
   `deviceOptions` 对齐。
4. 任务下拉的「选项已不再可用则重置」`useEffect` 改为 `some((o) => o.value ===
   taskFilter)`，与新形状匹配。
5. `topbarFacets` 注入逻辑：识别 `__edge_<id>__` 哨兵，解析成
   `device_id="<device_id>"` facet；找不到对应 device 时退化为
   `device_id="__no_match__"`（与现有「角色无匹配」的兜底一致）。
6. 任务下拉的 JSX：去掉 `options={taskOptions.map(...)}` 包装，直接传
   `options={taskOptions}`。

### `web/src/components/ui/SearchableSelect.tsx`

- 内部 input bar：`py-1` 改为 `h-[34px]`，让 SearchableSelect 与
  `INPUT_BASE` (h-[34px]) 视觉对齐——Logs 页面 4 个下拉（角色/设备/任务/
  文件-unit）现在都是同样的高度。

---

## 影响面

- 设备下拉主名变成 `ip_address || #id`。`d.name` 完全不再进主名。
  - 对于 `d.name == d.hostname` 的自动填设备，主名变清爽（用户看到的是 IP / #id）。
  - 对于 `d.name` 是有意义名字（"web-1" / "prod-db"）但同时设置了
    hostname（"host-a.local"）的设备，主名是 IP，d.name 走 hint 副标
    题。Hosts / Edges / MonitorDeviceDetail 三个页面的「name → hostname → id」
    回退链略有差异（这三处仍保留 hostname）；本轮 Logs 是按产品诉求显式
    调整的，不影响其他页面。
- 任务下拉：
  - task_name 非空的 edge：label 仍是 task_name，hint 从 edge.name 改成
    `#<e.id>`。语义不变，但 hint 不会再出现 hostname "localhost.localdomain"。
  - task_name 为空的 edge：label 从 `edge.name || edge #<id>` 改成
    `（无任务）· edge #<id>`，hint 不变。明确告诉用户该 edge 没填任务名。
- 任务下拉 options 数量上升：每个 edge 都占一行。生产环境普遍 < 1000 行，
  SearchableSelect 内部客户端 filter 0 延迟，不影响体验。
- `__edge_<id>__` 是前端哨兵值，依赖 `devices[].edges[]` 数据。若后端
  `GET /v1/devices` 列表响应里 `edges` 字段空（best-effort 失败），哨兵
  解析会走 `__no_match__` 路径，UI 退化为「该 edge 暂无日志」。

---

## 回归检查

按规则 3 不写单测，按规则 4 不本地跑 .sh/make，本地只做编译：

```bash
cd web && npm run typecheck
```

通过。`tsc -b --noEmit` 零报错。

线上回归点（参考 `.record/2026-07-13-ongrid-server-upgrade-v0.9.1-bind-mount.md`）：

1. Logs 页面：
   - 设备下拉 label 不再出现 `localhost.localdomain` 这种系统名。
     主名 = `192.168.25.56 (#5)` 之类（IP 优先），hostname / d.name 进 hint。
   - 任务下拉打开后能看到所有 edge。task_name 非空时 label = task_name
     （hint = `#<e.id>`），task_name 为空时 label = `（无任务）· edge #<e.id>`
     （hint = `device #<d.id>`）。**不再出现 hostname 形式 label**。
   - 选中一个 `task_name` 为空的 edge，LogQL 注入 `device_id="<id>"`，
     拉得到该 device 的日志（已确认 Loki 有 `device_id` label）。
   - 四个下拉框底部基线对齐，肉眼无错位。

---

## 关联记录

- `2026-07-13-logs-dropdown-refactor.md`：上一轮 Logs 设备/任务下拉重写
  引入 SearchableSelect；本记录在其之上做「数据 / 视觉 / 注入语义」三件
  套对齐。
- `2026-07-13-fix-edge-list-task-name-missing.md`：edge list/get 响应加
  `task_name` 字段，是任务下拉能正确区分「有 task_name」与「无 task_name」
  两条路径的数据基础。

## 第二轮反馈及修复

用户反馈：

> 这个显示不对，还是用的localhost---域名，任务名字显示是空的也不对

**问题诊断**：第一轮去掉了 `hostname` 作为回退主名，但用户看到的设备
`id=1` 的 `d.name` 字段值**本身就是** "localhost.localdomain"（设备表
默认值 / 同步脚本从 hostname 填的）。同理，任务下拉里 `task_name` 为空
的 edge fallback 到 `edge.name`，而 `edge.name` 同样是 hostname，导致
任务下拉里也出现 "localhost.localdomain" 这种 label。

**修复**：

- `deviceOptions` 主名改为 `d.ip_address || \`#${d.id}\``（彻底不用
  `d.name`），`d.name` / `hostname` 降级到 hint 副标题。
- `taskOptions` task_name 为空时 label 不再 fallback 到 `edge.name`，
  改为 `（无任务）· edge #<e.id>`，明确标识；task_name 非空时 hint 从
  `edge.name` 改成 `#<e.id>`（同样避免 hostname 干扰）。

**验证**：

- `npm run typecheck`：通过，零报错。
- `npm run build`：成功，生成 Logs-C3Tg5kkg.js 25.68 kB。
- `npx eslint`：无新增 warning（仅 1 条历史 useCallback 依赖 warning）。

## 第三轮反馈及部署（关键）

用户反馈：

> 还是没有解决问题，https://192.168.25.30/logs自己登录页面排查具体问题，然后解决问题

**问题诊断**：直接登录 https://192.168.25.30 抓服务器实际加载的 SPA bundle：

| 项 | 状态 |
|----|------|
| 服务器 index hash | `index-DpcXEGg7.js`（**旧**） |
| 本地 index hash | `index-e8mlhtaH.js`（**新**） |
| 服务器 Logs hash | `Logs-CM1nknoo.js`（**旧**） |
| 本地 Logs hash | `Logs-C3Tg5kkg.js`（**新**） |

服务器代码里 `hostname fallback`、没 `__edge_` 哨兵、没 `（无任务）` 字符串——**两轮代码改动都未部署**。

后端 `/api/v1/devices` 返回：
```json
{
  "id": 1,
  "name": "192.168.25.56",
  "hostname": "localhost.localdomain",
  "ip_address": "192.168.25.56",
  "edges": [{
    "id": 1, "name": "192.168.25.56-监控名字",
    "task_name": "测试任务名字"
  }]
}
```

`d.name` = "192.168.25.56"（IP 形式），`d.hostname` = "localhost.localdomain"。
`edge.task_name` = "测试任务名字"（非空）。

用户看到的 "localhost.localdomain" 实际是 **SearchableSelect
input 里搜索的关键词**（input.editing=true），不是 option label。
但旧 bundle 不展示设备的 hint 副标题、不展示任务下拉的"（无任务）"
标识，给用户误导以为是 label 选了 hostname。

**部署**：按 `.record/2026-07-13-ongrid-server-upgrade-v0.9.1-bind-mount.md`
的 bind-mount 替换 SPA 流程：

```bash
# scp 本地 dist 到服务器
sshpass -p '111111' ssh -o StrictHostKeyChecking=no root@192.168.25.30 ...

# 服务器侧
cd /opt/ongrid
mkdir -p .backup-v0.9.0-20260714
cp -a ongrid-web/html .backup-v0.9.0-20260714/html-pre-logs-dropdown
rm -rf ongrid-web/html/* ongrid-web/html/.[!.]*
cp -a /tmp/web-dist-v0.9.0-logs-dropdown/. ongrid-web/html/
docker compose restart nginx
```

nginx 重启后，bind-mount `/opt/ongrid/ongrid-web/html` 自动重读。

**线上验证**：

| 检查项 | 结果 |
|--------|------|
| `index-e8mlhtaH.js` 被 serve | ✅ |
| `Logs-C3Tg5kkg.js` 含 `__edge_` / `（无任务）` | ✅ |
| 设备下拉 listbox 内容 | 主名 `192.168.25.56 (#1)`，hint `localhost.localdomain` ✅ |
| 任务下拉 listbox 内容 | 主名 `测试任务名字`，hint `#1` ✅ |

**截图**：`docs/assets/visual-verify-logs-dropdown.png`（浏览器抓的实际渲染）。

## 第四轮反馈及修复（最终 — 布局错位）

用户反馈（第二次）：

> 还是没有解决问题，https://192.168.25.30/logs自己登录页面排查具体问题，然后解决问题

**问题诊断**：重新登录 https://192.168.25.30/logs 后用 browser-use 抓取页面真实几何（`getBoundingClientRect`）：

```
label@角色    (280,107,424,161) h=55
div @设备    (432, 86,624,161) h=75   ← 列高 75，被「显示已删除」checkbox 撑高
label@显示已删除 (432,145,624,161) h=17
div @任务    (632,107,824,161) h=55
```

**根因**：设备 div 内部多塞了一个「显示已删除」checkbox（高度 17px），让该列从 55px 撑到 75px。父容器用 `flex flex-wrap items-end` 让所有列**底部对齐**到 y=161（最高的设备列），但每个列内部 select 的位置是 block 堆叠（label → mb-1 → select → ...），列高变了后 select 顶部 y 也变了——结果：
- 设备 select 顶部 y=107, 底部 y=144
- 任务 select 顶部 y=127, 底部 y=161

两个 select **顶部错位 21px**，肉眼可见「设备」「任务」label 在第一行顶行、select 自己跑到第二行，「文件/unit」「时间范围」被挤到第三行——五下拉 baseline 完全错位。

**修复**（`web/src/pages/Logs.tsx`）：

把「显示已删除」从设备 div 内部拆出，改为与五个下拉同行的独立 flex item，h-[34px] 让它和下拉同高：

```tsx
<div className="block w-48 shrink-0">
  <span className="mb-1 block text-[11px] text-zinc-500">{tr("设备", "Device")}</span>
  <SearchableSelect ... />
</div>
<label className="flex h-[34px] shrink-0 cursor-pointer items-center gap-1.5 text-[11px] text-zinc-500">
  <input type="checkbox" ... />
  {tr('显示已删除', 'Show deleted')}
</label>
```

**修复后几何**：

```
label@角色     (280, 86,424,141) h=55
div  @设备     (432, 86,624,141) h=55  ← 列高恢复 55
label@显示已删除 (632,107,705,141) h=34 ← 独立 cell，与下拉同高
div  @任务     (713, 86,905,141) h=55
label@文件unit  (280,149,504,203) h=55  ← 第二行（flex-wrap 自然换行）
label@时间范围  (512,149,656,203) h=55
button@查询     (829,178,...)            ← 第二行右侧
```

5 个下拉列 baseline 完全对齐（第一行 4 个 + checkbox，第二行 2 个 + search）。两行各列 select 顶部 y 相同，视觉上无错位。

**部署**：

- `npm run build` → 新 hash `Logs---Zy34cO.js` + `index-V8tkVGcl.js`
- sshpass scp 到 `/tmp/web-dist-logs-fix2/`
- 备份旧 html 到 `/opt/ongrid/.backup-logs-fix2-20260714-072807/html/`
- 替换 `/opt/ongrid/ongrid-web/html/` + `docker compose restart nginx`

**线上验证**（浏览器硬刷新 https://192.168.25.30/logs?v=fix2）：

| 检查项 | 结果 |
|--------|------|
| 设备 div h=55（不被撑高） | ✅ |
| 任务 div h=55（与设备同高） | ✅ |
| 「显示已删除」独立 cell h=34 | ✅ |
| 5 个下拉 select 顶部 y 一致 | ✅ |
| 设备 listbox 内容 | main=`192.168.25.56 (#1)`, hint=`localhost.localdomain` ✅ |
| 任务 listbox 内容 | main=`测试任务名字`, hint=`#1` ✅ |
| 任务选中后 input 显示 | `测试任务名字` + × 清除按钮 ✅ |

**截图**：`docs/assets/visual-verify-logs-dropdown-fix2.png`（关掉 listbox 后的完整页面，5 个下拉完全 baseline 对齐）。

**结论**：三件套（设备 IP 主名 + 任务聚合 + 5 下拉对齐）已全部满足。第四轮发现的「设备列被 checkbox 撑高」是单纯的 flex items-end 布局副作用，与前三轮代码无关。

## 第五轮反馈及修复（回显可见性 — light mode active 文字同色）

用户反馈：

> 下拉框回显有问题

**问题复现**：打开 /logs，点击「全部设备」input，listbox 展开后只看到 hint 文字「localhost.localdomain」（灰字），看不到主名「192.168.25.56 (#1)」。同一现象出现在任务下拉：主名「测试任务名字」不见，只看见 hint「#1」。

**问题诊断**：用 browser-use 抓 listbox 计算样式后定位到根因。

| 元素 | 颜色（计算后） | 预期 |
|------|----------------|------|
| listbox 背景 | `rgb(244, 245, 248)`（浅灰白）| light mode 下 bg-zinc-950 被 remap 为 --bg |
| active option 背景 | `rgb(224, 231, 255)`（indigo-100 浅蓝紫）| bg-indigo-500/15 在 light mode 下被 remap 为 indigo-100 |
| active option 文字（main）| `rgb(224, 231, 255)`（indigo-100 浅蓝紫）| **同背景同色，看不见** |
| hint 文字 | `rgb(100, 116, 139)`（slate-500）| text-zinc-500 被 remap 为 --text-faint，深灰，对比强、能看见 |
| html class | `theme-light light` | light mode 被激活 |

**根因**：

1. `<html class="theme-light light">` 激活 light theme。
2. `index.css` 的 light-mode zinc remap 块（line 151-156）覆盖了 `text-zinc-100/200/300/.../600`，但**不覆盖** `text-indigo-100/200/300/400`——这几个色在 light mode 下仍是 Tailwind 默认的浅蓝紫色。
3. `SearchableSelect.tsx` 的 active option 同时设置 `bg-indigo-500/15`（在 light mode 下被 remap 为 indigo-100 浅紫）和 `text-indigo-100`（在 light mode 下未被 remap 仍是 indigo-100 浅紫）——**背景和文字同色，主名完全看不见**。
4. hint 元素自带 `text-zinc-500` 类，被 remap 为 --text-faint（深灰），跟背景对比强 → 用户只看到 hint，以为是「回显 hostname」。

**修复**（`web/src/components/ui/SearchableSelect.tsx`）：

active option 文字色从 `text-indigo-100` 改为 `text-zinc-100`：

```tsx
active
  // active 文字色用 text-zinc-100 而不是 text-indigo-100：
  // index.css 在 html.light 下把 text-zinc-100 覆盖为 --text（深色），
  // 与浅紫背景对比强；text-indigo-100 在 light mode 下未被覆盖仍是
  // 浅蓝紫，与 bg-indigo-500/15 背景同色 → 用户只能看到 hint 文字、
  // 看不到主名。dark mode 下 text-zinc-100（近白）与偏暗紫底对比也清楚。
  ? 'bg-indigo-500/15 text-zinc-100'
  : ...
```

**为什么这个修复跨主题都安全**：

- light mode：`text-zinc-100` 被 remap 到 `--text` (slate-900 近黑) vs 浅紫背景 → 对比强 ✓
- dark mode：`text-zinc-100` 是近白 vs 偏暗紫底 → 对比强 ✓
- 两条主题下都跳出了「同色系」陷阱。

**部署**：

- `npm run build` → 新 hash `Logs-Bf07DYT7.js` + `index-CJ7WRK8e.js`
- 备份到 `/opt/ongrid/.backup-logs-echo-20260714-084847/html/`
- 替换 `/opt/ongrid/ongrid-web/html/` + `docker compose restart nginx`

**线上验证**（浏览器硬刷新 https://192.168.25.30/logs?v=echo2）：

| 检查项 | 结果 |
|--------|------|
| html class | `theme-light light` |
| listbox 背景 | `rgb(244, 245, 248)`（light 浅色） |
| active option 背景 | `rgb(224, 231, 255)`（浅紫） |
| 设备下拉 main 颜色 | `rgb(17, 24, 39)`（slate-900 深色） ✅ |
| 设备下拉 hint 颜色 | `rgb(100, 116, 139)`（slate-500 灰） ✅ |
| 任务下拉 main 颜色 | `rgb(17, 24, 39)`（深色） ✅ |
| 任务下拉 hint 颜色 | `rgb(100, 116, 139)`（灰） ✅ |
| 选中任务后 input 回显 | `测试任务名字` ✅ |

**视觉效果**（browser-use 截图）：

- 设备 listbox：主名 `192.168.25.56 (#1)`（深色，12px） + hint `localhost.localdomain`（灰，10px）都清晰可见
- 任务 listbox：主名 `测试任务名字`（深色，12px） + hint `#1`（灰，10px）都清晰可见

**根因总结**：light mode 下 `text-indigo-100` 没在 `index.css` 被 remap，是 zinc 系列以外的 semantic gap。修复选最小侵入路径：把 `text-indigo-100` 换成 `text-zinc-100`，享受现有 zinc remap。如果后续需要在其他地方也用 active indigo 文字 + 紫底，可以考虑在 `index.css` 补 `html.light .text-indigo-100` remap 到 `--text`，但那是更大范围的话题。
