<h1 align="center">Claude2API</h1>

<p align="center">Claude2API 是一个基于 Go 开发的 Claude.ai API 兼容网关、账号池与网页镜像服务。项目通过统一管理多个 Claude.ai 账号并自动轮询可用账号，将 Claude 模型能力以 OpenAI API 和 Anthropic API 兼容接口的形式提供给第三方客户端、自动化脚本与开发工具。Claude2API 支持 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 接口，兼容 Claude Code、Codex CLI 以及支持自定义 Base URL 的应用；同时提供流式输出、多轮对话、System Prompt、多模态图片输入、扩展思考（Thinking）、Function Calling / Tool Use、长上下文处理、会话清理、API Key 鉴权、调用日志和可视化后台管理。项目支持 Docker Compose 一键部署，适合个人学习、接口适配、客户端联调和 Claude API 集成测试。</p>

> [!WARNING]
> 免责声明：
>
> 本项目涉及对 Claude.ai 官网相关能力的逆向研究，仅供个人学习、技术研究与非商业性技术交流使用。
>
> - 严禁将本项目用于任何商业用途、盈利性使用、批量操作、自动化滥用或规模化调用。
> - 严禁将本项目用于破坏市场秩序、恶意竞争、套利倒卖、二次售卖相关服务，以及任何违反 Anthropic 服务条款或当地法律法规的行为。
> - 严禁将本项目用于生成、传播或协助生成违法、暴力、色情、未成年人相关内容，或用于诈骗、欺诈、骚扰等非法或不当用途。
> - 使用者应自行承担全部风险，包括但不限于账号受限、临时封禁、永久封禁以及因违规使用导致的法律责任。
> - 本项目依赖 Claude.ai 上游接口，上游接口、风控策略及页面结构的变化都可能导致部分功能失效。
> - 使用本项目即视为你已充分理解并同意本免责声明；请勿使用重要账号、常用账号或高价值账号进行测试。

## 快速开始

Docker Compose 一键部署

```bash
git clone https://github.com/basketikun/claude2api.git
cd claude2api
cp config.example.yaml config.yaml
docker compose up -d
```

## 版本与更新提示

根目录 `VERSION` 保存当前版本，格式为 `v1.0.0`，编译时嵌入程序。发布版本时修改此文件，重新编译或构建镜像；管理页侧栏底部显示运行中的版本，并提供 GitHub 入口。

点击侧栏版本号打开「版本更新」，查看当前版本、最新版本和按版本排列的更新记录。记录来自随程序打包的根目录 `CHANGELOG.md`，不依赖 GitHub 访问；弹窗底部可前往 GitHub 项目仓库。

维护 `CHANGELOG.md` 时，新版本排在前面：二级标题使用 `## 未发布` 或 `## v1.0.0 · 2026-08-31`，三级标题使用 `### 新增`、`### 修复`、`### 调整`，各项改动使用纯文本无序列表。发布时把「未发布」内容移到对应版本标题下，并同步修改 `VERSION`。

程序每小时最多读取一次 GitHub `main` 分支的 `VERSION`，按语义化版本比较；远程版本更高时显示绿色圆点和新版本号。读取失败时仍显示当前版本，悬停版本号可查看检查状态，不会误报更新，也不会自动升级。只有将 `VERSION` 发布到主分支后，远程检查才能正常取得版本。

GitHub 图标使用[官方品牌资源](https://brand.github.com/foundations/logo)，已保存为 `web/github.svg`，页面从本地加载。

## 核心功能

- **官网镜像**：提供 Claude.ai 官网镜像和账号池选择页面，可随机或指定可用账号进入镜像站，并自动维护访问所需的 Cookie、浏览器指纹与会话信息。
- **号池管理**：支持批量导入 Claude.ai 账号，自动获取邮箱和组织信息，并提供账号状态查看、手动刷新、失效清理与定期巡检。
- **账号轮询**：API 请求自动轮询可用账号，请求失败时可按配置换号重试，避免单个账号异常影响服务。
- **OpenAI 兼容**：支持 `POST /v1/chat/completions` 和 `POST /v1/responses` 接口，可接入 OpenAI SDK 及支持自定义 Base URL 的客户端。
- **Anthropic 兼容**：支持 `POST /v1/messages` 接口，可接入 Anthropic SDK、Claude Code 等兼容客户端。
- **开发工具接入**：支持配置 Codex CLI、Claude Code 使用本服务的兼容 API。
- **模型列表**：提供 `GET /v1/models` 接口，统一返回当前支持的基础模型及 Thinking 模型。
- **流式响应**：OpenAI 与 Anthropic 接口均支持流式和非流式输出。
- **多轮对话**：按客户端会话固定账号和普通网页对话，使用真实父消息 UUID 续聊，仅上传新增用户消息和工具结果；会话状态持久化到 SQLite。
- **多模态输入**：支持 OpenAI 与 Anthropic 格式的 Base64 图片输入，并自动上传至 Claude.ai。
- **扩展思考**：支持独立的 `thinking` 和 effort 参数，也兼容模型名的 `-thinking` 后缀；显式思考设置优先于后缀。
- **工具调用**：支持 OpenAI `tools / tool_calls`、Responses `function_call` 和 Anthropic `tools / tool_use`，兼容流式、非流式及工具结果回传。
- **长上下文处理**：提示词超过配置阈值时自动转换为文本附件，减少超长上下文直接提交造成的问题。
- **会话清理**：活跃对话保留，闲置超过配置时间后清理；额度耗尽换号时重建历史，短期限速等待原账号。
- **密钥管理**：可在后台创建、查看和删除多个 API 密钥，用于号池、镜像和 API 接口鉴权。
- **在线测试**：后台内置多轮对话测试页面，支持选择模型、流式输出和图片上传。
- **调用日志**：记录调用时间、接口、模型、使用账号、响应状态、输入输出 Token、总耗时、首字延迟和 TPS；列表直接显示推理强度。TPS 按估算输出 Token 除以总耗时计算，重复请求复用不计生成速度。
- **日志详情**：可选保存完整请求与响应内容，支持查看详情、批量删除及仅保留最近指定数量的日志。
- **数据概览**：按日期查看估算 Token 趋势、请求成功率、首字时间、调用来源、模型及账号用量，以及近 180 天请求活跃度。
- **运行时配置**：可在后台调整出口代理、重试次数、长上下文阈值、会话清理、账号巡检和详细日志等设置。

## 账号用量

账号列表、详情和号池卡片显示最近记录的 5 小时、7 天已用比例及重置时间，时间按浏览器所在时区展示。数据来自对话的 `message_limit` 事件，通过本服务的 API 或官网镜像发送对话时采集，不额外发送消息查询额度。刷新列表只能读取最近记录；从未收到用量事件时显示“暂无记录”，超过重置时间后保留原值并提示待更新。

字段依据 2026-10-29 用户从 `POST /api/organizations/{组织}/chat_conversations/{会话}/completion` 捕获的[真实响应](internal/service/testdata/message_limit.json)：读取 `message_limit.windows` 中的 `utilization`（0.16 表示已用 16%）和 `resets_at`（Unix 秒）。这份响应的顶层 `resetsAt` 为 `null`，不用于重置时间展示。

已有数据库可在升级启动前执行一次迁移：

```bash
sqlite3 data/app.db < migrations/20261029_add_account_usage.sql
```

启动时的 AutoMigrate 也会补齐这两个字段；已由启动补齐的数据库不要重复执行 SQL。迁移脚本保留到确认线上升级完成。

## Token 用量记录

2026-10-29 实测普通对话、启用思考模式的对话及会话详情，均未返回输入、输出、缓存或思考 Token 计数。保留的[真实对话流](internal/service/testdata/completion.sse)包含模型、文本事件、额度窗口和结束事件，不能从额度百分比反推 Token 数。

新增记录使用 `tiktoken-go/tokenizer` 的 `cl100k_base` 参考词表估算文本，不再按 UTF-8 字节数除以 4。该词表属于 OpenAI，不能保证比字节估算更接近当前 Claude 的真实用量；[Anthropic 公开的旧分词器](https://github.com/anthropics/anthropic-tokenizer-typescript)也明确不适用于 Claude 3 及更新模型。只有实际模型返回的用量才能作为计费依据，不以本地分词结果冒充官方统计。

输入逐项计算实际发送的提示词、文本附件及风格提示；输出按内容块拼齐后计算回答、工具参数和可见思考，避免流式分片改变统计结果。概览和日志分别保存「回答与工具」「可见思考」，总量只相加一次；兼容接口的 `output_tokens` / `completion_tokens` 包含这两项，并复用同一份采集记录，不把本地添加的 `<think>`、Markdown 包装或错误提示算作模型输出。Chat 流式请求可用 `stream_options.include_usage: true` 接收最终用量；Messages 流式在 `message_delta.usage` 返回最终输入、输出累计值。

不计图片、缓存、隐藏思考、网页已有对话历史、上游内部提示和未公开的工具开销；未返回思考文本时的 0 仅代表没有采集到可见思考，不代表实际思考消耗为 0。历史记录保留原估算值，缺少完整原始内容不能重新分词。兼容接口和管理统计均为估算，不用于对账或计费。

API 调用和镜像对话的统计独立保存在 `usage_records`，按账号、模型和调用来源关联；删除或清理调用日志不会删除用量记录。API 调用的记录范围是一轮公开请求，镜像的记录范围是一轮 completion 请求，不回填升级前未记录的历史。账号详情显示全时段累计用量。

管理页默认进入「数据概览」，支持今天、7 天、30 天、90 天和全部范围。模型与账号用量分别分页；缓存用量显示“未提供”，不推算缓存命中率或费用。请求活跃度固定展示近 180 天。

管理查询：`GET /api/usage?days=30&timezone_offset=-480&group=model&offset=0&limit=10`，沿用管理鉴权。`days=0` 为全部，正数从当地当天零点回溯指定天数；`timezone_offset` 使用浏览器 `getTimezoneOffset()` 的分钟值；`group` 为 `account` 或 `model`，可传 `account` 精确筛选邮箱。返回汇总、每日趋势、分组分页、来源分布及近 180 天活跃度，明确标注 `token_source: estimated`，`current_token_method: cl100k_base` 仅说明新增记录的方法，汇总可能包含旧估算；`cache_tokens: null` 表示未提供。

已有数据库执行 `migrations/20261029_add_token_statistics.sql`，或由启动 AutoMigrate 补齐，不能重复执行 SQL。本地已执行的迁移仍保留，供其他环境升级。详细日志保留文本和参数，Base64 媒体内容替换为类型及长度信息。

## 效果展示

<table width="100%">
  <tr>
    <td width="50%"><img src="https://i.ibb.co/3yMvBm0R/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/zhpt12gG/image.png" alt="image" border="0"></td>
  </tr>
  <tr>
    <td width="50%"><img src="https://i.ibb.co/XZ4G85yp/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/LhxVLkYN/image.png" alt="image" border="0"></td>
  </tr>
  <tr>
    <td width="50%"><img src="https://i.ibb.co/99v19T70/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/ZpYzPkb9/image.png" alt="image" border="0"></td>
  </tr>
  <tr>
    <td width="50%"><img src="https://i.ibb.co/1Y0C0Nyq/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/rK773GJR/image.png" alt="image" border="0"></td>
  </tr>
</table>

## 账号导入

进入管理后台的「账号管理」，点击「批量导入」，每行填写一个 `sessionKey`：

```text
sk-ant-sid01-xxxxxxxx
sk-ant-sid01-yyyyyyyy
```

导入任务会在后台执行，并自动查询账号信息。也可以在导入窗口选择或粘贴 JSON，支持账号数组、单个账号对象或含 `accounts` 数组的对象，凭据字段支持 `sessionKey`、`session_key` 或 `cookies.sessionKey`。JSON 账号对象的全部原始字段会保存。

账号管理页的「导出全部」会下载所有账号的 JSON，包含登录凭据，可再次导入。旧账号导出数据库中已有的邮箱、组织 UUID、sessionKey 和 cookies；新 JSON 导入的账号导出保存的原始对象。`sessionKey` 等同于账号登录凭据，请妥善保管导出文件。

本地运行时，每个账号还会同步保存为项目 `auth/<邮箱>.json` 文件，内容与账号导出一致。已有账号会在启动时自动生成文件；导入、刷新和删除会同步更新文件。数据库仍是运行时账号索引，并保存会话与日志。备份 JSON 可通过账号管理页重新导入，不会自动扫描手动放入的文件。

Docker 镜像把 auth 目录放在 `/app/data/auth`，与现有 data 挂载一起持久保存。可通过环境变量 `CLAUDE2API_AUTH_DIR` 指定其他目录。`auth/` 已排除 Git 和镜像构建上下文。

### 支持的模型

`GET /v1/models` 汇总可用账号当前套餐支持的模型。Claude.ai `GET /api/account` 的 `memberships[].organization.claude_ai_bootstrap_models_config` 是通用目录，不能直接当成账号权限：过滤 `inactive: true` 后，还需结合 `analytics_subscription_plan` 筛选。Free 仅保留 Sonnet、Haiku，Opus、Fable 属于付费套餐，依据 [Claude 官方套餐说明](https://academy.claude.com/tutorials/choosing-the-right-claude-model)。仅为 `paprika_modes` 包含 `extended` 的模型提供 `-thinking` 版本。字段依据 2026-10-29 捕获的 [Free 账号真实响应](internal/service/testdata/account_models.json)。

导入账号、手动刷新账号和定期巡检都会同步套餐和模型目录。升级后服务启动时会自动为缺少记录的已有账号补刷一次；没有套餐或模型记录时返回空列表，不使用旧名单兜底。账号页、在线测试、`/v1/models` 和账号调度共用套餐筛选规则；实际调用仍受账号当前额度及组织权限限制。

账号详情页可逐个停用套餐目录内的模型（POST /api/accounts/models，body 为 `{"email","disabled":[...]}`）。停用后该账号不再参与这个模型及其 `-thinking` 版本的调度，也不计入 `/v1/models`；刷新账号会更新目录但保留停用列表。已有数据库字段迁移为 `migrations/20261103_add_account_disabled_models.sql`，或由启动 AutoMigrate 补齐。

已有数据库的模型字段迁移为 `migrations/20261029_add_account_models.sql`，套餐字段迁移为 `migrations/20261029_add_account_plan.sql`；启动时 AutoMigrate 也会补齐字段，两种方式选择一种，不能重复执行 SQL。

### Thinking 与 effort

Claude Code 使用基础模型名，并通过独立参数设置思考。例如 Sonnet 5.5 开启思考、effort 为 max 时：

```json
{
  "model": "claude-sonnet-5-5",
  "thinking": { "type": "adaptive", "display": "updates" },
  "output_config": { "effort": "max" }
}
```

Messages 的 `thinking.type=adaptive/enabled` 映射为网页端的 `thinking_mode=extended`；`output_config.effort` 映射为网页端 completion 的 `effort`。Chat Completions 使用 `reasoning_effort`，Responses 使用 `reasoning.effort`。支持 `low`、`medium`、`high`、`xhigh`、`max`，上游按模型和账号权限检查可用性。省略 effort 时使用网页默认值。显式思考设置优先于模型名后缀，单独指定 effort 会开启思考。

网页端和日志中的模型名仍是基础名称，`-thinking` 是本服务提供的别名。日志详情另列实际上游模型、thinking_mode 和 effort。网页返回的思考摘要会作为兼容文本显示；`thinking.display=omitted` 隐藏摘要，并保持上游思考开启。

<details>
<summary><code>GET /v1/models</code></summary>
<br>

获取当前服务暴露的模型列表：

```bash
curl http://localhost:8787/v1/models \
  -H "Authorization: Bearer <api-key>"
```

```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-sonnet-4-6",
      "object": "model",
      "created": 1750000000,
      "owned_by": "anthropic"
    }
  ]
}
```

</details>

<details>
<summary><code>POST /v1/chat/completions</code></summary>
<br>

OpenAI Chat Completions 兼容接口，支持多轮对话、图片输入以及流式输出。图片使用 Data URI：

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {
        "role": "system",
        "content": "回答尽量简短"
      },
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "描述这张图片"},
          {
            "type": "image_url",
            "image_url": {"url": "data:image/png;base64,iVBORw0KGgo..."}
          }
        ]
      }
    ],
    "stream": true
  }'
```

</details>

<details>
<summary><code>POST /v1/responses</code></summary>
<br>

OpenAI Responses API 兼容接口，`input` 支持字符串或消息数组：

```bash
curl http://localhost:8787/v1/responses \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "instructions": "回答尽量简短",
    "input": [
      {
        "role": "user",
        "content": [
          {"type": "input_text", "text": "描述这张图片"},
          {
            "type": "input_image",
            "image_url": "data:image/png;base64,iVBORw0KGgo..."
          }
        ]
      }
    ],
    "stream": false
  }'
```

</details>

<details>
<summary><code>POST /v1/messages</code></summary>
<br>

Anthropic Messages 兼容接口，可用于接入支持自定义 Anthropic Base URL 的客户端：

```bash
curl http://localhost:8787/v1/messages \
  -H "x-api-key: <api-key>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 1024,
    "system": "回答尽量简短",
    "messages": [
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "描述这张图片"},
          {
            "type": "image",
            "source": {
              "type": "base64",
              "media_type": "image/png",
              "data": "iVBORw0KGgo..."
            }
          }
        ]
      }
    ],
    "stream": true
  }'
```

</details>

### 客户端接入

Codex CLI 可通过自定义 OpenAI Base URL 使用 `/v1/responses`，Claude Code 可通过自定义 Anthropic Base URL 使用 `/v1/messages`；两者均填写本服务地址和后台创建的 API Key 即可。

### 网页会话续聊与 New API

默认开启 `session_reuse`，创建普通网页对话并持久化账号、对话 ID 和真实父消息 UUID。客户端继续传完整历史，网关核对已同步部分后只向 Claude.ai 发送新增消息和工具结果；历史压缩、回退、模型或工具定义变化时自动重建。

- Claude Code：自动读取 `metadata.user_id` 中的 `session_id`，也兼容旧版 `_session_` 标识。
- 通用客户端：每个聊天固定发送 `X-Claude2API-Session-ID`，或请求体 `session_id` / `metadata.session_id`。首次未提供时服务生成 ID，并在同名响应头返回；下一轮需要带回该 ID。
- Responses 客户端：也可使用稳定的 `prompt_cache_key` 识别会话，并发送完整历史。此处不依赖 `previous_response_id`。
- 重复请求：同一会话、相同历史及生成参数复用已完成结果，工具调用 ID 保持一致。主动重新生成可更换 `Idempotency-Key`。提交后流中断的同一请求会被阻止重复提交；使用新的该请求头可以重建。

New API 推荐配置 Claude 渠道，上游指向本服务，入口和上游都使用 `/v1/messages`，保留 `metadata`。多个后端时开启按会话的渠道亲和性，避免跨后端重试。自定义请求头可在支持该功能的版本中配置：

```json
{
  "X-Claude2API-Session-ID": "{client_header:X-Claude2API-Session-ID}",
  "Idempotency-Key": "{client_header:Idempotency-Key}"
}
```

普通对话在活跃期间不会立即删除。系统管理可设置闲置时间（默认 24 小时）、额度耗尽默认冷却时间（默认 30 分钟）、短期限速等待时间（默认 10 秒）。上游提供额度恢复时间时优先使用该时间。调用详情显示新建、续聊、换号恢复、重复请求复用，以及本轮实际发送的文本字节数。

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=basketikun/claude2api&type=Date)](https://www.star-history.com/#basketikun/claude2api&Date)

## 社区支持

学 AI，上 L 站：[Linux.do](https://linux.do)

交流群：点击链接加入群聊【开源无限画布(2群)】：https://qm.qq.com/q/Shgvco7XEu
