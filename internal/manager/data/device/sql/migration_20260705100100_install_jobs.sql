-- install_jobs + install_job_events: one-click edge install job tracking.
-- See plan "设备页SSH双通道与一键安装" §数据库迁移.

CREATE TABLE install_jobs (
  id              BIGINT PRIMARY KEY AUTO_INCREMENT,
  device_id       BIGINT NOT NULL,
  edge_id         BIGINT NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'queued',  -- queued|running|success|failed|cancelled
  auth_kind       VARCHAR(16) NOT NULL,                  -- password|key (credential snapshot, not logged)
  password_snap   VARCHAR(255) NULL,                     -- in-arg snapshot, cleared by worker as soon as used
  key_snap        MEDIUMTEXT   NULL,
  host            VARCHAR(255) NOT NULL,
  port            INT NOT NULL,
  user            VARCHAR(64)  NOT NULL,
  options_json    JSON NULL,
  log_output      MEDIUMTEXT NOT NULL,
  started_at      DATETIME NULL,
  finished_at     DATETIME NULL,
  exit_code       INT NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_install_jobs_device (device_id, created_at),
  INDEX idx_install_jobs_status (status, created_at),
  CONSTRAINT fk_install_jobs_device FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE install_job_events (
  id              BIGINT PRIMARY KEY AUTO_INCREMENT,
  install_job_id  BIGINT NOT NULL,
  ts              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  kind            VARCHAR(32) NOT NULL,                  -- log|state|error
  payload         TEXT NOT NULL,
  INDEX idx_install_job_events (install_job_id, ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;