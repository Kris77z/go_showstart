# 显式 App bridge 订单与受控回退

此候选版本保留原 `showstart.base_url`、账号池选择、监控配置与三个监控接口的响应契约。没有切换或部署在线服务。新订单入口是单独构建、单独调用的 `bridge-order`，不读取旧 Sign/Token，也不会自动挑最高价或随机实名人。

新版本原 `ticket`、`monitor.auto_purchase_enable`、`Order`、`CoreOrder` 和旧 worker 写入口已禁用，调用时在网络请求前明确拒绝。需要旧行为时使用保留的原 binary 和下述 wrapper；该 binary 的历史内部 POST 重试和多 worker 行为没有因此被修改，wrapper 不保证其内部 exactly-once。wrapper 仅保证同机、同 root、同真实账号的受控启动隔离，以及未决记录存在时拒绝后续启动。

## 能力合同 v1

| Go 原接口类别 | 候选 bridge 对应能力 | 边界 |
| --- | --- | --- |
| GetToken | 原 `/waf/gettoken` | 保留监控兼容；订单 CLI 不接收 token |
| ActivitySearchList | 原 `/wap/activity/list` | 保留监控兼容 |
| ActivityTicketList | 原 `/wap/activity/V2/ticket/list` | 保留监控兼容 |
| ActivityDetail | `ReadActivityDetail` → `/bridge/v1/activity/details` | 绑定同一 alias/cusid/device；原始 App result |
| Confirm | `Prepare` → `/bridge/v1/orders/prepare` | 核对票档、金额、数量、配送、实名；只读 |
| CpList | `ReadPerformers` → `/bridge/v1/performers`；prepare 内复核 | 必须显式提供 ID；无需实名提供 `[]` |
| AdressList | `ReadAddresses` 明确 unsupported | 不能用空列表伪装没有地址；本版只支持电子票 |
| OrderList (`/nj/coupon/order_list`) | `ReadCoupons` 明确 unsupported | 这是优惠券相关接口；App 订单列表另用 `ReadOrders` → `/bridge/v1/orders/list` |
| Order | `Submit` → `/bridge/v1/orders` | 单次发送；bridge 先持久化账号 claim；不支付 |
| CoreOrder | `Status(recover=true)` → `/bridge/v1/orders/status` | 只查询 journal 中的 `core_order_key`；旧方法禁止调用 |
| GetOrderResult | 同一 status/recover 接口 | 只查询 journal 中的 `order_job_key`；request_key 不是查询 key |

`ReadOrder` 可通过已知 `order_id` 调用 `/bridge/v1/orders/detail`，用于人工核对；列表里没找到订单不能解除未决记录。以上 App 查询不自动升级为订单终态，也不触发重新创建。

## 本地构建和显式选择

```sh
go build -mod=vendor -o bridge-order ./cmd/bridge-order
go build -mod=vendor -o legacy-order-guard ./cmd/legacy-order-guard
```

bridge 的订单功能须独立启用并设置固定绝对私密状态目录。只允许显式 `http://127.0.0.1:PORT`、`http://[::1]:PORT` 或 `localhost` endpoint；禁用代理和重定向，不接受远端地址、URL 凭据或 query。`localhost` 固定拨号到 `127.0.0.1`。不要使用在线监控账号作候选登录测试。

以下全部是占位参数；prepare 本身只读，submit 会创建待付款订单，必须明确加 `--confirm`。

```sh
./bridge-order prepare \
  --endpoint http://127.0.0.1:18778 \
  --account-alias CANDIDATE_ALIAS --cusid ACCOUNT_ID --device-no DEVICE_NO \
  --request-key operation_20261015_001 --timeout 20s \
  --activity-id 123456 --ticket-id TICKET_ID --quantity 2 \
  --unit-price 19.90 --total-amount 39.80 \
  --performer-ids '["PERSON_ID_1","PERSON_ID_2"]' \
  --order-type 1 --delivery electronic
```

检查返回的绑定和确认页，再用完全相同参数把 `prepare` 换成 `submit` 并加 `--confirm`。本版只支持 `order_type=1`、`electronic`、空 `coupon_id` 和 `address_id`。金额是最多两位小数、最多九位整数的正十进制字符串，精确要求 `unit_price * quantity == total_amount`；quantity 1..100。request key 为 8..128 位 `[A-Za-z0-9_-]`，票档、实名人和上游查询 key 为 1..128 位同字符集。

```sh
./bridge-order status \
  --endpoint http://127.0.0.1:18778 \
  --account-alias CANDIDATE_ALIAS --cusid ACCOUNT_ID --device-no DEVICE_NO \
  --request-key operation_20261015_001 --timeout 20s

./bridge-order recover \
  --endpoint http://127.0.0.1:18778 \
  --account-alias CANDIDATE_ALIAS --cusid ACCOUNT_ID --device-no DEVICE_NO \
  --request-key operation_20261015_001 --timeout 20s --wait --poll-interval 200ms
```

status 只读记录；recover 仅查询已持久化 job/core key。`--wait` 只有一个轮询层，一次最多一个请求，全程共用不超过 60 秒的 deadline。没有查询 key 时保留 `delivery_unknown` 并要求人工核对，不猜造 key。查询失败、pending、未找到或超时都不能释放账号的新提交权。相同 key/选择可读取既有状态；不同 key 遇到同 cusid 未决记录会被拒绝。

同绑定的 legacy 未决记录可通过 status/recover 展示开始时间与人工对账原因；recover 不对 legacy 调用 App 查询。Go 不把 legacy 终态视为 App 创建成功，也不会替 legacy record 发起新的创建。

发送后网络失败、读取失败、HTTP 错误、重定向、无效 JSON、冲突状态或绑定不匹配均返回 `delivery_unknown`，不会重发、重登后再发、改走 WAP 或自动支付。只有版本、request key、绑定和 `dispatch:not_started` 全部匹配的明确 bridge 拒绝合同，才能证明没有进入创建发送。JSON 结果写 stdout；技术失败写 stderr 并非零退出。submit 的 rejected/started/delivery_unknown 也非零退出；created 为零，queued 为零只表示已接受排队，尚未创建订单。status/recover 的零退出只表示成功读取状态。保留原 request key 查询，不能用换 key 解决未知。

## 回退必须保留订单事实

wrapper 与 Python 使用同一绝对状态目录和 `account-<sha256(cusid)>.lock` 的 `flock`。统一示例为 `/opt/miniprogram-order/shared/showstart/orders`；凭据在 `private`，状态在 `shared`。状态目录必须已存在且为 0700；lock/journal 为 0600。其直接父目录 `/opt/miniprogram-order/shared/showstart` 必须由管理员预先创建并完成持久化准备，且不可被其他用户写入。Python 只创建最后一级，不递归创建父级；Go guard 不自动创建任何目录。两端每次使用先 fsync 状态目录，再 fsync 直接父目录；任一步失败禁止发出/启动，重试也不能跳过同步。缺少父目录、权限异常或符号链接拒绝使用。锁文件 inode 不替换。wrapper 在持锁时扫描整份 v1 journal，损坏、未知版本、未决记录或重复 JSON key 均拒绝执行 child。

```sh
./legacy-order-guard \
  --state-root /opt/miniprogram-order/shared/showstart/orders \
  --account-alias CANDIDATE_ALIAS --cusid ACCOUNT_ID --device-no DEVICE_NO \
  --request-key rollback_20261015_001 --timeout 30m \
  -- /absolute/retained-original-binary
```

真正启动前写入 durable `backend:legacy,state:started` 和不可变开始时间，持锁覆盖 child 生命周期；退出后写 `delivery_unknown`。exit 0 也不是已创建/已拒绝的证据，后续提交继续阻断。child 使用独立进程组，取消或直接 child 退出时清理残留组员；只有清理、记录保存完成才释放锁。原 argv 不写入 journal，只保存 digest。文件 fsync、原子 rename 和目录 fsync 失败时拒绝启动。

`legacy-order-guard precheck` 只提供同锁预检，不能替代实际启动 wrapper。存在 started/delivery_unknown/queued 时可以恢复只读监控，但不能启动旧写入口。本版只接受有有效 order ID 的 created 持久化终态；缺少已取证的最终拒绝分类，因此磁盘中的 rejected 也阻断，HTTP 的明确发前 rejected 不写入 journal。人工核对流程必须另行落实，本版没有自动解除未知的按钮。

该约束适用于所有参与者共用固定 state root、受控入口、同一真实 cusid 的部署。wrapper 无法验证旧 binary 私密配置实际使用哪个账号；部署入口必须保证绑定正确。手动直启旧 binary、更换/删除状态目录、为同账号配置另一个 root、child 主动 setsid 或脱离进程组都超出控制范围。普通派生孙进程由 wrapper 的进程组清理覆盖。只读旧任务需由操作者检查配置确实关闭写入；不要把未审查的 binary 参数自动称为只读。wrapper 支持 Linux/macOS，不支持 Windows。

## 本次离线证据与上线前核验

测试只连接 localhost；没有真实账号、App/WAP 外网或真实订单请求。Go 测试覆盖丢失响应仅创建一次、4xx/5xx/redirect/畸形响应保持未知、串行 deadline 恢复、凭据一致快照与 stale token 丢弃、旧入口零请求、未知时旧 child 零执行及 exit0 仍阻断。Python→Go CLI 的配对测试另在 Python 仓执行。

上线前固定两仓 revision、native/runtime 版本、原 binary 哈希、账号和私密配置路径；停用目标账号其他写入口并核对未决记录。稳定监控的地址、状态和进程只有现场核验后才能称为未受影响；源码测试不代表生产已切换。检查 `account_blocked`、`delivery_unknown`、`queued`、`request_key_conflict` 和查询失败；任何未知都保持写入停用。首轮候选观察窗口及负责人由实际部署者指定，本次没有生产监测结论。
