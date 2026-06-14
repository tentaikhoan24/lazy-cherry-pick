// M16b — AI provider presets for headless conflict resolution.
//
// The Rust `run_ai_resolve` command is a generic engine: it runs `command` with
// a rendered `args` list, optionally feeds the prompt via STDIN, and reports
// success/cost. Everything provider-specific lives here as DATA — a preset just
// pre-fills the editable Settings fields. Adding a new CLI = adding one entry,
// no Rust/engine change. "custom" lets the user plug in anything.
//
// Placeholders in `args`:
//   {model}   → replaced by the model value; if model is empty the token (and a
//               preceding "--model"/"-m" flag) is dropped.
//   {prompt}  → replaced by the prompt as a SINGLE arg; only used when
//               promptVia === "arg" (otherwise the prompt goes via STDIN).

export type PromptVia = "stdin" | "arg";
export type OutputFormat = "claude-json" | "none";

export interface AiModelOption {
  value: string;
  label: string;
}

export interface AiProviderPreset {
  id: string;
  label: string;
  /** Default executable name (bare → resolved via Detect) or path. */
  command: string;
  /** Args template (see placeholder docs above). */
  args: string;
  promptVia: PromptVia;
  outputFormat: OutputFormat;
  /** Model choices for the dropdown; empty → free-text model input. */
  models: AiModelOption[];
  /** Short help line shown in Settings. */
  note: string;
}

export const AI_PROVIDERS: AiProviderPreset[] = [
  {
    id: "claude",
    label: "Claude Code",
    command: "claude",
    args:
      '-p --output-format json --allowedTools "Read,Edit,Write,Glob,Grep" --disallowedTools "Bash" --permission-mode acceptEdits --model {model}',
    promptVia: "stdin",
    outputFormat: "claude-json",
    models: [
      { value: "", label: "Default (Claude Code's configured model)" },
      { value: "opus", label: "Opus" },
      { value: "sonnet", label: "Sonnet" },
      { value: "haiku", label: "Haiku" },
    ],
    note: "Uses your existing Claude Code login (no API key). Sandboxed — no shell/git access. Verified.",
  },
  {
    id: "gemini",
    label: "Gemini CLI",
    command: "gemini",
    args: "--yolo --model {model}",
    promptVia: "stdin",
    outputFormat: "none",
    models: [
      { value: "", label: "Default" },
      { value: "gemini-2.5-pro", label: "Gemini 2.5 Pro" },
      { value: "gemini-2.5-flash", label: "Gemini 2.5 Flash" },
    ],
    note: "Google Gemini CLI (`--yolo` auto-approves edits). Requires `gemini` logged in. Tune flags while testing.",
  },
  {
    id: "codex",
    label: "OpenAI Codex CLI",
    command: "codex",
    args: "exec --full-auto --skip-git-repo-check {prompt}",
    promptVia: "arg",
    outputFormat: "none",
    models: [{ value: "", label: "Default" }],
    note: "OpenAI Codex CLI — `codex exec` runs headless. Prompt passed as an argument. Tune flags while testing.",
  },
  {
    id: "aider",
    label: "Aider",
    command: "aider",
    args: "--yes-always --no-auto-commits --no-gitignore --message {prompt}",
    promptVia: "arg",
    outputFormat: "none",
    models: [{ value: "", label: "Default" }],
    note: "Aider — `--no-auto-commits` keeps it from committing; we stage after review. Tune flags while testing.",
  },
  {
    id: "custom",
    label: "Custom…",
    command: "",
    args: "",
    promptVia: "stdin",
    outputFormat: "none",
    models: [],
    note: "Any headless CLI agent that edits files in place. Use {model} / {prompt} placeholders as needed.",
  },
];

export function findProvider(id: string): AiProviderPreset {
  return AI_PROVIDERS.find((p) => p.id === id) ?? AI_PROVIDERS[AI_PROVIDERS.length - 1];
}

const MODEL_FLAGS = new Set(["--model", "-m", "--model=", "-m="]);

/** Split an args template into tokens, respecting double-quoted segments. */
function tokenize(template: string): string[] {
  const out: string[] = [];
  const re = /"([^"]*)"|(\S+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(template)) !== null) {
    out.push(m[1] !== undefined ? m[1] : m[2]);
  }
  return out;
}

/**
 * Render an args template into the final argument vector passed to Rust.
 * - `{prompt}` becomes a single arg only when `promptVia === "arg"`.
 * - `{model}` is substituted; when `model` is empty, the token is dropped, and
 *   a directly-preceding model flag ("--model"/"-m") is popped too.
 */
export function renderAiArgs(
  template: string,
  model: string,
  prompt: string,
  promptVia: PromptVia,
): string[] {
  const tokens = tokenize(template);
  const out: string[] = [];
  for (const tok of tokens) {
    if (tok === "{prompt}") {
      if (promptVia === "arg") out.push(prompt);
      continue;
    }
    if (tok.includes("{model}")) {
      if (model) {
        out.push(tok.replaceAll("{model}", model));
      } else {
        // Drop the empty model token, and a preceding bare model flag.
        const last = out[out.length - 1];
        if (last !== undefined && MODEL_FLAGS.has(last)) out.pop();
      }
      continue;
    }
    out.push(tok);
  }
  return out;
}
