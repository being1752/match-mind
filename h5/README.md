# MatchMind H5

移动端优先的 Vue 3 H5，复用现有 Go API，包含登录注册、信息录入、批次轮询、投资需求、项目需求、详情匹配、重复处理和合并撤销。

## 本地运行

```powershell
cd C:\code\Go\match-mind-h5
npm install
npm run dev
```

打开 `http://127.0.0.1:5173`。开发服务器会将 `/api` 代理到 `http://127.0.0.1:8080`。

生产构建：

```powershell
npm run build
```

部署 `dist` 目录，并通过 `VITE_API_BASE_URL` 指向后端 `/api/v1` 地址。H5 使用 history 路由，Web 服务器需将未知页面路径回退到 `index.html`。
