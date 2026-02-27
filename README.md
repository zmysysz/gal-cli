# GAL-CLI

A lightweight, extensible multi-agent CLI tool for LLM workflows — built for developers, DevOps, and QA professionals who want full control.

## Why GAL-CLI?

- **True multi-agent** — switch agents/models mid-conversation, each with isolated tools and prompts
- **Universal provider support** — OpenAI, Anthropic, DeepSeek, Ollama, ZhipuAI, or any OpenAI-compatible API
- **Extensible by design** — add capabilities via Skills (markdown docs + scripts) or MCP servers, no code changes needed
- **Agentic loop** — automatic tool execution with streaming output, handles complex multi-step tasks
- **Interactive input** — progressive user input collection for passwords, confirmations, and safety checks
- **Shell integration** — built-in shell mode with alias support, tab completion, and LLM context awareness
- **Session management** — persistent conversations with auto-save, resume anytime with full context
- **Smart context handling** — automatic LLM-based compression when hitting token limits
- **Developer-friendly** — pure CLI/TUI, no web UI overhead, works over SSH, integrates with your workflow

## Features

- **Multi-agent** — define multiple agents with different system prompts, tools, and models; switch on the fly
- **Multi-provider** — OpenAI, Anthropic, DeepSeek, Ollama, ZhipuAI (any OpenAI-compatible API)
- **Tool calling** — built-in tools (`file_read`, `file_write`, `file_edit`, `file_list`, `grep`, `bash`, `interactive`) with agentic loop
- **Interactive input** — LLM can collect user information progressively (passwords, choices, etc.) without multiple back-and-forth messages
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
context_limit: 60000  # token threshold for auto context compression (default 60000)

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
  zhipu:
    type: openai
    api_key: ${ZHIPU_API_KEY}
    base_url: https://open.bigmodel.cn/api/paas/v4
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

### Interactive Mode

```bash
gal-cli chat                    # start chat with default agent (new session)
gal-cli chat -a <agent>         # start chat with specific agent
gal-cli chat --session <id>     # resume or create session with given ID
```

### Non-Interactive Mode

Use `--message` (or `-m`) to run in non-interactive mode: send one message and exit.

```bash
# Basic usage
gal-cli chat -m "your message"

# Read from file
gal-cli chat -m @prompt.txt

# Read from stdin (pipe-friendly)
echo "hello" | gal-cli chat -m -
cat code.go | gal-cli chat -m -

# With agent/model/session
gal-cli chat -a coder -m "write a function"
gal-cli chat --model openai/gpt-4o -m "analyze"
gal-cli chat --session task1 -m "continue the task"

# Output: stdout = LLM response, stderr = tool calls
gal-cli chat -m "summarize" < input.txt > output.txt
```

### Management Commands

```bash
gal-cli agent list              # list all agents
gal-cli agent show <name>       # show agent config
gal-cli session list            # list all saved sessions
gal-cli session show <id>       # show session metadata
gal-cli session rm <id>         # delete a session
gal-cli tool list               # list all available tools
gal-cli init                    # initialize ~/.gal/
```

### In-Chat Commands (Interactive Mode)

```
/agent <name>       switch agent
/agent list         list agents
/model <name>       switch model
/model list         list models
/skill              list loaded skills
/mcp                list MCP servers
/shell              enter shell mode
/shell --context    enter shell mode with LLM context
/chat               return to chat mode (from shell)
/clear              clear conversation
/help               show help
/quit               exit
```

## Shell Mode

Shell mode provides a lightweight terminal interface within the chat session:

```bash
# Enter shell mode
/shell

# Enter shell mode with LLM context (command outputs visible to LLM)
/shell --context
```

**Features:**
- Tab completion for commands and file paths
- Bash alias support (`ll`, `la`, etc. from `~/.bashrc`)
- Full path commands work (`/bin/ls`, `/usr/bin/python`)
- Built-in commands work everywhere (`/model zhipu/glm-4-plus` works in both chat and shell mode)
- Directory navigation with `cd`
- All bash features (pipes, redirects, variables, etc.)
- Command history with ↑/↓ arrows

**Context Mode:**
When using `/shell --context`, command outputs are added to the conversation history, allowing the LLM to see and respond to command results. Useful for debugging, analysis, or iterative tasks.

**Command Priority:**
1. Built-in commands (`/model`, `/agent`, `/help`, etc.) are always recognized first
2. In shell mode: other `/` prefixed inputs are treated as shell commands (e.g., `/bin/ls`)
3. In chat mode: unknown `/` commands show an error message

Return to chat mode with `/chat`.

## Interactive Input

The `interactive` tool allows LLM to collect user information progressively without multiple back-and-forth messages. This provides a better user experience for tasks requiring multiple inputs (passwords, choices, configuration values, etc.).

### How It Works

1. **LLM decides it needs user input** and calls the `interactive` tool with all questions at once
2. **Progressive prompts** — user is asked questions one by one
3. **Local collection** — no LLM calls during input collection
4. **All results returned** — LLM receives all answers as JSON and continues the task

### Example Usage

When you ask "Generate an SSH key pair", the LLM will:

```
⚡ interactive
📝 Select key type
Options:
  1) rsa
  2) ed25519
  3) ecdsa
Enter number or text:
> 2
  → ed25519

📝 Key size (e.g., 4096 for RSA)
> 256
  → 256

📝 Comment/email for the key
> user@example.com
  → user@example.com

🔒 Passphrase (leave empty for none) (input hidden)
> 
  → (empty)

📝 Interactive input 4/4 (Ctrl+C to cancel)
```

### Features

- **Progressive UX** — one question at a time, not overwhelming
- **Two input types** — `blank` (free text) and `select` (choose from options)
- **Sensitive fields** — passwords show as `********` in echo
- **Cancellable** — press Ctrl+C to cancel input collection
- **Status indicator** — shows progress (e.g., "2/4") and cancel hint
- **Safety confirmations** — LLM should use this tool to confirm dangerous operations (write/delete/system modifications) with yes/no/trust options

### Use Cases

1. **Information Collection** — passwords, API keys, configuration values
2. **Command Prerequisites** — sudo password, SSH passphrase before executing commands
3. **Safety Confirmations** — confirm before:
   - Write operations (file_write, file_edit)
   - Dangerous operations (rm, dd, system modifications)
   - Privacy-related actions (reading sensitive files, network requests)
   - System changes (installing software, modifying configs)

### Confirmation Pattern

For risky operations, LLM should ask for confirmation:

```json
{
  "fields": [{
    "name": "confirm",
    "type": "interactive_input",
    "interactive_type": "select",
    "interactive_hint": "About to delete 50 files. Proceed?",
    "options": ["yes", "no", "trust"]
  }]
}
```

- **yes** — proceed with this operation
- **no** — cancel the operation
- **trust** — proceed and skip similar confirmations in this conversation

### Tool Definition

The `interactive` tool is built-in and available to all agents. LLM calls it with a `fields` array:

```json
{
  "fields": [
    {
      "name": "key_type",
      "type": "interactive_input",
      "interactive_type": "select",
      "interactive_hint": "Select key type",
      "options": ["rsa", "ed25519", "ecdsa"]
    },
    {
      "name": "passphrase",
      "type": "interactive_input",
      "interactive_type": "blank",
      "interactive_hint": "Enter passphrase (optional)",
      "sensitive": true
    }
  ]
}
```

See `INTERACTIVE_INPUT.md` for detailed documentation.

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

> **Note:** There is currently no iteration limit on the agentic loop. When the conversation context grows beyond the configured `context_limit` (default 60K tokens), old messages are automatically compressed via an LLM summarization call.

## Built-in Tools

| Tool | Description |
|------|-------------|
| `file_read` | Read file content |
| `file_write` | Write/create files |
| `file_edit` | Replace lines by range (more efficient than file_write for partial edits) |
| `file_list` | List directory tree with configurable depth |
| `grep` | Search text pattern in files recursively |
| `bash` | Execute shell commands |
| `interactive` | Collect user input progressively (passwords, choices, etc.) |

## License

MIT
