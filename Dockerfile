# 使用 Go 镜像进行编译
FROM golang:1.19-alpine AS builder

# 设置工作目录
WORKDIR /app

# 安装 git 和基本构建工具
RUN apk add --no-cache git gcc musl-dev

# 复制 go.mod 和 go.sum 文件
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 编译
RUN CGO_ENABLED=0 GOOS=linux go build -o tgState

# 使用轻量级的 Alpine 镜像
FROM alpine:latest

# 安装 ca-certificates 包
RUN apk add --no-cache ca-certificates

# 创建应用目录
RUN mkdir -p /app

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的二进制文件
COPY --from=builder /app/tgState /app/tgState
COPY --from=builder /app/assets /app/assets

# 确保文件有执行权限
RUN chmod +x /app/tgState

# 设置暴露的端口
EXPOSE 8088

# 设置容器启动时要执行的命令
CMD [ "/app/tgState" ]