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
- **运行时配置**：可在后台调整出口代理、重试次数、长上下文阈值、会话清理、账号巡检和详细日志等设置。

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

### 支持的模型

当前服务暴露以下基础模型，并同时提供对应的 `-thinking` 版本：

- `claude-sonnet-4-6`
- `claude-haiku-4-5-20251001`
- `claude-sonnet-5`
- `claude-sonnet-5-5`

实际可用性取决于账号权限和 Claude.ai 上游状态，请以 `GET /v1/models` 的返回结果为准。

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
