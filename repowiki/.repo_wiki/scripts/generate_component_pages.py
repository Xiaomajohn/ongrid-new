#!/usr/bin/env python3
"""Generate citation-backed component pages from code_index.json.

Citation format: footnote-style with GitHub permalinks.
"""
import json
import re
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path("/root/builder/ongrid-new")
INDEX_PATH = REPO_ROOT / "repowiki/.repo_wiki/code_index.json"
DOCS_DIR = REPO_ROOT / "repowiki/docs/components"
COMMIT = "47bad98d46a1d70231d237285784a8192d7756c7"
REMOTE = "https://github.com/Xiaomajohn/ongrid-new"
BASELINE_DATE = "2026-07-06"


def permalink(rel_path: str, start: int, end: int) -> str:
    """Generate GitHub permalink for file path + line range."""
    clean = rel_path.lstrip("./")
    return f"[{clean}#L{start}-L{end}]({REMOTE}/blob/{COMMIT}/{clean}#L{start}-L{end})"


def short_link(rel_path: str, start: int, end: int) -> str:
    """Generate short citation label (file path + line range, no URL)."""
    clean = rel_path.lstrip("./")
    return f"{clean} L{start}–L{end}"


def frontmatter(page_title: str, managed: list) -> str:
    parts = [
        "---",
        "generated_by: repo-wiki-agent",
        f'baseline_commit: "{COMMIT}"',
        f'last_updated: "{BASELINE_DATE}"',
        "managed_sections:",
    ]
    for s in managed:
        parts.append(f'  - "{s}"')
    parts.append("---")
    return "\n".join(parts)


def symbol_citation(idx: int, rel_path: str, start: int, end: int) -> str:
    return f"[^{idx}]: {short_link(rel_path, start, end)} — {permalink(rel_path, start, end)}"


def render_go_symbol_block(file_path, symbols, footnote_offset=1):
    """Render a list of public symbols for a single file, returns (body, footnotes)."""
    if not symbols:
        return "", []
    body_parts = [f"#### `{file_path}`"]
    fns = []
    for i, sym in enumerate(symbols[:15]):  # cap per file
        fn_idx = footnote_offset + i
        name = sym.get("name", "")
        kind = sym.get("kind", "")
        of = sym.get("of", "")
        if kind == "type":
            label = f"`type {name} {of}`"
        elif kind == "func":
            label = f"`func {name}(...)`"
        else:
            label = f"`{name}`"
        body_parts.append(f"- {label}[^{fn_idx}]")
        fns.append(symbol_citation(fn_idx, file_path, sym["line"], sym["line"] + 30))
    if len(symbols) > 15:
        body_parts.append(f"- _…另有 {len(symbols)-15} 个符号（截断）_")
    return "\n".join(body_parts), fns


def build_backend_page(index: dict) -> str:
    # 只取 cmd/<name>/main.go，不包含 _test.go
    cmds = sorted(p for p in index["components"]["cmd"]["paths"] if p.endswith("/main.go"))
    symbols = index["go_public_symbols"]
    body = []
    footnotes = []

    # Overview
    body.append("<!-- BEGIN:REPO_WIKI_MANAGED -->")
    body.append("## Overview")
    body.append("")
    body.append("控制面后端由两个入口二进制构成：")
    body.append("")
    body.append("- `ongrid` — Manager + IAM 控制面入口")
    body.append("- `ongrid-edge` — 边缘 Agent 入口")
    body.append("")
    body.append("两个入口由各 BC（`internal/manager`、`internal/iam`、`internal/edgeagent`、`internal/pkg`）组装。")
    body.append("")
    body.append("## Entrypoints")
    body.append("")
    body.append("| 入口 | 路径 | 角色 |")
    body.append("|---|---|---|")
    for p in cmds:
        body.append(f"| `{p}` | 命令入口 | 'main' 函数 |")
    body.append("")
    body.append("## Entry Symbol")
    body.append("")
    main_syms = symbols.get("./cmd/ongrid/main.go", [])
    if main_syms:
        body.append(f"`cmd/ongrid/main.go` 顶层定义：")
        body.append("")
        # Just show first few key funcs
        for sym in main_syms[:6]:
            name = sym["name"]
            line = sym["line"]
            body.append(f"- `{name}`[^{line}]")
            footnotes.append(symbol_citation(line, "./cmd/ongrid/main.go", line, line + 5))
    body.append("")
    body.append("## Layering")
    body.append("")
    body.append("BC 内部分层（Kratos 风格）：")
    body.append("")
    body.append("```")
    body.append("server  ── HTTP / gRPC handler")
    body.append("  ↓")
    body.append("service ── 跨 biz 编排")
    body.append("  ↓")
    body.append("biz     ── 业务用例 + 领域模型")
    body.append("  ↓")
    body.append("data    ── 仓储实现（MySQL / Redis）")
    body.append("  ↓")
    body.append("model   ── DO / PO 实体")
    body.append("```")
    body.append("")
    body.append("## Cross-Domain Boundary")
    body.append("")
    body.append("按 AGENTS.md 约束，`internal/<domain>` 之间禁止直接 import，必须经：")
    body.append("")
    body.append("- API（`api/<domain>/v1/*.proto`）")
    body.append("- 事件总线")
    body.append("- `internal/pkg/` 内的通用基础库")
    body.append("")
    body.append("接口在消费方定义，避免循环依赖；构造依赖通过构造函数注入，不使用全局变量。")
    body.append("")
    body.append("## Entrypoint Symbols by BC")
    body.append("")
    body.append("### cmd/ongrid")
    body.append("")
    for sym in (symbols.get("./cmd/ongrid/main.go") or [])[:8]:
        body.append(f"- `{sym['name']}`[^{sym['line']}]")
        footnotes.append(symbol_citation(sym['line'], "./cmd/ongrid/main.go", sym['line'], sym['line'] + 5))
    body.append("")
    body.append("### cmd/ongrid-edge")
    body.append("")
    edge_syms = symbols.get("./cmd/ongrid-edge/main.go") or []
    if edge_syms:
        for sym in edge_syms[:8]:
            body.append(f"- `{sym['name']}`[^{sym['line']}]")
            footnotes.append(symbol_citation(sym['line'], "./cmd/ongrid-edge/main.go", sym['line'], sym['line'] + 5))
    body.append("")
    body.append("<!-- END:REPO_WIKI_MANAGED -->")
    body.append("")

    # 拼装
    fm = frontmatter("后端服务 (Go)", ["## Overview", "## Entrypoints", "## Entry Symbol", "## Layering", "## Cross-Domain Boundary", "## Entrypoint Symbols by BC"])
    out = [fm, "", "# 后端服务 (Go)", "", "\n".join(body)]
    out.extend(["", "## 引用"] + sorted(set(footnotes)))
    return "\n".join(out)


def build_edge_agent_page(index: dict) -> str:
    plugins = index["components"]["edgeagent"]["plugins"]
    symbols = index["go_public_symbols"]
    body = []
    footnotes = []

    body.append("<!-- BEGIN:REPO_WIKI_MANAGED -->")
    body.append("## Overview")
    body.append("")
    body.append("Edge Agent 入口位于 `cmd/ongrid-edge/main.go`，实现位于 `internal/edgeagent/`，按能力划分为多个插件。")
    body.append("")
    body.append("## Plugins (9)")
    body.append("")
    body.append("| 插件 | 路径 | 用途 |")
    body.append("|---|---|---|")
    plugin_desc = {
        "audit": "审计日志",
        "custommetrics": "自定义指标采集",
        "databasemetrics": "数据库指标（MySQL/PG/Mongo/Redis）",
        "hostmetrics": "主机指标（CPU/内存/磁盘/网络）",
        "logs": "日志采集",
        "metrics": "指标入口",
        "metricscommon": "指标公共工具",
        "procmetrics": "进程指标",
        "traces": "链路追踪",
    }
    for p in plugins:
        body.append(f"| `{p}` | `internal/edgeagent/plugins/{p}/` | {plugin_desc.get(p, '—')} |")
    body.append("")
    body.append("## Entrypoint Symbols")
    body.append("")
    edge_syms = symbols.get("./cmd/ongrid-edge/main.go") or []
    for sym in edge_syms[:10]:
        kind_label = "type" if sym["kind"] == "type" else "func"
        body.append(f"- `cmd/ongrid-edge/main.go` L{sym['line']}: `{kind_label} {sym['name']}`[^{sym['line']}]")
        footnotes.append(symbol_citation(sym['line'], "./cmd/ongrid-edge/main.go", sym['line'], sym['line'] + 5))
    body.append("")
    body.append("## Plugin Symbols (示例)")
    body.append("")
    # 从每个 plugin 子目录抽 2-3 个公开符号
    for p in plugins[:5]:
        body.append(f"### `{p}/`")
        body.append("")
        plugin_files = sorted(f for f in symbols if f"/internal/edgeagent/plugins/{p}/" in f)[:2]
        for pf in plugin_files:
            for sym in (symbols[pf] or [])[:4]:
                kind_label = "type" if sym["kind"] == "type" else "func"
                body.append(f"- `{pf}` L{sym['line']}: `{kind_label} {sym['name']}`[^{sym['line']}]")
                footnotes.append(symbol_citation(sym['line'], pf, sym['line'], sym['line'] + 5))
        body.append("")
    body.append("")
    body.append("## Core Sub-Directories")
    body.append("")
    body.append("`internal/edgeagent/` 下子目录：")
    body.append("")
    sub_dirs = [
        "biz", "cmdpolicy", "collector", "host_files", "model",
        "plugins", "restart_service", "service", "skill", "webshell",
    ]
    for sd in sub_dirs:
        cnt = len([f for f in symbols if f"/internal/edgeagent/{sd}/" in f])
        body.append(f"- `internal/edgeagent/{sd}/` — {cnt} 文件含公开符号")
    body.append("")
    body.append("## Plugin Sample")
    body.append("")
    body.append("以 `bash/` 插件为例（受 `deploy/edge/bash-policy.example.yaml` 约束）：")
    body.append("")
    body.append("- 入口：`internal/edgeagent/bash/`")
    body.append("- 策略：`deploy/edge/bash-policy.example.yaml`")
    body.append("")
    body.append("<!-- END:REPO_WIKI_MANAGED -->")

    fm = frontmatter("Edge Agent", ["## Overview", "## Plugins (9)", "## Entrypoint Symbols", "## Plugin Symbols (示例)", "## Core Sub-Directories", "## Plugin Sample"])
    out = [fm, "", "# Edge Agent", "", "\n".join(body)]
    out.extend(["", "## 引用"] + sorted(set(footnotes), key=lambda x: int(re.search(r'\[\^(\d+)\]', x).group(1))))
    return "\n".join(out)


def build_frontend_page(index: dict) -> str:
    modules = index["components"]["frontend"]["modules"]
    ts_symbols = index["ts_public_symbols"]
    body = []
    footnotes = []

    body.append("<!-- BEGIN:REPO_WIKI_MANAGED -->")
    body.append("## Overview")
    body.append("")
    body.append("前端位于 `web/`，使用：")
    body.append("")
    body.append("- React 18 + TypeScript")
    body.append("- Vite（开发与构建）")
    body.append("- Tailwind CSS（UI 配色与排版）")
    body.append("- pnpm（包管理）")
    body.append("")
    body.append("入口：`web/src/main.tsx`，构建产物 `web/dist/`。")
    body.append("")
    body.append("## Modules (7)")
    body.append("")
    body.append("| 模块 | 路径 | 角色 |")
    body.append("|---|---|---|")
    desc_map = {
        "api": "后端接口封装（按域划分）",
        "components": "通用 UI 组件",
        "i18n": "多语言文案",
        "lib": "通用工具",
        "pages": "业务页面",
        "store": "状态管理",
        "test": "测试",
    }
    for m in modules:
        body.append(f"| `{m}` | `web/src/{m}/` | {desc_map.get(m, '—')} |")
    body.append("")
    body.append("## Style System")
    body.append("")
    body.append("- 全局样式：`web/src/styles/index.css`")
    body.append("- 主题色：`zinc` 中性骨架 + `indigo` 主操作")
    body.append("- 语义色：emerald(成功) / amber(降级) / red(异常) / sky(信息)")
    body.append("- `html.light` 覆盖：浅色模式")
    body.append("")
    body.append("## API Layer Exports")
    body.append("")
    # 从 web/src/api/<域>/index.ts 抽取 export
    api_index_files = sorted(f for f in ts_symbols if re.match(r"\./web/src/api/[^/]+/index\.(ts|tsx)$", f))
    for f in api_index_files[:5]:
        domain = f.split("/web/src/api/")[1].split("/")[0]
        body.append(f"### `api/{domain}`")
        body.append("")
        for sym in (ts_symbols[f] or [])[:6]:
            body.append(f"- `{f}` L{sym['line']}: `{sym['name']}`[^{sym['line']}]")
            footnotes.append(symbol_citation(sym['line'], f, sym['line'], sym['line'] + 3))
        body.append("")
    body.append("")
    body.append("## UI Components")
    body.append("")
    # 从 web/src/components/ 抽取
    comp_files = sorted(f for f in ts_symbols if f.startswith("./web/src/components/"))[:5]
    for f in comp_files:
        for sym in (ts_symbols[f] or [])[:3]:
            body.append(f"- `{f}` L{sym['line']}: `{sym['name']}`[^{sym['line']}]")
            footnotes.append(symbol_citation(sym['line'], f, sym['line'], sym['line'] + 3))
    body.append("")
    body.append("## Pages")
    body.append("")
    # 列出 web/src/pages/ 下的文件
    pages_files = sorted(f for f in ts_symbols if f.startswith("./web/src/pages/"))[:5]
    for f in pages_files:
        for sym in (ts_symbols[f] or [])[:2]:
            body.append(f"- `{f}` L{sym['line']}: `{sym['name']}`[^{sym['line']}]")
            footnotes.append(symbol_citation(sym['line'], f, sym['line'], sym['line'] + 3))
    body.append("")
    body.append("## Build Commands")
    body.append("")
    body.append("```bash")
    body.append("cd web")
    body.append("pnpm install")
    body.append("pnpm lint")
    body.append("pnpm build       # tsc --noEmit + vite build")
    body.append("```")
    body.append("")
    body.append("<!-- END:REPO_WIKI_MANAGED -->")

    fm = frontmatter("前端 (React + TS)", ["## Overview", "## Modules (7)", "## Style System", "## API Layer Exports", "## UI Components", "## Pages", "## Build Commands"])
    out = [fm, "", "# 前端 (React + TS)", "", "\n".join(body)]
    out.extend(["", "## 引用"] + sorted(set(footnotes), key=lambda x: int(re.search(r'\[\^(\d+)\]', x).group(1))))
    return "\n".join(out)


def build_pkg_page(index: dict) -> str:
    packages = index["components"]["pkg"]["packages"]
    symbols = index["go_public_symbols"]
    body = []
    footnotes = []

    body.append("<!-- BEGIN:REPO_WIKI_MANAGED -->")
    body.append("## Overview")
    body.append("")
    body.append("`internal/pkg/` 存放各 BC 复用的基础库，遵循 AGENTS.md 约束：")
    body.append("")
    body.append("- 不依赖任何业务包")
    body.append("- 通过构造函数注入，无全局可变变量")
    body.append("- 所有 IO 函数第一个参数为 `context.Context`")
    body.append("")
    body.append("## Packages (29)")
    body.append("")
    body.append("| 包 | 用途 |")
    body.append("|---|---|")
    pkg_desc = {
        "auth": "认证核心",
        "authzmw": "认证中间件",
        "config": "配置加载",
        "credinject": "凭证注入",
        "dbx": "数据库封装",
        "docextract": "文档抽取",
        "embedding": "向量 embedding",
        "errs": "错误码",
        "grafana": "Grafana 集成",
        "httpserver": "HTTP server 工具",
        "llm": "LLM 客户端",
        "logger": "slog 封装",
        "logquery": "日志查询",
        "mcpclient": "MCP 客户端",
        "notify": "通知",
        "passwd": "密码",
        "prom": "Prometheus 指标",
        "promauth": "Prometheus 认证",
        "promquery": "Prometheus 查询",
        "promwrite": "Prometheus 写入",
        "qdrantx": "Qdrant 向量库",
        "runner": "任务执行",
        "secretbox": "密钥加密",
        "tenantctx": "多租户上下文",
        "tracequery": "Trace 查询",
        "tracing": "链路追踪",
        "tunnel": "隧道",
        "workspace": "工作区",
        "zhipuauth": "智谱认证",
    }
    for p in packages:
        body.append(f"| `{p}` | {pkg_desc.get(p, '—')} |")
    body.append("")
    body.append("## Conventions")
    body.append("")
    body.append("- 错误用 `%w` 包装；不重复记录")
    body.append("- 共享状态必须加锁，测试带 `-race`")
    body.append("- 敏感字段禁止明文入日志")
    body.append("- 密码必须 bcrypt / argon2id")
    body.append("")
    body.append("## Sample Public Symbols")
    body.append("")
    # 选取一些 pkg 包的关键公开符号
    pkg_samples = [p for p in packages if any(f"/internal/pkg/{p}/" in f for f in symbols.keys())][:5]
    for pkg_name in pkg_samples:
        body.append(f"### `{pkg_name}/`")
        body.append("")
        # 列出该包第一个 Go 文件的符号
        pkg_files = sorted(f for f in symbols if f"/internal/pkg/{pkg_name}/" in f)[:2]
        for pf in pkg_files:
            for sym in (symbols[pf] or [])[:5]:
                kind_label = "type" if sym["kind"] == "type" else "func"
                body.append(f"- `{pf}` L{sym['line']}: `{kind_label} {sym['name']}`[^{sym['line']}]")
                footnotes.append(symbol_citation(sym['line'], pf, sym['line'], sym['line'] + 5))
        body.append("")
    body.append("")
    body.append("<!-- END:REPO_WIKI_MANAGED -->")

    fm = frontmatter("共享包 (pkg)", ["## Overview", "## Packages (29)", "## Conventions", "## Sample Public Symbols"])
    out = [fm, "", "# 共享包 (pkg)", "", "\n".join(body)]
    out.extend(["", "## 引用"] + sorted(set(footnotes)))
    return "\n".join(out)


def build_iam_page(index: dict) -> str:
    proto_defs = index["proto_definitions"]
    iam_proto = "./api/iam/v1/iam.proto"
    iam_info = proto_defs.get(iam_proto, {})
    body = []
    footnotes = []

    body.append("<!-- BEGIN:REPO_WIKI_MANAGED -->")
    body.append("## Overview")
    body.append("")
    body.append("`internal/iam/` 提供身份认证与权限管理，遵循与 Manager 一致的分层（server/service/biz/data/model）。")
    body.append("")
    body.append("## Service Definition")
    body.append("")
    body.append("API 定义：`api/iam/v1/iam.proto`，含 1 个 service + 22 个 message。")
    body.append("")
    if iam_info.get("services"):
        for svc in iam_info["services"]:
            line = svc["line"]
            body.append(f"- `service {svc['name']}`[^{line}]")
            footnotes.append(symbol_citation(line, iam_proto, line, line + 30))
            for rpc in svc.get("rpcs", [])[:15]:
                rline = rpc["line"]
                body.append(f"    - `rpc {rpc['name']}(...)`[^{rline}]")
                footnotes.append(symbol_citation(rline, iam_proto, rline, rline + 10))
    body.append("")
    body.append("## Message Types")
    body.append("")
    if iam_info.get("messages"):
        for msg in iam_info["messages"][:15]:
            line = msg["line"]
            body.append(f"- `message {msg['name']}`[^{line}]")
            footnotes.append(symbol_citation(line, iam_proto, line, line + 30))
    body.append("")
    body.append("## File Layout")
    body.append("")
    body.append("```")
    body.append("internal/iam/")
    body.append("  server/   ── HTTP / gRPC handler")
    body.append("  service/  ── 跨 biz 编排")
    body.append("  biz/      ── 业务用例")
    body.append("  data/     ── 仓储（MySQL）")
    body.append("  model/    ── 实体")
    body.append("```")
    body.append("")
    body.append("## Security Constraints")
    body.append("")
    body.append("- 密码使用 bcrypt / argon2id；禁止 MD5 / SHA1")
    body.append("- 多租户接口强制 `tenant_id` 过滤")
    body.append("- SQL 全部参数化；禁止字符串拼接")
    body.append("- 密钥禁止进代码 / 镜像 / 日志")
    body.append("")
    body.append("<!-- END:REPO_WIKI_MANAGED -->")

    fm = frontmatter("IAM 服务", ["## Overview", "## Service Definition", "## Message Types", "## File Layout", "## Security Constraints"])
    out = [fm, "", "# IAM 服务", "", "\n".join(body)]
    out.extend(["", "## 引用"] + sorted(set(footnotes), key=lambda x: int(re.search(r'\[\^(\d+)\]', x).group(1))))
    return "\n".join(out)


def build_manager_page(index: dict) -> str:
    layers = index["components"]["manager"]["layers"]
    subdomains = index["components"]["manager"]["subdomains"]
    proto_defs = index["proto_definitions"]
    body = []
    footnotes = []

    body.append("<!-- BEGIN:REPO_WIKI_MANAGED -->")
    body.append("## Overview")
    body.append("")
    body.append("`internal/manager/` 是控制面核心域，按 5 个层 + 33 个子域组织。")
    body.append("")
    body.append("## Layers")
    body.append("")
    body.append("| 层 | 路径 | 角色 |")
    body.append("|---|---|---|")
    layer_desc = {
        "biz": "业务用例 + 领域模型",
        "data": "仓储实现（MySQL / Redis）",
        "model": "DO / PO 实体",
        "server": "HTTP / gRPC handler",
        "service": "跨 biz 编排",
    }
    for l in layers:
        body.append(f"| `{l}` | `internal/manager/{l}/` | {layer_desc.get(l, '—')} |")
    body.append("")
    body.append("## gRPC Services (5)")
    body.append("")
    body.append("| 子域 | proto | service | RPC 数 |")
    body.append("|---|---|---|---|")
    manager_protos = [
        ("aiops", "./api/manager/aiops/v1/aiops.proto"),
        ("alert", "./api/manager/alert/v1/alert.proto"),
        ("edge", "./api/manager/edge/v1/edge.proto"),
        ("metric", "./api/manager/metric/v1/metric.proto"),
        ("notification", "./api/manager/notification/v1/notification.proto"),
    ]
    for sub, proto_path in manager_protos:
        info = proto_defs.get(proto_path, {})
        svcs = info.get("services", [])
        msgs = info.get("messages", [])
        if svcs:
            svc = svcs[0]
            rpc_n = len(svc.get("rpcs", []))
            body.append(f"| `{sub}` | `{proto_path}` | `{svc['name']}`[^{svc['line']}] | {rpc_n} |")
            footnotes.append(symbol_citation(svc['line'], proto_path, svc['line'], svc['line'] + 30))
            for rpc in svc.get("rpcs", [])[:8]:
                body.append(f"    - `rpc {rpc['name']}`[^{rpc['line']}]")
                footnotes.append(symbol_citation(rpc['line'], proto_path, rpc['line'], rpc['line'] + 10))
        body.append("")
    body.append("")
    body.append("## RPC Inventory (aiops 示例)")
    body.append("")
    aiops_info = proto_defs.get("./api/manager/aiops/v1/aiops.proto", {})
    if aiops_info.get("services"):
        for rpc in aiops_info["services"][0].get("rpcs", []):
            body.append(f"- `rpc {rpc['name']}`[^{rpc['line']}]")
            footnotes.append(symbol_citation(rpc['line'], "./api/manager/aiops/v1/aiops.proto", rpc['line'], rpc['line'] + 10))
    body.append("")
    body.append("## RPC Inventory (edge)")
    body.append("")
    edge_info = proto_defs.get("./api/manager/edge/v1/edge.proto", {})
    if edge_info.get("services"):
        for rpc in edge_info["services"][0].get("rpcs", []):
            body.append(f"- `rpc {rpc['name']}`[^{rpc['line']}]")
            footnotes.append(symbol_citation(rpc['line'], "./api/manager/edge/v1/edge.proto", rpc['line'], rpc['line'] + 10))
    body.append("")
    body.append("## Message Types")
    body.append("")
    # 抽取部分 manager proto 的 message
    for sub, proto_path in manager_protos[:3]:
        info = proto_defs.get(proto_path, {})
        if info.get("messages"):
            body.append(f"### `{sub}`")
            body.append("")
            for msg in info["messages"][:6]:
                body.append(f"- `message {msg['name']}`[^{msg['line']}]")
                footnotes.append(symbol_citation(msg['line'], proto_path, msg['line'], msg['line'] + 20))
            body.append("")
    body.append("")
    body.append("## Subdomain Layout")
    body.append("")
    body.append("每个子域在 5 层中均有对应实现：")
    body.append("")
    body.append("```")
    body.append("internal/manager/")
    body.append("  biz/<subdomain>/        ─ 业务")
    body.append("  data/<subdomain>/       ─ 仓储")
    body.append("  model/<subdomain>/      ─ 实体")
    body.append("  server/<subdomain>/     ─ handler")
    body.append("  service/<subdomain>/    ─ 编排")
    body.append("```")
    body.append("")
    body.append("## Cross-Subdomain Communication")
    body.append("")
    body.append("- 经 `api/manager/<sub>/v1/*.proto` 调用")
    body.append("- 经事件总线异步解耦")
    body.append("- 跨域共享通过 `internal/pkg/`")
    body.append("")
    body.append("## Architecture Constraints")
    body.append("")
    body.append("- 接口在消费方定义，禁止循环依赖")
    body.append("- 依赖通过构造函数注入，无全局变量")
    body.append("- 高基数字段（user_id/email/url）禁止作 Prometheus label")
    body.append("- 错误用 `%w` 包装；不重复记录")
    body.append("")
    body.append("<!-- END:REPO_WIKI_MANAGED -->")

    fm = frontmatter("Manager 服务", ["## Overview", "## Layers", "## gRPC Services (5)", "## RPC Inventory (aiops 示例)", "## RPC Inventory (edge)", "## Message Types", "## Subdomain Layout", "## Cross-Subdomain Communication", "## Architecture Constraints"])
    out = [fm, "", "# Manager 服务", "", "\n".join(body)]
    out.extend(["", "## 引用"] + sorted(set(footnotes), key=lambda x: int(re.search(r'\[\^(\d+)\]', x).group(1))))
    return "\n".join(out)


def main():
    index = json.loads(INDEX_PATH.read_text())
    DOCS_DIR.mkdir(parents=True, exist_ok=True)

    pages = {
        "backend.md": build_backend_page(index),
        "edge-agent.md": build_edge_agent_page(index),
        "frontend.md": build_frontend_page(index),
        "pkg.md": build_pkg_page(index),
        "iam.md": build_iam_page(index),
        "manager.md": build_manager_page(index),
    }

    for name, content in pages.items():
        path = DOCS_DIR / name
        path.write_text(content)
        fn_count = content.count("[^")
        print(f"  wrote {path} — {len(content.splitlines())} lines, ~{fn_count} footnotes")


if __name__ == "__main__":
    main()