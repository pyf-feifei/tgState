# 使用官方的 Ubuntu 基础镜像
FROM ubuntu:latest

# 安装 ca-certificates 包，用于更新根证书
RUN apt-get update && apt-get install -y ca-certificates

# 创建应用目录
RUN mkdir -p /app

# 设置工作目录
WORKDIR /app

# 如果tgState文件存在，则复制它
# 如果不存在，请注释掉下面这行，并提供替代方案
COPY ./tgState /app/tgState

# 确保文件有执行权限
RUN if [ -f "/app/tgState" ]; then chmod +x /app/tgState; fi

# 设置暴露的端口
EXPOSE 8088

# 设置容器启动时要执行的命令
CMD [ "/app/tgState" ]