#!/bin/bash

# 秀动监控部署脚本
# 使用方法: ./deploy.sh [start|stop|restart|status|logs]

WORK_DIR="/path/to/go_showstart"  # 修改为你的实际路径
PROGRAM_NAME="showstart_monitor"
LOG_FILE="monitor.log"
PID_FILE="monitor.pid"

cd "$WORK_DIR" || exit 1

case "$1" in
  start)
    if [ -f "$PID_FILE" ]; then
      PID=$(cat "$PID_FILE")
      if ps -p "$PID" > /dev/null 2>&1; then
        echo "监控程序已在运行 (PID: $PID)"
        exit 1
      fi
    fi
    
    echo "启动监控程序..."
    nohup ./"$PROGRAM_NAME" > "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
    echo "监控程序已启动 (PID: $(cat $PID_FILE))"
    echo "查看日志: tail -f $LOG_FILE"
    ;;
    
  stop)
    if [ ! -f "$PID_FILE" ]; then
      echo "PID 文件不存在，程序可能未运行"
      exit 1
    fi
    
    PID=$(cat "$PID_FILE")
    echo "停止监控程序 (PID: $PID)..."
    kill "$PID"
    rm -f "$PID_FILE"
    echo "监控程序已停止"
    ;;
    
  restart)
    $0 stop
    sleep 2
    $0 start
    ;;
    
  status)
    if [ -f "$PID_FILE" ]; then
      PID=$(cat "$PID_FILE")
      if ps -p "$PID" > /dev/null 2>&1; then
        echo "监控程序正在运行 (PID: $PID)"
        echo "运行时长: $(ps -p $PID -o etime= | tr -d ' ')"
      else
        echo "PID 文件存在但程序未运行"
      fi
    else
      echo "监控程序未运行"
    fi
    ;;
    
  logs)
    tail -f "$LOG_FILE"
    ;;
    
  *)
    echo "使用方法: $0 {start|stop|restart|status|logs}"
    exit 1
    ;;
esac

exit 0


