# 修复 Hosts 列表页终端按钮一直禁用

## 问题
`/hosts` 列表页中，"终端"按钮在 `device.online=false` 时始终禁用。
但 Host 终端走的是「设备直连 SSH」语义（manager → 设备 IP），不依赖
edge agent 上报的 `device.online`——只要设备存在且账号有写权限，就
可以进入。

详情页 `HostDetail.tsx` 头部按钮已经按 `/shell-direct` 直连实现，列
表页仍按旧的 `/shell` + `!device.online` 判定，导致两页不一致、列表
按钮常态禁用。

## 改动
- 文件：`web/src/pages/Hosts.tsx`
- 组件：`ShellButton (host 视角)`
- 关键点：
  - 禁用条件由 `!canMutate || !device.online` 调整为
    `!canMutate || !device?.id`，不再依赖 `device.online`。
  - 跳转链接 `/hosts/:id/shell` → `/hosts/:id/shell-direct`，与
    `HostDetail.tsx` 头部按钮 / 路由表 `App.tsx` 保持一致，走设备直连
    transport（`webshell.ts` 看到 `/shell-direct` 走 direct 分支）。
  - 同步更新函数注释（说明这是设备直连语义）和 reason 文案（移除"设
    备未上线"，新增"设备 ID 缺失"）。

## 目的
让列表页"终端"按钮与详情页同语义：账号可写 + 设备 ID 注入即可进入终
端，不被 `device.online` 误伤；并把链接校正到 `shell-direct` 直连路
由，避免点击进入旧的 tunnel 路径。

## 影响范围
- 仅前端 UI：列表页 `ShellButton` 的禁用规则与跳转 href。
- 不改后端 API、不改路由表（`/shell-direct` 早已在 `App.tsx` 注册）。
- 不改 HostDetail 页面、其他按钮（Install / Files / Log / Edit）。