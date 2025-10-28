# go_showstart
## 项目介绍
go_showstart是一个基于golang的秀动自动化工具，当前支持两种模式：

- **抢票模式**：按照既定时间并发请求，尝试下单购票；
- **监控模式（新增）**：定时调用秀动演出列表接口，监控艺人新演出与“支持定时购票”标签，并通过 Webhook 发送提醒。

## 注意事项：
    1.目前并没有识别所有的参数类型，只支持了部分参数类型，欢迎大家提交PR。
    2.本项目只是个辅助工具，不能保证一定能抢到票，只是提高抢票成功率
    3.存在一些活动开票前支持wap端抢票，但是开票后只能在app端抢票的情况，故千万不能只依赖本项目！

## 使用说明
1. 在 [Releases](https://github.com/staparx/go_showstart/releases)中，下载对应系统的可执行文件。
2. 参考`config.example.yaml`文件，在可执行文件同目录创建 `config.yaml`。
3. 根据需求选择开启 **ticket**（抢票）或 **monitor**（监控）配置；两者可独立启用。
4. 运行可执行文件：
   - 若开启抢票模式，程序会在达到开始时间后自动尝试下单；
   - 若开启监控模式，程序会常驻轮询，日志中可看到每次检测结果。
5. 抢票模式使用前，请登陆 App 提前填写观演人信息；若需邮寄，请设置默认地址。

## 配置说明

### system
- max_goroutine: 最大并发数
- min_interval: 最小请求间隔
- max_interval: 最大请求间隔

### showstart
1. [登陆秀动网页版](https://wap.showstart.com)
2. F12打开浏览器开发者工具，刷新页面找到请求信息。如图：
![img.png](./docs/img.png)
3. 切换到缓存选项卡，找到其他对应字段信息，填写到配置中，如图：
![img_1.png](docs/img_1.png)


### ticket
- activity_id: 活动id（进入到活动后，可以通过url查看到ID）
- start_time: 抢票时间
- list: 抢票信息，
  - session：场次
  - price：价格
- people:观演人姓名


### smtp_email
- enable: 1 开启 0 关闭
- host: `"smtp.qq.com"` 邮箱服务器
- username: `"...@qq.com"` SMTP邮箱
- password: `""`  SMTP邮箱服务授权码
- email_to: `"...@qq.com"` 接收消息邮箱

### monitor（新增监控模式）
- enable: 1 开启 0 关闭；开启后主程序进入监控模式。
- keywords: 需要监控的艺人关键词列表，程序会逐个轮询。
- city_code: 城市编码，默认为 `99999` 表示“全国”。
- interval_seconds: 轮询周期，单位秒，默认 180（3 分钟）。
- webhook_url: 接收通知的 Webhook 地址（飞书/钉钉/等，文本格式）。
- state_dir: （可选）状态文件目录，默认 `monitor_state`，用于记录已通知的演出。

#### 监控通知逻辑
- **新演出上架**：检测到列表中存在未记录的 `activityId`，立即发送“新演出上架”通知；
- **定时购开启**：发现 `otherLabels` 中包含 `{"name":"支持定时购票"}`，且此前未通知过该演出，即发送“定时购已开启”通知；
- 状态文件会在每次成功通知后更新，防止重复推送。


## 自主开发
介绍部分参数定义，方便大家自主开发。调用秀动的接口方法在`client`文件夹中。
### cusat和cusid
- 代码：/client/service.go/GetToken()
- 返参：`access_token`用于Header的cusat`id_token`用于Header的cusid

### traceId
- 代码：/util/trace.go/GenerateTraceId()
- 返参：`traceId`用于Header的traceId
- 解释：`traceId`是一个唯一标识，用于标识请求的唯一性，具体生成方式可以参考代码。由随机数和时间戳组成。

### crpsign
- 代码：/util/sign.go/GenerateSign()
- 返参：`crpsign`用于Header的crpsign
- 解释：`crpsign`是一个加密字段，用于校验请求的合法性，具体加密方式可以参考代码。每次请求时都需要将cusat、cusid、traceId、path等字段拼接后进行Md5加密，得到`crpsign`字段。

### 特殊请求
- 代码：/util/aes.go/AESEncrypt()
- 返参：`data`字段内容
- 解释：部分请求需要对请求参数进行加密，具体加密方式可以参考代码。需要将请求参数进行AES加密，得到`data`字段内容。

### 代补充参数
- 代码：/vars/const.go
- 解释：
  - EncryptPathMap：特殊请求，需要加密的请求路径。
  - NeedCpMap：根据请求返参的buy_type，判断是否需要填写观演人信息的票务类型。
  - NeedAdress: 根据请求返参的TicketType，判断是否需要填写收货地址的票务类型。
  - SaleStatusMap：未使用，记录了已知的返参值对应的票务状态。


## 写在最后
本软件免费使用，请勿轻信他人。希望大家能原价购买到心仪的票，见到自己想见的人。

```diff
- 请勿使用本软件用于获利 本软件仅用于学习使用
- 希望大家能够遵守相关法律法规，不要用于非法用途。
```


## 贡献者
因原作者本身工作原因，无法及时更新，特此开源，希望大家能够一起完善。

感谢提交PR的小伙伴们

<a href="https://github.com/staparx/go_showstart/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=staparx/go_showstart" />
</a>