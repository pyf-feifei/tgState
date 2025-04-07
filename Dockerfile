# 使用 Go 镜像进行编译
FROM golang:1.19 AS builder

# 设置工作目录
WORKDIR /app

# 复制源代码
COPY . .

# 下载依赖并编译
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o tgState

# 使用官方的 Ubuntu 基础镜像
FROM ubuntu:latest

# 安装 ca-certificates 包，用于更新根证书
RUN apt-get update && apt-get install -y ca-certificates

# 创建应用目录
RUN mkdir -p /app

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的二进制文件
COPY --from=builder /app/tgState /app/tgState

# 确保文件有执行权限
RUN chmod +x /app/tgState

# 设置暴露的端口
EXPOSE 8088

# 设置容器启动时要执行的命令
CMD [ "/app/tgState" ]