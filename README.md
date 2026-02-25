# GAL-CLI

A multi-agent CLI tool with tool/skill/MCP management and model switching.

## Features

- **Multi-agent** — define multiple agents with different system prompts, tools, and models; switch on the fly
- **Multi-provider** — OpenAI, Anthropic, DeepSeek, Ollama (any OpenAI-compatible API)
- **Tool calling** — built-in tools (`file_read`, `file_write`, `file_edit`, `file_list`, `grep`, `bash`) with agentic loop
- **Skills** — user-defined capability packs: prompt injection via `SKILL.md` + auto-registered script tools
- **MCP** — connect to remote tool servers via HTTP-based Model Context Protocol
- **Streaming** — real-time streamed responses from all providers

## Quick Start

```bash
# Build
make

# Initialize config
./gal-cli init

# Set API keys
export OPENAI_API_KEY="sk-..."
# and/or
export ANTHROPIC_API_KEY="sk-ant-..."

# Start chatting
./gal-cli chat
```

## Configuration

`gal-cli init` creates default configs at `~/.gal/`:

```
~/.gal/
├── gal.yaml              # providers & global settings
├── agents/
│   └── default.yaml      # agent definitions
└── skills/               # user-global skills
```

### Provider Config (`~/.gal/gal.yaml`)

```yaml
providers:
  openai:
    type: openai
    api_key: ${OPENAI_API_KEY}
    base_url: https://api.openai.com/v1
  anthropic:
    type: anthropic
    api_key: ${ANTHROPIC_API_KEY}
    base_url: https://api.anthropic.com
  deepseek:
    type: openai
    api_key: ${DEEPSEEK_API_KEY}
    base_url: https://api.deepseek.com/v1
  ollama:
    type: openai
    base_url: http://localhost:11434/v1
```

The `type` field selects the adapter: `"anthropic"` for native Anthropic API, anything else uses the OpenAI-compatible adapter.

### Agent Config (`~/.gal/agents/<name>.yaml`)

```yaml
name: coder
description: Coding assistant
system_prompt: |
  You are an expert programmer.
models:
  - anthropic/claude-sonnet-4-20250514
  - openai/gpt-4o
default_model: anthropic/claude-sonnet-4-20250514
tools:
  - file_read
  - file_write
  - file_edit
  - file_list
  - grep
  - bash
skills:
  - code_review
mcps:
  team_tools:
    url: https://tools.internal.com/mcp
    headers:
      Authorization: "Bearer ${MCP_TOKEN}"
    timeout: 60
```

Model format: `<provider>/<model_id>` (e.g. `openai/gpt-4o`, `deepseek/deepseek-chat`).

## CLI Commands

```bash
gal-cli chat                    # start chat with default agent
gal-cli chat -a <agent>         # start chat with specific agent
gal-cli agent list              # list all agents
gal-cli agent show <name>       # show agent config
gal-cli init                    # initialize ~/.gal/
```

### In-Chat Commands

```
/agent <name>       switch agent
/agent list         list agents
/model <name>       switch model
/model list         list models
/clear              clear conversation
/help               show help
/quit               exit
```

## Skills

Skills are self-contained capability packs that extend an agent with domain-specific knowledge and scripts.

```
skills/code_review/
├── SKILL.md           # injected into system prompt
└── scripts/
    └── lint.sh        # auto-registered as tool "skill_code_review_lint"
```

Skills are resolved from `~/.gal/skills/` (global) then `./skills/` (project-local). Small skills (SKILL.md < 1KB) are fully injected into the system prompt; larger skills are loaded on demand via the `load_skills` tool.

Scripts in `scripts/` are auto-discovered and exposed to the LLM as callable tools. The LLM can invoke them like built-in tools — input via stdin/args, output via stdout. Scripts are automatically made executable on load.

> **Note:** Tool names are derived by stripping the extension, so `lint.sh` and `lint.py` would collide.

## MCP (Model Context Protocol)

gal-cli supports HTTP-based MCP servers for connecting to remote tool services. Configure MCP servers directly in the agent config:

```yaml
mcps:
  remote_db:
    url: https://db-tools.internal.com/mcp
    headers:
      Authorization: "Bearer ${DB_TOKEN}"
    timeout: 30    # seconds, default 30
```

MCP tools are auto-discovered and registered as `mcp_<server>_<tool>` (e.g. `mcp_remote_db_query`). The LLM can call them like any other tool.

> **Note:** Only HTTP-based MCP is supported. For local tools, use skills instead — they're simpler and more capable (SKILL.md prompt injection).

## Agentic Loop

When the LLM decides to call a tool (built-in, skill script, or MCP), gal-cli executes it and feeds the result back automatically. This loop continues until the LLM produces a final text response.

> **Note:** There is currently no iteration limit on the agentic loop. The full conversation history is sent on every round, so context window usage grows with each iteration.

## Built-in Tools

| Tool | Description |
|------|-------------|
| `file_read` | Read file content |
| `file_write` | Write/create files |
| `file_edit` | Replace lines by range (more efficient than file_write for partial edits) |
| `file_list` | List directory tree with configurable depth |
| `grep` | Search text pattern in files recursively |
| `bash` | Execute shell commands |

## License

MIT
