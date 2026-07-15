# 2026-07-15 · pluginhost Phase 5 E3:Asset Server(plugin 前端资源静态服务)

## 问题

Phase 4 后 server 端的 `internal/pluginhost/server/router.go` 已经能 list /
install / invoke,但 plugin 自带的前端产物(JS bundle / CSS / 图片)没有任何
HTTP 出口可以供给 web 端。Phase 5 E1 Sidebar 也只做到了"打开一个空
iframe / 直接挂 `<web-component-placeholder>`",因为没有 asset server,plugin
UI 一行代码都没法落到浏览器。

## 加了什么

### 1. 新文件:`internal/pluginhost/server/asset.go`(139 行)

`Handler.AssetPlugin(w, r)` 处理器,挂到 `GET /plugins/{id}/assets/{path...}`:

- 用 `parseIDParam` + `Reg.LookupByID(id)` 拿 instance;不存在 → 404
- 拼接 `path.Join(inst.InstallPath, assetPath)`,**不**用
  `filepath.Join`(避免 Windows `\` 在 URL 路径中的语义偏移)
- 用 `sandbox.PathSafeUnderRoot` 校验 fullPath 不越狱 inst.InstallPath
- 拒绝以 `.` 开头的隐藏文件与 `plugin.json`(不暴露 manifest 源文件)
- `http.ServeFile` 输出,自动处理 Last-Modified / ETag / Range / 404
- 加 `X-Plugin-Asset=true` 与 `X-Plugin-PackID=<id>` 响应头,便于 web 端调
  试时一眼分辨

错误码:

- 400:plugin id 不合法 / 路径越狱
- 403:试图访问元数据文件(`.compiled-manifest.json` / `.env` / `plugin.json`)
- 404:plugin 不存在 / 文件不存在
- 500:registry 没 wire-up / InstallPath 空

### 2. 修改:`internal/pluginhost/server/routes.go`

在 `r.Route("/{id}", ...)` 块内追加:

```go
r.Get("/assets/*", h.AssetPlugin)
```

与现有 / capabilities / invoke 路由并列。`/{id}` 内的子路由顺序:先已经
有的 / capabilities / invoke,后追加的 / assets——按 chi 文档,通配 `*` 匹
配优先级不影响同层级精确路径,所以顺序无关正确性。

### 3. 与 sandbox / manifest 的契约对齐

asset.go 用 `sandbox.PathSafeUnderRoot` 做越狱校验,Phase 2 已经实现的
sandbox.PathSafeUnderRoot 自然落点(plugin install 阶段就该做,不放在
asset 阶段做二次防御)。

manifest 层面,plugin 的 frontend spec(Format / Entry / ElementTag /
Schema)由 Phase 5 E4 负责消费,本 E3 阶段 asset server 只确保「能下到
任何相对路径的文件」,并不感知 frontend 元数据。这样 frontend spec 改
了也不需要重写 server,e3 不依赖 e4。

## 目的

Phase 5 E1、E2 把 sidebar / systemd 撤回问题解完了,但 plugin UI 落不到
浏览器这事没解。E3 是「让 plugin 前端 bundle 能被浏览器拉到」这一段
最短依赖路径。

不加这个,Phase 5 E4 的 pluginLoader 拉 entry JS 时只会拿到 404 或者
直接走 CDN,体验崩。

## 验证

```bash
$ go build ./internal/pluginhost/...
# 通过

$ curl -fsS http://192.168.25.30:8080/api/pluginhost/plugins/1/assets/js/main.js
# 200 + JS 内容(待 Phase 5 E4 + Phase 6 F demo 上线后真实跑一次)
```

## 后续

- Phase 5 E4 pluginLoader 接 `/{id}/assets/{entry}` 拿 JS,然后 blob URL
  + `<script>` 注入,等 `customElements.get(tagName)` 出现再挂到容器
- Phase 6 F 跑一遍 demo plugin 装到 192.168.25.56 上,确认 asset 路径真
  实可达(用 OnInstance.ServerURL 提供的 base)
- 不在本 E3 处理:Range 头 / 协商缓存(交给 http.ServeFile 默认行为;
  上线后看 access log,如果热 path 命中率低再补 SWR)

## 已知限制(P1 暂不解决)

- ETag / If-None-Match 中间层缓存:**不做**。`http.ServeFile` 自带的
  Last-Modified 已能满足开发期需要,生产 CDN 命中率留到 P2
- 鉴权:asset 与 invoke 共享同一条 chi mount,鉴权中间件按 plan §13 由
  manager server 统一处理;pluginhost 内部不重复上 token 校验
- 压缩:gzip / br 留给 nginx 反代层,asset server 不预压缩(避免下
  游 bracket pair 不匹配)
