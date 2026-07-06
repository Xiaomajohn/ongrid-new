# 2026-07-05 web html 路径修复

## 问题
部署后访问 https://localhost/ 返回 500 / 403，所有 SPA 路由都拿不到 index.html。

## 根因
`install.sh` / `upgrade.sh` 调用 `extract_image_dist` 时给 web dist 的 dst_dir 多带了一层 `html`：
```bash
extract_image_dist "ongrid-web:$VERSION" "$WEB_DIR/html" /usr/share/nginx/html
```
而 `extract_image_dist` 内部（line 869）会把 src 的 basename 作为 sub-dir：
```bash
name="${src##*/}"    # /usr/share/nginx/html → html
sub="${dst_dir}/${name}"   # $WEB_DIR/html/html
```
导致 SPA 内容被解压到 `${WEB_DIR}/html/html/`，但 docker-compose.yml 的 bind mount 是 `${WEB_DIR}/html` → 容器 `/usr/share/nginx/html`，于是容器内 SPA 实际在 `/usr/share/nginx/html/html/`，源码 `nginx.conf` 的 `root /usr/share/nginx/html` 找不到 index.html。

## 修复
- `deploy/install/install.sh` line 926-934：把 `dst_dir` 从 `"$WEB_DIR/html"` 改为 `"$WEB_DIR"`，让函数自己生成末尾的 `html/`。
- `deploy/install/upgrade.sh` line 678-686：同步修复。
- 源码 `deploy/install/nginx.conf` 不需要改（`root /usr/share/nginx/html` 与新解压路径对齐）。

## 验证
- `grep root /usr/share/nginx deploy/install/nginx.conf` → `root /usr/share/nginx/html;`（两处，对的）
- `grep extract_image_dist deploy/install/install.sh` → 已用 `$WEB_DIR`
- 当前部署的临时修（把 root 改成 `html/html` 并把 SPA 移到 `html/`）保留作为短期 workaround，等下次 `upgrade.sh` 走修复后逻辑会自动重置。

## 备注
- 不动 docker-compose.yml（bind mount 路径是对的）。
- 不动 Dockerfile.web / deploy/nginx/nginx.conf（镜像内的布局本来就对：COPY dist/ → /usr/share/nginx/html/）。
- nginx.conf（源码 deploy/install/nginx.conf）的 location /assets/ 也写的是 `root /usr/share/nginx/html;`，跟 location / 一致，修复同时让 /assets/ 也对。
