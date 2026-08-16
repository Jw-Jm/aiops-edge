FROM node:20-alpine AS builder
WORKDIR /app
# 先 COPY 全部源码：若项目本地已存在 node_modules（fetch-offline-deps.sh 预装到
# deploy/offline/frontend-node_modules 并同步回源目录），则直接复用，零网络零下载；
# 缺失时才走 npmmirror 国内源安装（配合 buildkit cache mount 持久化 npm 缓存）。
COPY . .
RUN if [ ! -d node_modules ] || [ -z "$(ls -A node_modules 2>/dev/null)" ]; then \
      npm config set registry https://registry.npmmirror.com && npm ci --prefer-offline; \
    fi
RUN npm run build

FROM nginx:1.27-alpine
# nginx 基镜像 apk 走国内源（阿里云），避免官方源不可达
RUN sed -i 's@dl-cdn.alpinelinux.org@mirrors.aliyun.com@g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates || true
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
