FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
# 国内源：避免直连 npmjs.org 超时
RUN npm config set registry https://registry.npmmirror.com && (npm ci 2>/dev/null || npm install)
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
