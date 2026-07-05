-- Device SSH credentials (plaintext, internal-only system) + SFTP file-ops audit table.
-- See plan "设备页SSH双通道与一键安装" §数据库迁移.

ALTER TABLE devices
  ADD COLUMN ssh_host          VARCHAR(255) NULL,
  ADD COLUMN ssh_port          INT NOT NULL DEFAULT 22,
  ADD COLUMN ssh_user          VARCHAR(64)  NULL,
  ADD COLUMN ssh_auth_kind     VARCHAR(16)  NOT NULL DEFAULT 'password',
  ADD COLUMN ssh_password      VARCHAR(255) NULL,                    -- plaintext
  ADD COLUMN ssh_key           MEDIUMTEXT   NULL,                    -- plaintext PEM
  ADD COLUMN ssh_host_key      TEXT         NULL,                    -- plaintext authorized_keys format
  ADD COLUMN ssh_last_seen_at  DATETIME     NULL,
  ADD COLUMN ssh_last_error    VARCHAR(512) NULL;

CREATE INDEX idx_devices_ssh_host ON devices(ssh_host);

-- SFTP file operation audit (one row per list/read/write/rm/mkdir/rename/chmod).
CREATE TABLE device_ssh_file_ops (
  id              BIGINT PRIMARY KEY AUTO_INCREMENT,
  device_id       BIGINT NOT NULL,
  ongrid_user_id  BIGINT NOT NULL,
  op              VARCHAR(16) NOT NULL,            -- list|read|write|mkdir|rmdir|rename|chmod|stat|upload|download
  path            VARCHAR(1024) NOT NULL,
  path_new        VARCHAR(1024) NULL,              -- rename target
  size_bytes      BIGINT NULL,
  mode            INT NULL,                        -- chmod arg
  status          VARCHAR(16) NOT NULL,            -- ok|denied|error
  err_msg         VARCHAR(512) NULL,
  client_ip       VARCHAR(64) NULL,
  started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at     DATETIME NULL,
  INDEX idx_device_ssh_file_ops_device (device_id, started_at),
  INDEX idx_device_ssh_file_ops_user (ongrid_user_id, started_at),
  CONSTRAINT fk_device_ssh_file_ops_device FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;