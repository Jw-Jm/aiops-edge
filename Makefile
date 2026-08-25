.PHONY: test-workflow-contract test-query-go test-executor test-orchestrator test-frontend test-workflow-all

test-workflow-contract:
	./ai-orchestrator/.venv314/bin/python -m pytest tests/workflow-e2e -q

test-query-go:
	cd ai-apm-query-go && go test ./...

test-executor:
	cd ai-action-executor && go test ./...

test-orchestrator:
	cd ai-orchestrator && ./.venv314/bin/python -m pytest tests -q

test-frontend:
	cd observability-frontend && npm run build

test-workflow-all: test-workflow-contract test-query-go test-executor test-orchestrator test-frontend
