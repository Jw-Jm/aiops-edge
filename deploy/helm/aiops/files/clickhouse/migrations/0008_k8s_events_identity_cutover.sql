-- 0008-k8s-events-identity-cutover
-- Historical rows without a verifiable canonical identity must never be
-- silently assigned a guessed event_id or an implicit tenant/cluster.  Keep
-- them in a bounded quarantine table for an evidence-backed replay decision.

CREATE TABLE IF NOT EXISTS observability.k8s_events_quarantine
(
    quarantine_id UUID DEFAULT generateUUIDv4(),
    quarantined_at DateTime64(3) DEFAULT now64(3),
    reason String,
    tenant_id String,
    cluster_id String,
    ts DateTime64(9),
    namespace String,
    kind String,
    name String,
    event_reason String,
    type String,
    message String,
    involved_object String,
    source_component String,
    source String,
    node String,
    time_bucket DateTime,
    event_id String
)
ENGINE = MergeTree
PARTITION BY toDate(quarantined_at)
ORDER BY (quarantined_at, quarantine_id)
TTL toDateTime(quarantined_at) + INTERVAL 365 DAY;

CREATE TABLE IF NOT EXISTS observability.k8s_events_identity_audit
(
    migration_id String,
    audited_at DateTime64(3) DEFAULT now64(3),
    scanned UInt64,
    quarantined UInt64,
    remaining_invalid UInt64
)
ENGINE = MergeTree
ORDER BY (audited_at, migration_id);

INSERT INTO observability.k8s_events_quarantine
(
    reason, tenant_id, cluster_id, ts, namespace, kind, name, event_reason,
    type, message, involved_object, source_component, source, node,
    time_bucket, event_id
)
SELECT
    multiIf(
        event_id = '', 'missing_event_id',
        NOT match(event_id, '^[0-9a-f]{64}$'), 'invalid_event_id',
        NOT match(tenant_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'), 'invalid_tenant_id',
        NOT match(cluster_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'), 'invalid_cluster_id',
        'unverifiable_identity'
    ) AS reason,
    tenant_id, cluster_id, ts, namespace, kind, name, reason, type, message,
    involved_object, source_component, source, node, time_bucket, event_id
FROM observability.k8s_events
WHERE event_id = ''
   OR NOT match(event_id, '^[0-9a-f]{64}$')
   OR NOT match(tenant_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
   OR NOT match(cluster_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

INSERT INTO observability.k8s_events_identity_audit
    (migration_id, scanned, quarantined, remaining_invalid)
SELECT
    '0008_k8s_events_identity_cutover',
    count(),
    countIf(
        event_id = ''
        OR NOT match(event_id, '^[0-9a-f]{64}$')
        OR NOT match(tenant_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
        OR NOT match(cluster_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
    ),
    0
FROM observability.k8s_events;

ALTER TABLE observability.k8s_events
    DELETE WHERE event_id = ''
       OR NOT match(event_id, '^[0-9a-f]{64}$')
       OR NOT match(tenant_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
       OR NOT match(cluster_id, '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
    SETTINGS mutations_sync = 1;

ALTER TABLE observability.k8s_events
    MODIFY COLUMN event_id String;
