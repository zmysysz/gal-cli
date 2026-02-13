# GAL-CLI

A multi-agent CLI tool with tool/skill/MCP management and model switching.

## Features

- **Multi-agent** — define multiple agents with different system prompts, tools, and models; switch on the fly
- **Multi-provider** — OpenAI, Anthropic, DeepSeek, Ollama (any OpenAI-compatible API)
- **Tool calling** — built-in tools (`file_read`, `file_write`, `bash`) with agentic loop
- **Skills** — user-defined capability packs: prompt injection via `SKILL.md` + auto-registered script tools
- **Streaming** — real-time streamed responses from all providers

## Quick Start

```bash
# Build
go build -o gal .

# Initialize config
./gal init

# Set API keys
export OPENAI_API_KEY="sk-..."
# and/or
export ANTHROPIC_API_KEY="sk-ant-..."

# Start chatting
./gal chat
```

## Configuration

`gal init` creates default configs at `~/.gal/`:

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
  - bash
skills:
  - code_review
```

Model format: `<provider>/<model_id>` (e.g. `openai/gpt-4o`, `deepseek/deepseek-chat`).

## CLI Commands

```bash
gal chat                    # start chat with default agent
gal chat -a <agent>         # start chat with specific agent
gal agent list              # list all agents
gal agent show <name>       # show agent config
gal init                    # initialize ~/.gal/
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
    └── lint.sh        # auto-registered as tool "skill:code_review:lint"
```

Skills are resolved from `./skills/` (project-local) then `~/.gal/skills/` (global).

Scripts in `scripts/` are auto-discovered and exposed to the LLM as callable tools. The LLM can invoke them like built-in tools — input via stdin/args, output via stdout.

## Built-in Tools

| Tool | Description |
|------|-------------|
| `file_read` | Read file content |
| `file_write` | Write/create files |
| `bash` | Execute shell commands |

## License

MIT
