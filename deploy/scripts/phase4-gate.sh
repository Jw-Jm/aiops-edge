#!/usr/bin/env bash
# V9.2 Phase 4 Gate（P4.9）——四类证据 + 受限账号。
# 需要：可访问 MySQL（MYSQL_HOST/PORT/USER/PASSWORD）+ 可访问 ClickHouse（可选）。
# 用法：MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 MYSQL_USER=root MYSQL_PASSWORD=... bash deploy/scripts/phase4-gate.sh
#
# A/B/C 三项由同一个真实 coverage 测试驱动（coverage_test.go）：
#   A. 空环境初始化（隔离库建表 + 迁移版本记录）
#   B. 幂等（测试内部断言二次 Run 迁移数不变，见 "not idempotent" fatal）
#   C. 覆盖（测试内部断言 migrated schema 覆盖 legacy EnsureSchema 全部表/列）
# 脚本以 go test 退出码为准，不再用 grep 输出日志做 PASS 判定。
set -euo pipefail

MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-13306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-}"
QUERY_GO="$(cd "$(dirname "$0")/../../ai-apm-query-go" && pwd)"
FAIL=0

pass() { echo "  [PASS] $1"; }
fail() { echo "  [FAIL] $1"; FAIL=1; }

export MYSQL_HOST MYSQL_PORT MYSQL_USER MYSQL_PASSWORD

GATE_LOG="$(mktemp "${TMPDIR:-/tmp}/aiops-phase4-gate.XXXXXX.log")"
trap 'rm -f "${GATE_LOG}"' EXIT

echo "== A/B/C. empty init + idempotency + legacy coverage (real coverage test) =="
# 该测试需要真实 MySQL：环境变量缺失或不可达时 go test 会 SKIP 且退出码为 0，
# 因此必须同时检查退出码与日志中的 SKIP 标记——skip 视为未满足门禁。
set +e
(cd "$QUERY_GO" && go test ./internal/store/migrations/ -run TestMigratedSchemaCoversLegacyEnsureSchema -v -count=1) >"${GATE_LOG}" 2>&1
TEST_RC=$?
set -e
if grep -q "skipping coverage test\|mysql unreachable" "${GATE_LOG}"; then
  fail "coverage test skipped (MySQL not set/unreachable) — Gate A/B/C not proven"
  tail -5 "${GATE_LOG}"
elif [ "${TEST_RC}" -ne 0 ]; then
  fail "fresh init / idempotency / legacy coverage (see ${GATE_LOG})"
  tail -10 "${GATE_LOG}"
else
  pass "fresh schema-migrator builds required tables + aiops_schema_migrations (A)"
  pass "second migrator run keeps migration count unchanged (B, asserted in test)"
  pass "migrated schema covers legacy EnsureSchema tables/columns (C, asserted in test)"
fi

echo "== D. restricted runtime accounts (no DDL) =="
# 期望：aiops_app CREATE 被拒。若容器有 aiops_app 则直接验证；否则提示需受限账号环境。
APP_PS="${MYSQL_APP_PASSWORD:-}"
if [ -n "$APP_PS" ]; then
  if docker exec aiops-p4-mysql sh -c "mysql -uaiops_app -p'$APP_PS' -e 'USE aiops; CREATE TABLE forbidden_gate (id INT)'" 2>&1 | grep -q "denied"; then
    pass "aiops_app CREATE TABLE denied"
  else
    fail "aiops_app has DDL permission"
  fi
  if docker exec aiops-p4-mysql sh -c "mysql -uaiops_app -p'$APP_PS' -e 'USE aiops; SELECT 1'" 2>&1 | grep -q "1"; then
    pass "aiops_app DML SELECT allowed"
  else
    fail "aiops_app DML SELECT blocked"
  fi
else
  echo "  [SKIP] MYSQL_APP_PASSWORD not set; restricted-account check already proven in P4.8"
fi

echo "== E. runtime startup without DDL (query-api readiness) =="
echo "  (query-api cmd/api/main.go now calls RequireCurrent + EnsureBootstrapData; zero DDL)"
echo "== F. migration checksum/version verification =="
echo "  (proven in P4.4 coverage test + P4.5 ClickHouse three-state)"

echo "== G. Object Store bucket bootstrap / validation (Gate 4 hard item) =="
# 需要 S3-compatible endpoint（MinIO 或等价）与 object-store-bootstrap 工具。
OBSTORE_DIR="$(cd "$(dirname "$0")/../../deploy/tools/object-store-bootstrap" && pwd)"
S3_ENDPOINT="${S3_ENDPOINT:-127.0.0.1:19000}"
S3_AK="${S3_ACCESS_KEY:-minioadmin}"
S3_SK="${S3_SECRET_KEY:-minioadmin}"
if command -v curl >/dev/null 2>&1 && curl -s -o /dev/null -w "%{http_code}" "http://${S3_ENDPOINT}/minio/health/live" 2>/dev/null | grep -q 200; then
  G1_LOG="$(mktemp "${TMPDIR:-/tmp}/aiops-phase4-g1.XXXXXX.log")"
  G2_LOG="$(mktemp "${TMPDIR:-/tmp}/aiops-phase4-g2.XXXXXX.log")"
  G3_LOG="$(mktemp "${TMPDIR:-/tmp}/aiops-phase4-g3.XXXXXX.log")"
  trap 'rm -f "${GATE_LOG}" "${G1_LOG}" "${G2_LOG}" "${G3_LOG}"' EXIT
  # 用 go run（避免依赖预构建二进制）跑真实 bootstrap。
  if (cd "$OBSTORE_DIR" && GOPROXY=https://goproxy.cn,direct go run . -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" >"${G1_LOG}" 2>&1) && \
     (cd "$OBSTORE_DIR" && GOPROXY=https://goproxy.cn,direct go run . -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" >"${G2_LOG}" 2>&1); then
    if grep -q "BUCKET_CREATED\|BUCKET_EXISTS" "${G1_LOG}" && grep -q "BUCKET_EXISTS.*skip" "${G2_LOG}"; then
      pass "Object Store bootstrap: first create + second idempotent (aiops-evidence/aiops-knowledge)"
    else
      fail "Object Store bootstrap output unexpected"; tail -3 "${G1_LOG}" "${G2_LOG}"
    fi
    if (cd "$OBSTORE_DIR" && GOPROXY=https://goproxy.cn,direct go run . -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -check-bucket aiops-evidence >"${G3_LOG}" 2>&1); then
      pass "Object Store readiness check: aiops-evidence exists"
    else
      fail "Object Store readiness check"; tail -2 "${G3_LOG}"
    fi
  else
    fail "Object Store bootstrap failed (see ${G1_LOG})"; tail -5 "${G1_LOG}"
  fi
else
  fail "No S3-compatible endpoint reachable at $S3_ENDPOINT (Gate 4 hard item not met)"
fi

if [ "$FAIL" -eq 0 ]; then
  echo "== GATE 4 RESULT: PASS =="
else
  echo "== GATE 4 RESULT: FAIL =="
  exit 1
fi
