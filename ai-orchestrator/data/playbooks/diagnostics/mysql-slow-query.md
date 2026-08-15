---
title: MySQL 慢查询
tags: [mysql, database, slow]
alert_keys: [MySQLSlowQueries, MySQLLongRunning, MySQLLockWait]
applies_to: [mysql]
---
# MySQL 慢查询

## What this means
MySQL 出现大量慢查询(超过 long_query_time), 导致接口延迟升高、连接占用时间变长, 进而可能触发连接池打满。

## Immediate checks
1. 平台查询: 该 DB 实例慢查询数/QPS/延迟曲线(近 30 分钟)
2. SHOW FULL PROCESSLIST 看长时间运行与锁等待(State 为 Waiting for table lock 等)
3. 日志检索: slow_log 中耗时 TopN SQL, 检查执行计划(EXPLAIN)是否走索引
4. 平台查询: 相关表行数增长与索引使用情况

## Likely causes
- SQL 未命中索引/索引失效(全表扫描)
- 大事务或长事务持有锁阻塞其他查询
- 数据量增长后旧 SQL 性能退化
- 连接数不足导致排队(与连接耗尽联动)

## Escalation criteria
- 慢查询导致核心接口 P99 明显恶化
- 锁等待/死锁影响多张核心表, 需 DBA 专项介入
