---
title: MySQL 连接耗尽
tags: [mysql, database, connection]
alert_keys: [MySQLTooManyConnections, MySQLConnectionExhausted]
applies_to: [mysql]
---
# MySQL 连接耗尽

## What this means
MySQL 活跃连接数达到 max_connections, 新连接被拒绝(Too many connections), 业务报获取连接超时/失败。

## Immediate checks
1. 平台查询: 连接数曲线(近 30 分钟)与 max_connections 对比, 确认突增时间点
2. SHOW STATUS LIKE 'Threads_connected'; SHOW PROCESSLIST 看连接来源与状态堆积
3. 日志检索: 应用日志中 too many connections / get connection timeout 报错
4. 链路检索: 调用链中 SQL 耗时与慢查询, 排查是否慢查询占满连接

## Likely causes
- 慢查询/长事务堆积占满连接池
- 应用连接池配置过大或泄漏(未正确归还连接)
- 突增流量(促销/重试风暴)放大连接需求
- max_connections 设置过低

## Escalation criteria
- 连接耗尽导致核心业务不可用
- 需调整连接池参数或扩实例, 升级为变更处理
