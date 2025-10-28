# 使用官方 Go 镜像
FROM golang:1.21-alpine AS builder

# 设置工作目录
WORKDIR /app

# 复制 go mod 文件
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 编译应用
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o showstart_monitor .

# 使用轻量级镜像运行
FROM alpine:latest

# 安装 ca-certificates（用于 HTTPS 请求）
RUN apk --no-cache add ca-certificates tzdata

# 设置时区为上海
ENV TZ=Asia/Shanghai

WORKDIR /root/

# 从 builder 阶段复制可执行文件
COPY --from=builder /app/showstart_monitor .

# 复制配置文件
COPY --from=builder /app/config.yaml .

# 创建状态文件目录
RUN mkdir -p monitor_state

# 暴露端口（虽然我们不需要，但 Railway 可能需要）
EXPOSE 8080

# 运行应用
CMD ["./showstart_monitor"]
