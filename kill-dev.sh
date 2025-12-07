#!/bin/bash

# 定义清理函数，确保脚本退出时删除临时文件
cleanup() {
    if [ -f "$tmp_file" ]; then
        rm -f "$tmp_file"
    fi
}
trap cleanup EXIT

echo "=== Wails进程管理器 (macOS版) ==="
echo ""

# 创建临时数组存储进程信息
pids=()
commands=()
index=1

# 使用临时文件存储进程列表
tmp_file=$(mktemp)
# 排除 grep 自身、当前脚本 和 kill_wails 关键词
ps -awf | grep -i wails | grep -v grep | grep -v "$0" | grep -v "kill_wails" > "$tmp_file"

echo "正在扫描进程..."

# 读取进程信息
while IFS= read -r line; do
    if [[ -n "$line" ]]; then
        # 提取PID (第2列)
        pid=$(echo "$line" | awk '{print $2}')
        # 提取完整命令 (从第11列开始，适配macOS ps输出)
        cmd=$(echo "$line" | cut -d' ' -f11-)
        
        # 存储到数组
        pids[index]=$pid
        commands[index]=$cmd
        index=$((index + 1))
    fi
done < "$tmp_file"

echo "找到以下进程："
echo "------------------------------------------------------------------------"
echo "编号 | PID     | 命令"
echo "------------------------------------------------------------------------"

# 显示进程列表
for ((i=1; i<index; i++)); do
    printf "%-4s | %-7s | %s\n" "$i" "${pids[i]}" "${commands[i]}"
done

echo "------------------------------------------------------------------------"

if [ $index -eq 1 ]; then
    echo "没有找到wails进程！"
    exit 0
fi

# 显示操作选项
echo ""
echo "操作选项:"
echo "  [数字]       - 选择单个进程 (如: 1)"
echo "  [范围]       - 选择多个进程 (如: 1-3)"
echo "  [多个]       - 选择多个进程 (如: 1,3,5)"
echo "  all          - 终止所有进程"
echo "  q            - 退出"
echo "  [空格分隔]   - 选择多个 (如: 1 3 5)"
echo ""

# 读取用户输入 (关键修改：添加 -e 参数)
read -e -p "请选择要终止的进程: " selection

case "$selection" in
    [qQ]uit|[qQ])
        echo "已取消操作"
        exit 0
        ;;
    [aA]ll|[aA])
        read -e -p "确定要终止所有 $((index-1)) 个进程吗？(y/n): " confirm
        if [[ $confirm == [Yy]* ]]; then
            echo "正在终止所有进程..."
            for pid in "${pids[@]}"; do
                if [ -n "$pid" ]; then
                    kill -9 "$pid" 2>/dev/null && echo "✅ 已终止进程 $pid" || echo "❌ 无法终止进程 $pid"
                fi
            done
            echo "✅ 操作完成！"
        else
            echo "操作已取消"
        fi
        ;;
    *)
        # 处理各种输入格式
        selected_pids=()
        
        # 预处理输入：将逗号替换为空格，以便统一处理
        selection=${selection//,/ }
        
        # 将输入转换为数组
        read -ra input_indices <<< "$selection"

        for item in "${input_indices[@]}"; do
            # 处理范围 (如 1-3)
            if [[ "$item" =~ ^[0-9]+-[0-9]+$ ]]; then
                start=${item%-*}
                end=${item#*-}
                for ((i=start; i<=end; i++)); do
                    if [ -n "${pids[i]}" ]; then
                        selected_pids+=("${pids[i]}")
                    fi
                done
            # 处理单个数字
            elif [[ "$item" =~ ^[0-9]+$ ]]; then
                if [ -n "${pids[$item]}" ]; then
                    selected_pids+=("${pids[$item]}")
                fi
            fi
        done
        
        if [ ${#selected_pids[@]} -eq 0 ]; then
             echo "❌ 未选择有效进程或输入格式错误"
             exit 1
        fi
        
        # 显示选择的进程
        echo ""
        echo "选择了以下进程:"
        for pid in "${selected_pids[@]}"; do
            # 查找对应的命令用于显示
            for ((i=1; i<index; i++)); do
                if [ "${pids[i]}" = "$pid" ]; then
                    echo "  PID $pid: ${commands[i]}"
                    break
                fi
            done
        done
        
        read -e -p "确定要终止这些进程吗？(y/n): " confirm
        
        if [[ $confirm == [Yy]* ]]; then
            echo "正在终止进程..."
            for pid in "${selected_pids[@]}"; do
                kill -9 "$pid" 2>/dev/null && echo "✅ 已终止进程 $pid" || echo "❌ 无法终止进程 $pid (可能已退出)"
            done
            echo "✅ 操作完成！"
        else
            echo "操作已取消"
        fi
        ;;
esac