FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
# 国内源 + npm 缓存持久化（buildkit cache mount：首次下载后后续构建零网络复用；
# deploy/offline/npm-cache 由 fetch-offline-deps.sh 预下载，构建时可挂载复用）。
# 注意：不 COPY 宿主 node_modules（含平台特定原生二进制，跨平台不可用），
# 必须在容器内 npm ci 安装正确平台的 rollup/esbuild 等原生模块。
RUN --mount=type=cache,target=/root/.npm \
    npm config set registry https://registry.npmmirror.com && \
    (npm ci --prefer-offline 2>/dev/null || npm install --prefer-offline)
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
# nginx 基镜像 apk 走国内源（阿里云），避免官方源不可达
RUN sed -i 's@dl-cdn.alpinelinux.org@mirrors.aliyun.com@g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates || true
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
