# Bird

一个轻量、可配置的多实例 FTP 服务程序。  
Bird 会从 JSON 配置文件读取多个 FTP 实例，并并发启动它们。每个实例可以绑定独立端口、根目录和用户列表。

## 特性

- 支持单进程并发启动多个 FTP 实例
- 每个实例可配置独立目录、端口、用户名和密码
- 启动时自动创建目录
- 内置多用户认证
- 比较美观的终端日志

## 目录结构

- main.go：程序入口
- config.json：示例配置
- go.mod / go.sum：依赖管理

## 快速开始

### 1. 准备配置文件

默认读取项目根目录下的 config.json。配置文件格式如下：

```json
[
	{
		"port": 2121,
		"dir": "./ftp_data/instance_1",
		"backup_files": ["watched.txt", "docs/report.csv"],
		"ignored_files": ["config.txt", "admin/settings.ini"],
		"public_ip": "203.0.113.10",
		"passive_ports": "30000-30100",
		"users": {
			"alice": "123456",
			"bob": "654321"
		}
	},
	{
		"port": 2222,
		"dir": "./ftp_data/instance_2",
		"public_ip": "203.0.113.10",
		"passive_ports": "30101-30200",
		"users": {
			"guest": "guest"
		}
	}
]
```

### 2. 运行

```bash
go run main.go
```

或指定配置文件：

```bash
go run main.go -config ./config.json
```

如果你已经编译了二进制文件：

```bash
./bird -config ./config.json
```

## 日志风格

Bird 在终端中使用 ANSI 颜色输出，主色调为黄绿：

- [INFO]：绿色
- [OK]：亮黄绿色
- [WARN]：黄色
- [ERROR]：红色

## 参数说明

- -config：配置文件路径，默认值为 config.json

## 配置字段说明

- `port`：FTP 控制连接端口（需要做端口映射）
- `dir`：该实例的根目录
- `users`：用户与密码映射
- `public_ip`：服务器对外暴露的公网 IP（NAT/端口映射场景必须配置）
- `passive_ports`：被动模式端口范围，例如 `30000-30100`（该范围也必须全部做端口映射）
- `backup_files`：可选字段，数组。用于配置多个需要监控并备份的文件（相对于 `dir` 的路径，如 `watched.txt`、`docs/report.csv`）。当任意用户通过 FTP 修改这些文件时，会先在该文件同级目录自动创建 `backup` 目录，保存修改前的旧文件，命名格式为 `原始名称 (日期).扩展名`，例如 `report (2026-04-18 02-30-45).csv`
- `ignored_files`：可选字段，数组。指定会被忽略的文件列表（相对于 `dir` 的路径）。客户端无法上传、修改、删除或重命名这些文件，尝试操作时会静默忽略（不返回错误，但实际不执行任何操作）

## 注意事项

- 请确保端口未被占用
- 请确保程序对目标目录有读写权限
- 外网访问 FTP 时，除了 `port` 外，还必须映射 `passive_ports` 全部端口范围
- 用户密码目前为明文配置，建议仅在受信任环境使用

## 依赖

- github.com/goftp/server
- github.com/goftp/file-driver

## License

仅用于学习与内部工具场景，可按需自行扩展。
