# Auditbeat 离线资源

auditbeat 是 Elastic 闭源二进制，**本仓不在构建时联网下载**。

## 获取

从 Elastic 官方下载页 https://artifacts.elastic.co/downloads/beats/auditbeat/
下载对应版本，例如：

- `auditbeat-9.4.2-linux-amd64.tar.gz`
- `auditbeat-9.4.2-linux-arm64.tar.gz`

> 版本号与 `Makefile` 中 `AUDITBEAT_VERSION` 一致；只升不降，避免和已发版
> bundle 不兼容。

## 放置规则

1. 解压 tar.gz，**只取 `auditbeat` 二进制本身**（不要带整个目录）
2. 复制到本目录对应子目录：

```bash
cp auditbeat-9.4.2-linux-amd64/auditbeat   linux-amd64/auditbeat
cp auditbeat-9.4.2-linux-arm64/auditbeat   linux-arm64/auditbeat
chmod +x linux-amd64/auditbeat linux-arm64/auditbeat
```

## 使用

放好二进制后，`make stage-auditbeat` 会把它镜像到 `bin/<arch>/auditbeat`，
之后 `make package` / `make build-edge-bundle` 链路自动消费。

```bash
make stage-auditbeat && make package
```

## 校验

```bash
file linux-amd64/auditbeat
# 应输出: ELF 64-bit LSB executable, x86-64, ...
file linux-arm64/auditbeat
# 应输出: ELF 64-bit LSB executable, ARM aarch64, ...
```

## 注意

- `.gitignore` 已排除 `linux-*/auditbeat` 本身，二进制不入 git
- `.gitkeep` 占位空文件仅用于保留目录结构
- 仅 Linux（auditd 依赖 Linux 内核 audit 子系统）；darwin 边缘不支持