# 当前部署版本归档

2026-10-15 从运行中服务的 `/opt/showstart/go-monitor` 提取源码。
除删除一个历史注释中的账号请求示例和格式化两个 Go 文件外，运行源码保持服务器版本；没有重启或重新部署。

本版包含 App bridge 接入、账号池热加载、定时购票提示及成功率附加信息。
bridge 和 Linux 登录恢复源码在
[miniprogram-order/projects/showstart](https://github.com/Kris77z/miniprogram-order/tree/codex/showstart-deployed-20261015/projects/showstart)。

```bash
go test ./...
go build -o showstart-monitor .
```

服务从工作目录读取 `config.yaml`。从 `config.example.yaml` 建立服务器私密配置，
设置 `showstart.base_url` 为 `http://127.0.0.1:18777`，
`showstart.auth_state_file` 为服务器账号池绝对路径，
`showstart.auth_account_alias` 与 bridge 所选账号一致。
启用监控前配置关键词、通知地址和持久化 state_dir；不要把真实配置提交到 Git。
监控模式与下单模式不同，保留示例的关闭状态，避免复制模板就启动任务。

原部署由 systemd 先等待 bridge 就绪，再运行监控。生产凭证、登录态、通知地址、
日志、去重状态、编译产物均未归档。测试使用模拟数据，不触发真实通知或下单。
