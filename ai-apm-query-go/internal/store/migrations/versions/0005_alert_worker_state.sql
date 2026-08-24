-- C-03（报告 §17 / 27.22）：Alert 评估单 Leader + cooldown/dampening MySQL 持久化。
--
-- 表设计：
--   alert_eval_leader：alert-eval 角色的单 Leader 租约（K8s Lease 语义的 MySQL 实现，
--     多 alert-eval pod 只有一个能评估；与 Run lease 同用 epoch/fencing 防双主）。
--   alert_rule_runtime_state：每条规则的 cooldown/dampening 运行时状态（MySQL 持久化，
--     进程内 map 只能当缓存；多 pod / 重启后不丢失冷却期与连续 breach 计数）。
-- 均为 additive，不修改既有表。
CREATE TABLE IF NOT EXISTS aiops.alert_eval_leader (
    leader_name    VARCHAR(64)  NOT NULL DEFAULT 'alert-eval',
    holder_id      VARCHAR(64)  NOT NULL DEFAULT '',
    holder_epoch   BIGINT       NOT NULL DEFAULT 0,
    token_hash     CHAR(64)     NOT NULL DEFAULT '',
    acquired_at    DATETIME(3)  NULL,
    expires_at     DATETIME(3)  NULL,
    PRIMARY KEY (leader_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint
CREATE TABLE IF NOT EXISTS aiops.alert_rule_runtime_state (
    rule_id         VARCHAR(64) NOT NULL,
    last_trigger_at DATETIME(3) NULL,
    breach_streak   INT         NOT NULL DEFAULT 0,
    updated_at      DATETIME(3) NULL,
    PRIMARY KEY (rule_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
