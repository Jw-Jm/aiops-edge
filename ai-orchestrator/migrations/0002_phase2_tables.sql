-- 0002_phase2_tables.sql — 二期强化采集数据表（IPMI / 部件可用性）
-- 四网段隔离约束：仅管理面采集；IPMI 用本地 /dev/ipmi0

-- IPMI 传感器（本地 /dev/ipmi0 采集，温度/风扇/电压/电源）
CREATE TABLE IF NOT EXISTS ipmi_sensors (
  id           BIGINT PRIMARY KEY AUTO_INCREMENT,
  node_name    VARCHAR(128) NOT NULL,
  sensor_name  VARCHAR(128),
  sensor_type  VARCHAR(64),
  reading      VARCHAR(64),
  status       VARCHAR(32),
  created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_ipmi_node (node_name),
  KEY idx_ipmi_type (node_name, sensor_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- IPMI 系统事件日志（SEL）
CREATE TABLE IF NOT EXISTS ipmi_sel_events (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  node_name  VARCHAR(128) NOT NULL,
  event_id   VARCHAR(64),
  event_time DATETIME,
  sensor     VARCHAR(128),
  event_desc VARCHAR(255),
  severity   VARCHAR(16),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_sel_node_time (node_name, event_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 服务器部件可用性（聚合 node_exporter + IPMI）
CREATE TABLE IF NOT EXISTS node_component_health (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  node_name  VARCHAR(128) NOT NULL,
  component  VARCHAR(32),
  status     VARCHAR(32),
  detail     VARCHAR(255),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_nch_node_comp (node_name, component)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
