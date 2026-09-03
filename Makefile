.PHONY: test-workflow-contract test-query-go test-executor test-orchestrator test-frontend test-workflow-all

# Python 解释器解析顺序（与 verify-aiops-workflow-gates.sh 一致）：
# AIOPS_PYTHON 显式指定 > 仓库内 .venv314（CI 创建）> 系统 python3。
# 统一使用绝对路径，使同一变量在仓库根与 ai-orchestrator/ 子目录内都能用。
AIOPS_PYTHON ?= $(shell if [ -x ai-orchestrator/.venv314/bin/python ]; then echo $(CURDIR)/ai-orchestrator/.venv314/bin/python; else echo python3; fi)

test-workflow-contract:
	$(AIOPS_PYTHON) -m pytest tests/workflow-e2e -q

test-query-go:
	cd ai-apm-query-go && go test ./...

test-executor:
	cd ai-action-executor && go test ./...

test-orchestrator:
	cd ai-orchestrator && $(AIOPS_PYTHON) -m pytest tests -q

test-frontend:
	cd observability-frontend && npm run test:run && npm run build

test-workflow-all: test-workflow-contract test-query-go test-executor test-orchestrator test-frontend
