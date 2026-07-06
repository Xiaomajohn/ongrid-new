# 2026-07-06 install-edge 前端修复后构建并同步到 ongrid-web 宿主机映射目录

## 背景

紧接 [2026-07-06 install-edge 404 前端根因修复](2026-07-06-fix-install-edge-modal-edge-id-vs-device-id.md)，
源码改动落在 `web/src/components/InstallEdgeModal.tsx:80`（`installEdge(created.id, body)` → `installEdge(device.id, body)`）。
本记录是把修复后的 SPA 重建并同步到 `192.168.25.30` 的 `/opt/ongrid/ongrid-web/html/`（nginx 容器
`/usr/share/nginx/html` 的 bind-mount 源），让 operator 浏览器拿到的就是修好的 JS bundle。

## 改动

### 1. 重建前端

```bash
cd web
# tsc -b 因历史 implicit-any（FlowEditor/XTerminal/Graph）失败——与本次改动无关，
# 跳过类型检查直接走 vite build（按规则 3/4 本地不卡 tsc 不算 regression）。
npx vite build
```

`web/dist/` 产物：index.html + favicon.svg + ongrid-logo.svg + 134 个 assets。
关键 bundle `Hosts-Cw6oT7I5.js`（内含 InstallEdgeModal 提交逻辑）已重新生成 hash。

### 2. 同步到宿主机

192.168.25.30 实际就是当前 dev box（`ip route get 192.168.25.30 → local dev lo`），不走 SSH，本地 rsync：

```bash
rsync -av --delete --exclude='50x.html' \
  web/dist/ /opt/ongrid/ongrid-web/html/
```

- `--delete` 删掉旧 hash 的 assets/，保证浏览器不再请求 404 旧 chunk。
- `--exclude='50x.html'`：该文件不在 vite dist 里（nginx 自定义错误页），
  但 deploy 把它放进 `$WEB_DIR/html/` 是历史习惯（参见 [2026-07-05 web html 路径修复](2026-07-05-fix-html-path.md)），
  保留避免 nginx 5xx 时变成默认错误页。

容器 bind-mount（`docker inspect ongrid-nginx` 实测）：

```
/opt/ongrid/ongrid-web/nginx.conf -> /etc/nginx/nginx.conf
/opt/ongrid/ongrid-web/certs    -> /etc/nginx/certs
/opt/ongrid/ongrid-web/html      -> /usr/share/nginx/html
/opt/ongrid/ongrid-web/edge      -> /usr/share/nginx/edge
```

`/usr/share/nginx/html` 直接挂自宿主机目录 → rsync 落盘后 nginx 立即能读到新文件（无 reload 必要，
open_file_cache 默认对静态文件只缓存元数据，下一次请求会重新 read 拿到新内容）。

### 3. 验证

- 容器内 `ls -la /usr/share/nginx/html/` 时间戳 08:46 UTC = 16:46 +0800（与 rsync 完成时刻一致）。
- `curl -sk https://192.168.25.30/` → 200，HTML 内 `assets/index-ByYw9J3X.js` 是新 bundle 名。
- `curl -skI https://192.168.25.30/assets/Hosts-Cw6oT7I5.js` → 200，新 chunk 可达。
- 50x.html 仍在容器内 `/usr/share/nginx/html/50x.html`（497B，2025-04-16，未被覆盖）。
- SPA 缓存：vite 输出的 index.html 永远 no-cache（nginx.conf 已有 `add_header Cache-Control "no-cache" always`），
  assets 带 content-hash，浏览器拿到新 index.html 就会拉新 hash 的 chunk，无需手动清浏览器缓存。

## 不要做的事

- 不要 `docker compose restart ongrid-nginx`：本流程只换静态文件，nginx worker 不需要重启；
  重启反而会触发短暂 502（旧 worker 退、新 worker 起中间的空窗）。
- 不要把 50x.html 写进 vite public/：那是 nginx 通用错误兜底，归属 nginx 配置层，
  不应该跟 SPA 一起 rebuild。维护边界在 deploy/install/install.sh:934（只解 `/usr/share/nginx/html`，
  50x.html 是 install 时或 post-install 由 operator 单独放置）。
- 不要 `make docker-ongrid-web` 重打镜像再重启容器：开发期热改 SPA 直接走
  `rsync web/dist/ → /opt/ongrid/ongrid-web/html/`，跳过整镜像重建
  （`make docker-ongrid-web` 也得跑 `npm run build`，但它会把 SPA 烤进镜像层 + 重启容器，
  浪费时间在 immutable layer 上）。

## 联动 .record

- [2026-07-06 install-edge 404 前端根因修复](2026-07-06-fix-install-edge-modal-edge-id-vs-device-id.md) —— 本次同步的源码改动
- [2026-07-05 web html 路径修复](2026-07-05-fix-html-path.md) —— `$WEB_DIR/html` 路径约定
- [2026-07-05 手动重打 ongrid 云端二进制并热替换](2026-07-05-hot-rebuild-ongrid-binary.md) —— 后端 binary 的同等"热替换"流程
