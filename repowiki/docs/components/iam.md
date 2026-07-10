---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Service Definition"
  - "## Message Types"
  - "## File Layout"
  - "## Security Constraints"
---

# IAM 服务

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

`internal/iam/` 提供身份认证与权限管理，遵循与 Manager 一致的分层（server/service/biz/data/model）。

## Service Definition

API 定义：`api/iam/v1/iam.proto`，含 1 个 service + 22 个 message。

- `service IamService`[^12]
    - `rpc Register(...)`[^15]
    - `rpc Login(...)`[^19]
    - `rpc Refresh(...)`[^22]
    - `rpc GetSelf(...)`[^25]
    - `rpc CreateOrg(...)`[^28]
    - `rpc ListOrgs(...)`[^31]
    - `rpc InviteMember(...)`[^34]
    - `rpc ListMembers(...)`[^37]
    - `rpc SwitchOrg(...)`[^41]

## Message Types

- `message Org`[^47]
- `message User`[^57]
- `message Membership`[^64]
- `message TokenPair`[^75]
- `message RegisterRequest`[^84]
- `message RegisterResponse`[^89]
- `message LoginRequest`[^95]
- `message LoginResponse`[^100]
- `message RefreshRequest`[^107]
- `message RefreshResponse`[^111]
- `message GetSelfRequest`[^115]
- `message GetSelfResponse`[^117]
- `message CreateOrgRequest`[^122]
- `message CreateOrgResponse`[^127]
- `message ListOrgsRequest`[^133]

## File Layout

```
internal/iam/
  server/   ── HTTP / gRPC handler
  service/  ── 跨 biz 编排
  biz/      ── 业务用例
  data/     ── 仓储（MySQL）
  model/    ── 实体
```

## Security Constraints

- 密码使用 bcrypt / argon2id；禁止 MD5 / SHA1
- 多租户接口强制 `tenant_id` 过滤
- SQL 全部参数化；禁止字符串拼接
- 密钥禁止进代码 / 镜像 / 日志

<!-- END:REPO_WIKI_MANAGED -->

## 引用
[^12]: api/iam/v1/iam.proto L12–L42 — [api/iam/v1/iam.proto#L12-L42](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L12-L42)
[^15]: api/iam/v1/iam.proto L15–L25 — [api/iam/v1/iam.proto#L15-L25](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L15-L25)
[^19]: api/iam/v1/iam.proto L19–L29 — [api/iam/v1/iam.proto#L19-L29](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L19-L29)
[^22]: api/iam/v1/iam.proto L22–L32 — [api/iam/v1/iam.proto#L22-L32](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L22-L32)
[^25]: api/iam/v1/iam.proto L25–L35 — [api/iam/v1/iam.proto#L25-L35](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L25-L35)
[^28]: api/iam/v1/iam.proto L28–L38 — [api/iam/v1/iam.proto#L28-L38](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L28-L38)
[^31]: api/iam/v1/iam.proto L31–L41 — [api/iam/v1/iam.proto#L31-L41](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L31-L41)
[^34]: api/iam/v1/iam.proto L34–L44 — [api/iam/v1/iam.proto#L34-L44](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L34-L44)
[^37]: api/iam/v1/iam.proto L37–L47 — [api/iam/v1/iam.proto#L37-L47](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L37-L47)
[^41]: api/iam/v1/iam.proto L41–L51 — [api/iam/v1/iam.proto#L41-L51](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L41-L51)
[^47]: api/iam/v1/iam.proto L47–L77 — [api/iam/v1/iam.proto#L47-L77](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L47-L77)
[^57]: api/iam/v1/iam.proto L57–L87 — [api/iam/v1/iam.proto#L57-L87](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L57-L87)
[^64]: api/iam/v1/iam.proto L64–L94 — [api/iam/v1/iam.proto#L64-L94](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L64-L94)
[^75]: api/iam/v1/iam.proto L75–L105 — [api/iam/v1/iam.proto#L75-L105](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L75-L105)
[^84]: api/iam/v1/iam.proto L84–L114 — [api/iam/v1/iam.proto#L84-L114](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L84-L114)
[^89]: api/iam/v1/iam.proto L89–L119 — [api/iam/v1/iam.proto#L89-L119](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L89-L119)
[^95]: api/iam/v1/iam.proto L95–L125 — [api/iam/v1/iam.proto#L95-L125](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L95-L125)
[^100]: api/iam/v1/iam.proto L100–L130 — [api/iam/v1/iam.proto#L100-L130](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L100-L130)
[^107]: api/iam/v1/iam.proto L107–L137 — [api/iam/v1/iam.proto#L107-L137](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L107-L137)
[^111]: api/iam/v1/iam.proto L111–L141 — [api/iam/v1/iam.proto#L111-L141](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L111-L141)
[^115]: api/iam/v1/iam.proto L115–L145 — [api/iam/v1/iam.proto#L115-L145](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L115-L145)
[^117]: api/iam/v1/iam.proto L117–L147 — [api/iam/v1/iam.proto#L117-L147](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L117-L147)
[^122]: api/iam/v1/iam.proto L122–L152 — [api/iam/v1/iam.proto#L122-L152](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L122-L152)
[^127]: api/iam/v1/iam.proto L127–L157 — [api/iam/v1/iam.proto#L127-L157](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L127-L157)
[^133]: api/iam/v1/iam.proto L133–L163 — [api/iam/v1/iam.proto#L133-L163](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/iam/v1/iam.proto#L133-L163)