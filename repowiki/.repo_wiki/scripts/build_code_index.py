#!/usr/bin/env python3
"""Generate code_index.json by scanning Go / TS / Proto files for public symbols."""
import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path("/root/builder/ongrid-new")
OUT_FILE = REPO_ROOT / "repowiki/.repo_wiki/code_index.json"
COMMIT = "47bad98d46a1d70231d237285784a8192d7756c7"

# Regex patterns
GO_FUNC = re.compile(r"^func\s+(?:\([^)]*\)\s+)?([A-Za-z_][A-Za-z0-9_]*)")
GO_TYPE = re.compile(r"^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(struct|interface|[*]?[A-Za-z_][A-Za-z0-9_]*)\s*\{?")
GO_VAR = re.compile(r"^var\s+([A-Za-z_][A-Za-z0-9_]*)")
GO_CONST = re.compile(r"^const\s+(?:\(?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=)?")
TS_EXPORT = re.compile(r"^export\s+(?:default\s+)?(?:async\s+)?(?:function|class|const|let|var|interface|type|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)")
PROTO_RPC = re.compile(r"^\s*rpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")
PROTO_SERVICE = re.compile(r"^service\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{")
PROTO_MESSAGE = re.compile(r"^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{")


def scan_go(rel_path: str) -> dict:
    full = REPO_ROOT / rel_path
    try:
        text = full.read_text(errors="ignore")
    except Exception as e:
        return {"path": rel_path, "error": str(e)}
    symbols = []
    for i, line in enumerate(text.splitlines(), 1):
        m = GO_FUNC.match(line)
        if m and not line.startswith("func ("):
            symbols.append({"line": i, "kind": "func", "name": m.group(1)})
            continue
        m = GO_TYPE.match(line)
        if m:
            symbols.append({"line": i, "kind": "type", "name": m.group(1), "of": m.group(2)})
            continue
    return {"path": rel_path, "symbols": symbols}


def scan_ts(rel_path: str) -> dict:
    full = REPO_ROOT / rel_path
    try:
        text = full.read_text(errors="ignore")
    except Exception as e:
        return {"path": rel_path, "error": str(e)}
    symbols = []
    for i, line in enumerate(text.splitlines(), 1):
        m = TS_EXPORT.match(line)
        if m:
            symbols.append({"line": i, "kind": "export", "name": m.group(1)})
    return {"path": rel_path, "symbols": symbols}


def scan_proto(rel_path: str) -> dict:
    full = REPO_ROOT / rel_path
    try:
        text = full.read_text(errors="ignore")
    except Exception as e:
        return {"path": rel_path, "error": str(e)}
    services = []
    current_service = None
    messages = []
    for i, line in enumerate(text.splitlines(), 1):
        m = PROTO_SERVICE.match(line)
        if m:
            current_service = {"name": m.group(1), "line": i, "rpcs": []}
            services.append(current_service)
            continue
        m = PROTO_RPC.match(line)
        if m and current_service:
            current_service["rpcs"].append({"name": m.group(1), "line": i})
            continue
        m = PROTO_MESSAGE.match(line)
        if m:
            messages.append({"name": m.group(1), "line": i})
    return {"path": rel_path, "services": services, "messages": messages}


def main():
    file_list = (REPO_ROOT / "repowiki/.repo_wiki/file_list.txt").read_text().splitlines()

    go_files = [f for f in file_list if f.endswith(".go")]
    ts_files = [f for f in file_list if f.endswith((".ts", ".tsx"))]
    proto_files = [f for f in file_list if f.endswith(".proto")]

    index = {
        "schema_version": "1.0",
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "baseline_commit": COMMIT,
        "repo_remote_url": "https://github.com/Xiaomajohn/ongrid-new.git",
        "totals": {
            "files_total": len(file_list),
            "go_files": len(go_files),
            "ts_files": len(ts_files),
            "proto_files": len(proto_files),
        },
        "components": {
            "cmd": {"paths": [f for f in go_files if f.startswith("./cmd/")]},
            "manager": {
                "paths": [f for f in go_files if "/internal/manager/" in f],
                # manager 按 biz|data|model|server|service 分层，每层下分子域
                "layers": sorted(set(
                    f.split("/internal/manager/")[1].split("/")[0]
                    for f in go_files if "/internal/manager/" in f
                )),
                # 子域是 manager/<layer>/<subdomain>/ 下的第二层
                "subdomains": sorted(set(
                    f.split("/internal/manager/")[1].split("/")[1]
                    for f in go_files if "/internal/manager/" in f
                    and len(f.split("/internal/manager/")[1].split("/")) > 1
                )),
            },
            "iam": {
                "paths": [f for f in go_files if "/internal/iam/" in f],
                "subdomains": sorted(set(
                    f.split("/internal/iam/")[1].split("/")[0]
                    for f in go_files if "/internal/iam/" in f
                )),
            },
            "edgeagent": {
                "paths": [f for f in go_files if "/internal/edgeagent/" in f],
                # plugins 是 /internal/edgeagent/plugins/<plugin_name>/... 下的子目录
                "plugins": sorted(set(
                    f.split("/internal/edgeagent/plugins/")[1].split("/")[0]
                    for f in go_files if "/internal/edgeagent/plugins/" in f
                    and len(f.split("/internal/edgeagent/plugins/")[1].split("/")) > 1
                )),
            },
            "pkg": {
                "packages": sorted(set(
                    f.split("/internal/pkg/")[1].split("/")[0]
                    for f in go_files if "/internal/pkg/" in f
                )),
            },
            "frontend": {
                "paths": ts_files,
                # 模块是 /web/src/<module>/ 下的子目录
                "modules": sorted(set(
                    f.split("/web/src/")[1].split("/")[0]
                    for f in ts_files if "/web/src/" in f
                    and len(f.split("/web/src/")[1].split("/")) > 1
                )),
            },
            "api": {"proto_files": proto_files},
        },
        "go_public_symbols": {},
        "ts_public_symbols": {},
        "proto_definitions": {},
    }

    for f in go_files:
        idx = scan_go(f)
        if idx.get("symbols"):
            index["go_public_symbols"][f] = idx["symbols"]

    for f in ts_files:
        idx = scan_ts(f)
        if idx.get("symbols"):
            index["ts_public_symbols"][f] = idx["symbols"]

    for f in proto_files:
        idx = scan_proto(f)
        index["proto_definitions"][f] = {
            "services": idx.get("services", []),
            "messages": idx.get("messages", []),
        }

    OUT_FILE.parent.mkdir(parents=True, exist_ok=True)
    OUT_FILE.write_text(json.dumps(index, indent=2, ensure_ascii=False))

    print(f"Wrote {OUT_FILE}")
    print(f"  total files: {index['totals']['files_total']}")
    print(f"  go files with public symbols: {len(index['go_public_symbols'])}")
    print(f"  ts files with exports: {len(index['ts_public_symbols'])}")
    print(f"  proto files: {len(index['proto_definitions'])}")
    print(f"  proto services: {sum(len(p['services']) for p in index['proto_definitions'].values())}")
    print(f"  proto messages: {sum(len(p['messages']) for p in index['proto_definitions'].values())}")


if __name__ == "__main__":
    main()