-- OpenCode worker 通道（lane）：keyed 用池级共享 API key，anonymous 不带 Authorization。
--
-- Zen tier 接受完全无认证的推理请求（健康探针已在生产上以 399 个节点持续证明），
-- 且匿名额度按出口 IP 独立计量；而池级 key 是单一全局配额桶，IP 池对它的限流完全
-- 失效。两个 lane 在同一节点上并存，各自拿一条 egress 租约。
--
-- NOT NULL DEFAULT 'keyed' 让现有 worker 与租约自动落进 keyed lane，无需 backfill。

ALTER TABLE opencode_pool_workers ADD COLUMN IF NOT EXISTS lane VARCHAR(16) NOT NULL DEFAULT 'keyed';
ALTER TABLE opencode_pool_workers DROP CONSTRAINT IF EXISTS opencode_pool_workers_lane_check;
ALTER TABLE opencode_pool_workers ADD CONSTRAINT opencode_pool_workers_lane_check CHECK (lane IN ('keyed', 'anonymous'));

ALTER TABLE opencode_egress_leases ADD COLUMN IF NOT EXISTS lane VARCHAR(16) NOT NULL DEFAULT 'keyed';
ALTER TABLE opencode_egress_leases DROP CONSTRAINT IF EXISTS opencode_egress_leases_lane_check;
ALTER TABLE opencode_egress_leases ADD CONSTRAINT opencode_egress_leases_lane_check CHECK (lane IN ('keyed', 'anonymous'));

-- 放宽 194 的 UNIQUE(exit_ip)：同一个出口 IP 现在允许两个 lane 各持一条租约。
ALTER TABLE opencode_egress_leases DROP CONSTRAINT IF EXISTS opencode_egress_leases_exit_ip_key;
ALTER TABLE opencode_egress_leases DROP CONSTRAINT IF EXISTS opencode_egress_leases_lane_exit_ip_key;
ALTER TABLE opencode_egress_leases ADD CONSTRAINT opencode_egress_leases_lane_exit_ip_key UNIQUE (lane, exit_ip);

-- 放宽 195 的 UNIQUE(pool_id, managed_node_id)：同一节点现在可以物化两个 worker。
ALTER TABLE opencode_pool_workers DROP CONSTRAINT IF EXISTS opencode_pool_workers_pool_node_key;
ALTER TABLE opencode_pool_workers DROP CONSTRAINT IF EXISTS opencode_pool_workers_pool_node_lane_key;
ALTER TABLE opencode_pool_workers ADD CONSTRAINT opencode_pool_workers_pool_node_lane_key UNIQUE (pool_id, managed_node_id, lane);

-- server_direct worker 的 managed_node_id IS NULL，PG 视 NULL 互不相等，
-- 上面那个三列 UNIQUE 对它完全不起作用——这正是当初单独建偏索引的原因。
DROP INDEX IF EXISTS opencode_pool_workers_single_direct;
CREATE UNIQUE INDEX IF NOT EXISTS opencode_pool_workers_single_direct_lane
    ON opencode_pool_workers (pool_id, lane)
    WHERE egress_mode = 'server_direct';

CREATE INDEX IF NOT EXISTS opencode_pool_workers_pool_lane_status
    ON opencode_pool_workers (pool_id, lane, status);

-- 池级灰度开关。默认关闭，开启后先只放 20 个匿名 worker；
-- anonymous_worker_limit = 0 表示不限量（铺满全部合格节点）。
ALTER TABLE opencode_pools ADD COLUMN IF NOT EXISTS anonymous_lane_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE opencode_pools ADD COLUMN IF NOT EXISTS anonymous_worker_limit INTEGER NOT NULL DEFAULT 20;
ALTER TABLE opencode_pools DROP CONSTRAINT IF EXISTS opencode_pools_anonymous_worker_limit_check;
ALTER TABLE opencode_pools ADD CONSTRAINT opencode_pools_anonymous_worker_limit_check
    CHECK (anonymous_worker_limit >= 0 AND anonymous_worker_limit <= 5000);

-- 承接 195 的 sync_opencode_worker_account_cooldown：匿名 lane 不再被降级、租约不再被删除。
--
-- 为什么必须改触发器而不是只在 Go 侧不写：rate_limit_reset_at 的写入方不止一个
-- （单次 429 就有 fastpath 与 handle429 两条独立路径，另有管理端手工操作），
-- 触发器是唯一能穷尽所有写入方的卡点。匿名额度按 IP 计量、429 是秒级事件，
-- 租约反复删建会让同伴抢走出口 IP，最终 duplicate_exit_ip 导致永久不可调度。
--
-- 只 CREATE OR REPLACE FUNCTION，不重建触发器：替换函数体不需要在 accounts
-- 这张热表上取 ACCESS EXCLUSIVE 锁。
--
-- 因 opencode_pool_workers.account_id 有 UNIQUE 约束，AND lane = 'keyed' 最多命中
-- 一行；对匿名账号和所有非 OpenCode 账号都是 no-op，keyed lane 行为逐字节不变。
CREATE OR REPLACE FUNCTION sync_opencode_worker_account_cooldown()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.rate_limit_reset_at IS NOT NULL
       AND NEW.rate_limit_reset_at > NOW()
       AND (OLD.rate_limit_reset_at IS DISTINCT FROM NEW.rate_limit_reset_at) THEN
        UPDATE opencode_pool_workers
        SET status = 'cooling', error_reason = 'rate_limited', updated_at = NOW()
        WHERE account_id = NEW.id AND lane = 'keyed';
        DELETE FROM opencode_egress_leases
        WHERE account_id = NEW.id
          AND EXISTS (
              SELECT 1 FROM opencode_pool_workers w
              WHERE w.account_id = NEW.id AND w.lane = 'keyed'
          );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
