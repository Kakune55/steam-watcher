# Steam Watcher

一个轻量的 Steam 好友状态采集器，使用 Go + Echo + DuckDB 构建。

它会定期采集好友在线状态、保存历史快照，并提供一个简洁的 Web 页面查看最新状态、采集记录和历史视图。

## 功能

- 定期采集 Steam 好友状态
- 手动触发采集
- 基于 DuckDB 的本地存储
- 简洁的 Web 面板
- 好友历史视图

## 配置

默认读取 `config.json`，也可以通过环境变量覆盖同名字段。

示例：

```json
{
  "listen_addr": ":8080",
  "steam_api_key": "your_steam_api_key",
  "steam_id": "your_steam_id64_or_vanity",
  "database_path": "steam_status.duckdb",
  "collect_interval_seconds": 300,
  "collect_on_start": true
}
```

常用环境变量：

- `CONFIG_PATH`
- `STEAM_API_KEY`
- `STEAM_ID64`
- `APP_ADDR`
- `DATABASE_PATH`
- `COLLECT_INTERVAL_SECONDS`
- `COLLECT_ON_START`

兼容旧配置：`DUCKDB_PATH` 也可以用来指定数据库路径。

## 运行

```bash
go run ./cmd/server
```

打开 `http://localhost:8080`。

## 假数据

如果想先看界面效果，可以写入一套演示数据：

```bash
go run ./cmd/fake-data
```

可选参数：

- `-db /path/to/demo.duckdb`

## Docker

构建：

```bash
docker build -t steam-watcher .
```

使用环境变量运行：

```bash
docker run --rm -p 8080:8080 \
  -e STEAM_API_KEY=your_key \
  -e STEAM_ID64=your_steam_id \
  -v $(pwd)/data:/data \
  steam-watcher
```

使用配置文件运行：

```bash
docker run --rm -p 8080:8080 \
  -v $(pwd)/config.json:/app/config.json:ro \
  -v $(pwd)/data:/data \
  steam-watcher
```
