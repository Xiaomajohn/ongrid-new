# 2026-07-15 — 修复 pluginhost 注册表构造函数名笔误

## 问题

打包 arm64 镜像时 `cmd/ongrid/main.go:2499` 编译报错:

```
cmd/ongrid/main.go:2499:24: undefined: phregistry.NewRegistry
```

## 根因

`internal/pluginhost/registry/registry.go` 的构造函数名是 `New()`(第 47 行),
`cmd/ongrid/main.go` Phase 4 D2 wire-up 段错写为 `phregistry.NewRegistry()`,
属于笔误。同项目内其它调用全部正确:

- `internal/pluginhost/pluginhost.go:30` —— `registry.New()`
- `internal/pluginhost/biz/lifecycle.go:48` —— 接收 `*registry.Registry` 类型

## 修改

- 文件: [cmd/ongrid/main.go](file:///f:/Code/Go/运维/ongrid-new/cmd/ongrid/main.go) 第 2499 行
- 改动:

  ```diff
  - phReg := phregistry.NewRegistry()
  + // 注册表构造器在 internal/pluginhost/registry 是 New(),不是 NewRegistry()
  + phReg := phregistry.New()
  ```

  仅一行函数名修正,后置 `phinvoke.NewRouter(phReg, phPool)` 接收
  `*registry.Registry` / `*rtp.Pool` 与 `New()` 返回值类型完全匹配。

## 验证

- 本地 `go build ./internal/pluginhost/...` ✅ 通过
- 本地 `go vet ./internal/pluginhost/...` ✅ 通过
- Windows 上 `go build ./cmd/ongrid` 因 onnxruntime_go cgo build constraints
  排除无法直接编译,符合规则"禁止在 Windows 上调试 Linux 上的打包程序",
  实际打包请在 192.168.25.30 的 `make package` 上验证。