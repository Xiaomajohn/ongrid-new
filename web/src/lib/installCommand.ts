// installCommand.ts — 唯一拼装源:手动安装和自动安装共用同一套 buildInstallCommand 函数。
// 前端用 window.location.host 拼出 curl 命令,后端 SSHInstaller 只负责执行这条字符串,
// 不再有任何 fmt.Sprintf 拼装逻辑。

export interface BuildInstallCommandInput {
  accessKey: string;
  secretKey: string;
  /** 浏览器环境默认 window.location.host */
  host?: string;
  /** 默认 40012 */
  tunnelPort?: number;
}

/** 返回值:
 *   cmd     - 单行完整 curl 命令,给后端 SSH 执行
 *   display - 同 cmd,给用户复制展示
 */
export function buildInstallCommand(input: BuildInstallCommandInput): {
  cmd: string;
  display: string;
} {
  const host =
    input.host ??
    (typeof window !== 'undefined' ? window.location.host : 'ongrid.example.com');
  const tunnelPort = input.tunnelPort ?? 40012;
  const hostnameOnly = host.split(':')[0] || host;
  const tunnelAddr = `${hostnameOnly}:${tunnelPort}`;

  const cmd =
    `curl -k -sSL https://${host}/install.sh | bash -s -- ` +
    `--access-key=${input.accessKey} ` +
    `--secret-key=${input.secretKey} ` +
    `--server-edge-addr=${tunnelAddr} ` +
    `--server-http-addr=${host}`;

  return { cmd, display: cmd };
}
