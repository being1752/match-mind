# MatchMind 内部录入与匹配 MVP

当前版本包含：

- 用户名、密码注册与登录，JWT 鉴权
- 原文先落库、后台异步整理，关闭页面不影响处理
- 按账号查看录入历史、原文、状态和整理结果
- 草稿自动保存、处理中任务自动恢复和幂等提交
- 超时卡死的处理中任务自动重新排队
- 批量文字切分、买卖方向判断、结构化入库
- 本地候选分段、AI原子分组、分组后批量结构化及覆盖率校验
- 原始信息永久保留，并记录上传账号和时间
- 同账号完全重复自动关联，同账号高置信语义重复自动合并
- 同账号中等置信重复候选由当前账号人工确认
- 不同账号的相同信息保留独立实体，投资需求与项目需求仍参与集团全局匹配
- 合并事件、实体版本和来源时间线
- Python Embedding 接口、PostgreSQL 向量任务和 pgvector 存储
- 结构化规则分与向量相似度组合匹配

## 启动

数据库连接由根目录 .env 的 DATABASE_URL 提供。Go 启动时自动执行增量迁移：

~~~powershell
go run .\cmd\server
~~~

Python AI 服务：

~~~powershell
cd ai-service
.\.venv\Scripts\python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 8090
~~~

结构化录入采用两阶段AI流程：候选分段先发送给配置的大模型判断原子边界，随后原子项再次发送进行字段提取。完整原文和字符位置由本地维护，模型不能改写最终入库原文。由于存在两次串行调用，Go侧 `AI_REQUEST_TIMEOUT` 默认设置为 `240s`。

AI服务调试接口：

~~~text
POST /v1/segments          # 仅本地候选分段，不调用外部AI
POST /v1/atomic-groups     # AI原子分组
POST /v1/structure-groups  # 已分组原子项批量结构化
POST /v1/structure         # 完整两阶段流程，Go后端使用
~~~

## Embedding 配置

向量外发默认关闭。先在 ai-service/.env 配置兼容 OpenAI Embeddings 的服务：

~~~env
EMBEDDING_API_BASE=https://dashscope.aliyuncs.com/compatible-mode/v1
EMBEDDING_API_KEY=你的Key
EMBEDDING_MODEL=qwen3.7-text-embedding
EMBEDDING_DIMENSIONS=1024
~~~

确认集团数据允许发送到该服务后，在根目录 .env 设置：

~~~env
EMBEDDING_ENABLED=true
~~~

重新启动 Python 和 Go。Go 启动时会补齐缺失向量，也可以登录后调用：

~~~http
POST /api/v1/embeddings/backfill
POST /api/v1/matches/rebuild
Authorization: Bearer <token>
~~~

`POST /api/v1/matches/rebuild` 使用现有结构化数据和向量重新生成全部匹配解释，不会调用大模型或重新生成向量。

不同模型的向量不能混合使用；更换模型后需要重新生成全部向量。

## 主要接口

~~~text
POST /api/v1/auth/register
POST /api/v1/auth/login
GET  /api/v1/auth/me

POST /api/v1/ingestion-batches
GET  /api/v1/ingestion-batches          # 当前账号录入历史
GET  /api/v1/ingestion-batches/{id}

GET  /api/v1/entities/{type}/{id}
GET  /api/v1/entities/{type}/{id}/matches

GET  /api/v1/duplicate-candidates
POST /api/v1/duplicate-candidates/{id}/merge
POST /api/v1/duplicate-candidates/{id}/reject
POST /api/v1/merge-events/{id}/revert
~~~

除了注册和登录，其余 /api/v1 请求都必须携带 JWT。

## 测试

~~~powershell
go test ./...
cd ai-service
.\.venv\Scripts\python.exe -m pytest
~~~

合并集成测试默认跳过；显式连接本地测试数据库时运行：

~~~powershell
$env:RUN_DB_INTEGRATION='1'
go test ./internal/database -run 'TestMergeEntitiesIntegration|TestVectorMatchingIntegration' -v -count=1
~~~

测试会创建带独立标记的临时数据，并在结束时精确清理。
