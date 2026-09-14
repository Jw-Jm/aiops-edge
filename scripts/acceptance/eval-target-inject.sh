#!/usr/bin/env bash
# 受控故障注入库（T11 / 验收第八章"故障注入与恢复"）
# 用法: eval-target-inject.sh <inject|recover|status> <case>
# 全部资源带 acceptance.io/run-id 注解，注入后 recover 恢复。
set -euo pipefail
NS=aiops-eval
RUN_ID=${RUN_ID:-fa-20260913T063444Z-d96df52f74fa}
CASE=${2:-}

ensure_app() {
  kubectl create ns $NS --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n $NS annotate deploy/eval-target acceptance.io/run-id=$RUN_ID --overwrite 2>/dev/null || true
}

status() {
  kubectl -n $NS get deploy eval-target -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null || echo "no-deploy"
  echo
}

case "$1" in
  setup)
    ensure_app
    kubectl -n $NS apply -f - << 'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: eval-target
  labels: {app: eval-target}
spec:
  replicas: 1
  selector: {matchLabels: {app: eval-target}}
  template:
    metadata: {labels: {app: eval-target}}
    spec:
      containers:
      - name: app
        image: public.ecr.aws/nginx/nginx:alpine
        resources: {limits: {memory: "64Mi"}}
EOF
    kubectl -n $NS annotate deploy/eval-target acceptance.io/run-id=$RUN_ID --overwrite
    kubectl -n $NS rollout status deploy/eval-target --timeout=120s
    ;;
  inject)
    case "$CASE" in
      c4-bad-image)   # ImagePullBackOff：Warning 事件
        kubectl -n $NS set image deploy/eval-target app=nginx:definitely-not-a-real-tag-xyz
        ;;
      c3-bad-probe)   # liveness 失败：探针路径错误 → 反复重启
        kubectl -n $NS set probe deploy/eval-target --liveness --get-url=http://:80/nonexistent-probe-path --initial-delay-seconds=5 --period-seconds=5 2>/dev/null || \
        kubectl -n $NS patch deploy eval-target --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/livenessProbe","value":{"httpGet":{"path":"/nonexistent-probe","port":80},"initialDelaySeconds":5,"periodSeconds":5}}]'
        ;;
      x2-bad-limits)  # 资源限制错误：内存 4Mi → OOMKilled
        kubectl -n $NS set resources deploy/eval-target --containers=app --limits=memory=4Mi
        ;;
      *) echo "unknown case $CASE"; exit 1;;
    esac
    kubectl -n $NS annotate deploy/eval-target acceptance.io/case=$CASE acceptance.io/run-id=$RUN_ID --overwrite
    echo "injected: $CASE"
    ;;
  recover)
    kubectl -n $NS rollout undo deploy/eval-target 2>/dev/null || true
    kubectl -n $NS set image deploy/eval-target app=public.ecr.aws/nginx/nginx:alpine 2>/dev/null || true
    kubectl -n $NS patch deploy eval-target --type=json -p='[{"op":"remove","path":"/spec/template/spec/containers/0/livenessProbe"}]' 2>/dev/null || true
    kubectl -n $NS set resources deploy/eval-target --containers=app --limits=memory=64Mi 2>/dev/null || true
    kubectl -n $NS rollout status deploy/eval-target --timeout=180s && echo "recovered"
    ;;
  *)
    echo "usage: $0 setup|inject|recover|status [case]"; exit 1;;
esac
