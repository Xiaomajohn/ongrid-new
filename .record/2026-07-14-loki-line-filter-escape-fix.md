# 2026-07-14 Loki line-filter `invalid char escape` 修复

## 问题

Logs 页面发到 `/api/v1/logs/query_range` 的查询在 Loki 端解析失败：

```
parse error at line 1, col 26: invalid char escape
```

URL 解码后的 query：

```
{ongrid_source=~".+"} |~ "(?i)192\.168\.33\.91"
```

错误指向 col 26 —— 也就是 `"(?i)192\.` 里 `\` 字符的位置。

## 根因（实测）

Loki 3.4.0 的查询解析器对 line filter（`|=` / `|~`）的字符串字面量 `"..."`
**先**走一遍字符串 lexer（Go 风格转义），把 `\\` 视作字面 `\`，然后**再**把剩
下的内容交给 RE2 引擎。lexer 只接受以下转义：`\\` `\"` `\n` `\r` `\t` `\f`
等 Go 风格；其它 `\X` 组合直接 400 拒掉。

RE2 引擎**反而**需要 `\.`、`\+`、`\(`、`\[` 等转义来匹配字面字符 —— 也就是
说用户/前端**想**写 `\.` 但字符串 lexer 会先把它当非法 Go 转义拒绝。

所以为了让 RE2 真正拿到 `\.`，前端必须输出 `\\.`：第一遍 lexer 把 `\\` 剥成
`\`，第二遍 RE2 看到 `\.` 视作字面点。

### 实测矩阵（grafana/loki:3.4.0）

| query                           | 结果    | 说明                       |
|---------------------------------|--------|----------------------------|
| `|~ "(?i)error"`               | 200 OK | `(?i)` 是 RE2 inline flag |
| `|~ "(?i)192\.168\.33\.91"`    | 400    | `\.` 被 lexer 拒           |
| `|~ "(?i)192\\.168\\.33\\.91"` | 200 OK | 双重转义后 RE2 拿到 `\.` |
| `|~ "(?i)192.168.33.91"`       | 200 OK | 不转义：`.` 在 RE2 里是元字符但匹配任意单字符，绝大多数 IP 仍匹配 |

## 改动

### 1. 新增公共工具 `web/src/lib/loki.ts`

抽出 `escapeLokiLineFilter(s: string): string`，对所有要扔进 Loki 字符串字面量
里的 token 做双重转义：

```ts
export function escapeLokiLineFilter(s: string): string {
  return s
    .replace(/[\\.*+?^${}()|[\]]/g, '\\$&')  // 1) 给 RE2 元字符加 \
    .replace(/\\/g, '\\\\');                  // 2) 再给所有 \ 加 \ 给 lexer 剥一层
}
```

放进 `lib/` 而不是 pages/，因为 IncidentDetail 的 Grafana explore 链接构造也
需要它 —— 跨页面直接 import 页面组件会触发 React 模块求值。

### 2. `web/src/pages/Logs.tsx`

- 删掉文件里的 `reEscape`（功能等价但名字误导）。
- import `escapeLokiLineFilter` 替换 include/exclude 拼接里的 `reEscape`。
- 修 LOGS_QUICK_CHIPS 里的硬编码 query：
  - 第 3 项 `systemd[1]`：JS 字面 `systemd\\[1\\]` → `systemd\\\\[1\\\\]`
    （字符串值 2 个 `\` → Loki lexer 剥一层 → RE2 看到 `\[` 匹配字面 `[`）。
  - 第 4 项 `sshd?.service`：JS 字面 `sshd?\\.service` → `sshd?\\\\.service`。
- 在 chip 数组里加注释说明 JS 字面量转义层数。

### 3. `web/src/pages/IncidentDetail.tsx`

第 1256 行 Grafana explore URL 构造里直接拼 `lineFilter` 的地方也走
`escapeLokiLineFilter` —— 用户在 incident 里点开的 Grafana 链接要同样过这
关。

### 4. `internal/manager/server/aiops/query_translate.go`

dialectGuide 里 `logql` 段加 RE2 元字符双重转义规则说明 + 修正示例：

- 192.168.33.91 例子：`(?i)192\\.168\\.33\\.91`（raw string 里 6 个 `\` 字符
  + 3 个 `.`），LLM 看到这个形态照搬。
- ssh 例子：`{unit=~"sshd?\\.service"} |~ "(?i)(Failed|invalid)"`。
- 加注释说明 `(?i)` 是 RE2 inline flag，不需要转义，不要写成 `\?i`。

### 5. 验证程序 `f:/tmp/qtcheck/main.go`

镜像 prompt 里的 raw string，验证实际字节数。运行结果：

```
[1] 字符串 raw bytes（%q 形式）:
"\"主机 1 最近包含 192.168.33.91 的日志\" → {device_id=\"1\"} |~ \"(?i)192\\\\.168\\\\.33\\\\.91\""

[4] 提取出的 query: "(?i)192\\\\.168\\\\.33\\\\.91"
[5] query 中反斜杠数: 6
OK: 6 个反斜杠 = 3 对 \. — Loki lexer 会剥为 3 个 \. 给 RE2，匹配字面 192.168.33.91。
```

确认 query_translate.go 的 prompt 实际下发的字节里包含 6 个 `\`（LLM 照搬）。

## 备注

- 行为约束：遵守项目规则"代码改动要添加记录，单独在 .\record 中"。
- 不写单元测试（按规则"不需要写单元测试验证，从逻辑上验证通过、打包正常即
  可"）。
- 不在本地验证 .sh / Makefile（按规则"本地开发环境不需要验证.sh、make 文件
  是否错误"）。
- 视觉/UI 无变化：这次纯逻辑修复。
