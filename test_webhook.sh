#!/bin/bash

# 测试 Webhook 通知脚本
WEBHOOK_URLS=("https://hook.echobell.one/t/lzr4ohbdn38hbp1zp29c" "https://hook.echobell.one/t/fhb0hnji7lwo1396a8cw")

for WEBHOOK_URL in "${WEBHOOK_URLS[@]}"; do
  echo "🧪 测试 1: 发送新演出通知 -> ${WEBHOOK_URL}"
  curl -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -d '{
      "type": "new",
      "artist": "汉堡黄",
      "title": "测试演出 - 汉堡黄「少女，飞」专场巡演",
      "showTime": "2025.12.01 周日 20:00",
      "siteName": "测试剧院",
      "url": "https://wap.showstart.com/pages/activity/detail/detail?activityId=123456"
    }'
done

for WEBHOOK_URL in "${WEBHOOK_URLS[@]}"; do
  echo -e "\n\n🧪 测试 2: 发送定时购通知 -> ${WEBHOOK_URL}"
  curl -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -d '{
      "type": "timed",
      "artist": "五月天",
      "title": "测试定时购 - 五月天演唱会",
      "showTime": "2025.12.15 周日 19:30",
      "siteName": "测试体育馆",
      "url": "https://wap.showstart.com/pages/activity/detail/detail?activityId=789012"
    }'
done

echo -e "\n\n✅ 测试完成！请检查你的通知接收端。"
