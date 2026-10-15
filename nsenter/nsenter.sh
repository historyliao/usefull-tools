#!/usr/bin/env bash

NAMESPACE="default"
POD_NAME=""

usage() {
    echo "用法: $0 [-n <namespace>] <pod_name> <command> [args...]"
    echo "示例: $0 -n default lyytest2 tcpdump -i eth0 -ennvvv icmp"
    echo "      $0 lyytest2 ip a"
    exit 1
}

# 解析 -n 参数
while getopts "n:h" opt; do
    case "${opt}" in
        n) NAMESPACE="${OPTARG}" ;;
        h|*) usage ;;
    esac
done
shift $((OPTIND -1))

if [ $# -lt 2 ]; then
    echo "错误: 缺少 Pod 名称或要执行的命令"
    usage
fi

POD_NAME="$1"
shift  # 剩余参数为待执行命令及选项

# 1. 获取 Container ID
CONTAINER_ID=$(crictl ps | grep "${POD_NAME}" | grep "${NAMESPACE}" | awk '{print $1}' | head -n 1)

if [ -z "${CONTAINER_ID}" ]; then
    echo "========================================================"
    echo "错误: 当前节点上无法通过 crictl ps 找到包含 '${POD_NAME}' 的容器！"
    echo "========================================================"
    exit 1
fi

# 2. 多重保障提取宿主机真实 PID (适配不同版本的 JSON 结构)
PID=""

# 方法 A: 尝试使用 Python 解析 JSON (Linux 节点普遍自带 python3 或 python)
if command -v python3 &>/dev/null; then
    PID=$(crictl inspect "${CONTAINER_ID}" | python3 -c 'import sys, json; data=json.load(sys.stdin); print(data.get("info", {}).get("pid", ""))')
elif command -v python &>/dev/null; then
    PID=$(crictl inspect "${CONTAINER_ID}" | python -c 'import sys, json; data=json.load(sys.stdin); print(data.get("info", {}).get("pid", ""))')
fi

# 方法 B: 若没有 Python，尝试 jq
if [ -z "${PID}" ] && command -v jq &>/dev/null; then
    PID=$(crictl inspect "${CONTAINER_ID}" | jq -r '.info.pid // empty')
fi

# 方法 C: 兜底逻辑，直接正则匹配 info 字段内部的 "pid": 123456
if [ -z "${PID}" ]; then
    PID=$(crictl inspect "${CONTAINER_ID}" | grep -o '"pid":[0-9]*' | grep -o '[0-9]*' | awk '$1 > 1' | head -n 1)
fi

if [ -z "${PID}" ] || [ "${PID}" -eq 1 ]; then
    echo "========================================================"
    echo "错误: 提取容器 ${CONTAINER_ID:0:12} 的宿主机 PID 失败！"
    echo "提示: 请运行 'crictl inspect ${CONTAINER_ID:0:12} | grep pid' 检查 JSON 输出格式。"
    echo "========================================================"
    exit 1
fi

echo "==> 目标 Pod: ${NAMESPACE}/${POD_NAME} (Container: ${CONTAINER_ID:0:12}, PID: ${PID})"
echo "==> 执行命令: $@"
echo "--------------------------------------------------------"

# 3. 进入网络命名空间执行命令
exec nsenter -t "${PID}" -n "$@"
