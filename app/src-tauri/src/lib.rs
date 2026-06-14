mod forge;

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Mutex;
use tauri::Emitter;
use tauri::Manager;
use tauri_plugin_shell::process::CommandEvent;
use tauri_plugin_shell::ShellExt;

static CALL_ID: AtomicU64 = AtomicU64::new(1);

// Tracks all currently-running sidecar children by call ID.
// Each concurrent call gets its own slot so kills are scoped correctly.
struct ActiveSidecar(Mutex<HashMap<u64, tauri_plugin_shell::process::CommandChild>>);

// ── sidecar_call ─────────────────────────────────────────────────────────────

#[tauri::command]
async fn sidecar_call(
    app: tauri::AppHandle,
    method: String,
    params: Option<Value>,
) -> Result<Value, String> {
    let sidecar = app
        .shell()
        .sidecar("sidecar")
        .map_err(|e| format!("sidecar lookup failed: {e}"))?;

    let (mut rx, mut child) = sidecar
        .spawn()
        .map_err(|e| format!("sidecar spawn failed: {e}"))?;

    let request = json!({
        "jsonrpc": "2.0",
        "id": 1,
        "method": method,
        "params": params,
    });
    child
        .write(format!("{}\n", request).as_bytes())
        .map_err(|e| format!("sidecar stdin write failed: {e}"))?;

    // Give this call a unique ID and store child so sidecar_cancel can kill it.
    let call_id = CALL_ID.fetch_add(1, Ordering::Relaxed);
    {
        let state = app.state::<ActiveSidecar>();
        state.0.lock().unwrap().insert(call_id, child);
    }

    let mut result: Option<Result<Value, String>> = None;

    while let Some(event) = rx.recv().await {
        match event {
            CommandEvent::Stdout(bytes) => {
                let text = String::from_utf8_lossy(&bytes);
                let parsed: Value = match serde_json::from_str(text.trim()) {
                    Ok(v) => v,
                    Err(e) => {
                        result = Some(Err(format!(
                            "failed to parse sidecar response `{text}`: {e}"
                        )));
                        break;
                    }
                };

                // Progress notification: has "progress" field, no "result"/"error".
                if let Some(progress) = parsed.get("progress") {
                    let _ = app.emit("cp-progress", progress.clone());
                    continue;
                }

                // Final result — remove only this call's child and kill it.
                if let Some(c) = app.state::<ActiveSidecar>().0.lock().unwrap().remove(&call_id) {
                    let _ = c.kill();
                }
                result = Some(Ok(parsed));
                break;
            }
            CommandEvent::Stderr(bytes) => {
                let line = String::from_utf8_lossy(&bytes);
                let line = line.trim();
                let ts = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap_or_default()
                    .as_secs();
                if let Some(rest) = line.strip_prefix("[GIT_CMD] ") {
                    // Format: <ms>|<branch>|git <args>
                    let parts: Vec<&str> = rest.splitn(3, '|').collect();
                    let (ms, branch, cmd) = if parts.len() == 3 {
                        let ms_val: Option<u64> = parts[0].parse().ok();
                        let br = if parts[1].is_empty() { None } else { Some(parts[1]) };
                        (ms_val, br, parts[2].to_string())
                    } else {
                        (None, None, rest.to_string())
                    };
                    let _ = app.emit("git-log", serde_json::json!({
                        "ts": ts, "type": "cmd", "cmd": cmd,
                        "branch": branch, "ms": ms
                    }));
                    if let Ok(path) = git_log_file(&app) {
                        if let Some(parent) = path.parent() {
                            let _ = fs::create_dir_all(parent);
                        }
                        use std::io::Write;
                        if let Ok(mut f) = std::fs::OpenOptions::new()
                            .create(true).append(true).open(&path)
                        {
                            let _ = writeln!(f, "cmd {} {}", ts, rest);
                        }
                    }
                } else if let Some(info) = line.strip_prefix("[GIT_INFO] ") {
                    let _ = app.emit("git-log", serde_json::json!({ "ts": ts, "type": "info", "cmd": info }));
                    if let Ok(path) = git_log_file(&app) {
                        if let Some(parent) = path.parent() {
                            let _ = fs::create_dir_all(parent);
                        }
                        use std::io::Write;
                        if let Ok(mut f) = std::fs::OpenOptions::new()
                            .create(true).append(true).open(&path)
                        {
                            let _ = writeln!(f, "info {} {}", ts, info);
                        }
                    }
                } else {
                    eprintln!("sidecar[stderr]: {}", line);
                }
            }
            CommandEvent::Terminated(payload) => {
                app.state::<ActiveSidecar>().0.lock().unwrap().remove(&call_id);
                if result.is_none() {
                    result = Some(Err(format!(
                        "sidecar terminated before response (code={:?})",
                        payload.code
                    )));
                }
                break;
            }
            CommandEvent::Error(err) => {
                app.state::<ActiveSidecar>().0.lock().unwrap().remove(&call_id);
                result = Some(Err(format!("sidecar error: {err}")));
                break;
            }
            _ => {}
        }
    }

    result.unwrap_or(Err("sidecar closed without sending a response".to_string()))
}

// ── sidecar_cancel ───────────────────────────────────────────────────────────

#[tauri::command]
async fn sidecar_cancel(app: tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<ActiveSidecar>();
    let children: Vec<_> = state.0.lock().unwrap().drain().collect();
    for (_, child) in children {
        let _ = child.kill();
    }
    Ok(())
}

// ── git command log ───────────────────────────────────────────────────────────

fn git_log_file(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let mut p = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("app_data_dir failed: {e}"))?;
    p.push("git.log");
    Ok(p)
}

#[tauri::command]
fn git_log_read(app: tauri::AppHandle) -> Result<Vec<serde_json::Value>, String> {
    let path = git_log_file(&app)?;
    if !path.exists() {
        return Ok(vec![]);
    }
    let data = fs::read_to_string(&path).map_err(|e| format!("read git log failed: {e}"))?;
    let entries: Vec<serde_json::Value> = data
        .lines()
        .rev()
        .take(1000)
        .filter_map(|line| {
            // Format: "<type> <ts> <rest>"
            // For cmd: rest = "<ms>|<branch>|git <args>"
            // For info: rest = "<label>"
            // For ai:   rest = "<json object, without ts/type>"
            let mut parts = line.splitn(3, ' ');
            let kind = parts.next()?;
            let ts: u64 = parts.next()?.parse().ok()?;
            let rest = parts.next().unwrap_or("");
            if kind == "cmd" {
                let sub: Vec<&str> = rest.splitn(3, '|').collect();
                let (ms, branch, cmd) = if sub.len() == 3 {
                    let ms_val: Option<u64> = sub[0].parse().ok();
                    let br = if sub[1].is_empty() { None } else { Some(sub[1]) };
                    (ms_val, br, sub[2].to_string())
                } else {
                    (None, None, rest.to_string())
                };
                Some(serde_json::json!({ "ts": ts, "type": "cmd", "cmd": cmd, "branch": branch, "ms": ms }))
            } else if kind == "ai" {
                let mut obj: serde_json::Map<String, Value> = serde_json::from_str(rest).ok()?;
                obj.insert("ts".to_string(), serde_json::json!(ts));
                obj.insert("type".to_string(), serde_json::json!("ai"));
                Some(Value::Object(obj))
            } else {
                Some(serde_json::json!({ "ts": ts, "type": kind, "cmd": rest }))
            }
        })
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
        .collect();
    Ok(entries)
}

#[tauri::command]
fn git_log_clear(app: tauri::AppHandle) -> Result<(), String> {
    let path = git_log_file(&app)?;
    if path.exists() {
        fs::write(&path, "").map_err(|e| format!("clear git log failed: {e}"))?;
    }
    Ok(())
}

fn unix_ts() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

fn append_log_line(app: &tauri::AppHandle, line: &str) {
    if let Ok(path) = git_log_file(app) {
        if let Some(parent) = path.parent() {
            let _ = fs::create_dir_all(parent);
        }
        use std::io::Write;
        if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(&path) {
            let _ = writeln!(f, "{line}");
        }
    }
}

/// Short display name for an AI CLI executable, e.g. "C:\...\claude.cmd" -> "claude".
fn ai_command_name(command: &str) -> String {
    std::path::Path::new(command)
        .file_stem()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_else(|| command.to_string())
}

/// Replaces the raw prompt text inside `args` (arg-mode prompt delivery) with a
/// `<prompt: N chars>` placeholder so the log doesn't balloon with file contents.
fn sanitize_ai_args(args: &[String], prompt: &str, prompt_via: &str) -> Vec<String> {
    if prompt_via == "arg" && !prompt.is_empty() {
        args.iter()
            .map(|a| {
                if a == prompt {
                    format!("<prompt: {} chars>", prompt.chars().count())
                } else {
                    a.clone()
                }
            })
            .collect()
    } else {
        args.to_vec()
    }
}

/// Records one AI-CLI invocation in the same `git.log` file / `git-log` event
/// stream as git commands, so `GitConsole.svelte` shows both side by side.
fn log_ai_result(
    app: &tauri::AppHandle,
    command: &str,
    args: &[String],
    prompt_via: &str,
    prompt_chars: usize,
    success: bool,
    cost_usd: f64,
    duration_ms: u64,
    error: &str,
) {
    let ts = unix_ts();
    let error_trunc: String = error.chars().take(300).collect();
    let mut payload = serde_json::json!({
        "command": command,
        "args": args,
        "promptVia": prompt_via,
        "promptChars": prompt_chars,
        "success": success,
        "costUsd": cost_usd,
        "durationMs": duration_ms,
        "error": error_trunc,
    });
    append_log_line(app, &format!("ai {ts} {payload}"));
    if let Value::Object(ref mut m) = payload {
        m.insert("ts".to_string(), serde_json::json!(ts));
        m.insert("type".to_string(), serde_json::json!("ai"));
    }
    let _ = app.emit("git-log", payload);
}

// ── app settings ─────────────────────────────────────────────────────────────

fn default_max_commits() -> u32 { 100 }
fn default_apply_mode() -> String { "apply".to_string() }
fn default_true() -> bool { true }

#[derive(Debug, Clone, Serialize, Deserialize)]
struct AppSettings {
    #[serde(rename = "maxCommits", default = "default_max_commits")]
    max_commits: u32,
    #[serde(rename = "defaultApplyMode", default = "default_apply_mode")]
    default_apply_mode: String,
    #[serde(rename = "showEolMarkers", default)]
    show_eol_markers: bool,
    #[serde(rename = "autoFetchOnOpen", default)]
    auto_fetch_on_open: bool,
    #[serde(default = "default_theme")]
    theme: String,
    #[serde(rename = "externalDiffEnabled", default)]
    external_diff_enabled: bool,
    #[serde(rename = "externalDiffPath", default)]
    external_diff_path: String,
    #[serde(rename = "externalDiffArgs", default)]
    external_diff_args: String,
    #[serde(rename = "externalMergeEnabled", default)]
    external_merge_enabled: bool,
    #[serde(rename = "externalMergePath", default)]
    external_merge_path: String,
    #[serde(rename = "externalMergeArgs", default)]
    external_merge_args: String,
    #[serde(rename = "checkForUpdatesOnStartup", default = "default_true")]
    check_for_updates_on_startup: bool,
    /// M11a — auto-stash uncommitted changes before a cherry-pick batch and
    /// pop them once the whole flow finishes. Off by default for safety.
    #[serde(rename = "autoStash", default)]
    auto_stash: bool,
    /// M16/M16b — AI conflict resolution via a headless AI CLI agent
    /// (Claude Code / Gemini / Codex / Aider / custom). The engine is generic;
    /// `ai_provider` only remembers which preset the UI last applied.
    #[serde(rename = "aiEnabled", default)]
    ai_enabled: bool,
    #[serde(rename = "aiProvider", default = "default_ai_provider")]
    ai_provider: String,
    #[serde(rename = "aiCommand", default)]
    ai_command: String,
    #[serde(rename = "aiArgs", default = "default_ai_args")]
    ai_args: String,
    /// "" = the tool's own default model; else a provider-specific alias/id.
    #[serde(rename = "aiModel", default)]
    ai_model: String,
    /// "stdin" (prompt fed via STDIN) | "arg" (prompt already embedded in args).
    #[serde(rename = "aiPromptVia", default = "default_prompt_via")]
    ai_prompt_via: String,
    /// "claude-json" (parse Claude's JSON envelope for success/cost) | "none"
    /// (use the process exit code only).
    #[serde(rename = "aiOutputFormat", default = "default_output_format")]
    ai_output_format: String,
    #[serde(rename = "aiTimeoutSecs", default = "default_ai_timeout")]
    ai_timeout_secs: u32,
    /// M14 — per-repo forge connections, keyed by repo path.
    /// Token itself is NOT here — it lives in the OS keychain. Only metadata
    /// (kind + baseURL + username + host) so we can locate the token.
    #[serde(rename = "forgeConnections", default)]
    forge_connections: HashMap<String, ForgeConnection>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
struct ForgeConnection {
    /// "github" | "gitlab"
    kind: String,
    /// API base URL — e.g. "https://api.github.com" or "https://gitlab.example.com"
    #[serde(rename = "baseURL")]
    base_url: String,
    /// Used to derive the keychain key together with kind + username.
    host: String,
    /// Display name + keychain key component.
    username: String,
}

fn default_theme() -> String { "dark".to_string() }
fn default_ai_provider() -> String { "claude".to_string() }
fn default_prompt_via() -> String { "stdin".to_string() }
fn default_output_format() -> String { "claude-json".to_string() }
fn default_ai_timeout() -> u32 { 120 }
fn default_ai_args() -> String {
    "-p --output-format json --allowedTools \"Read,Edit,Write,Glob,Grep\" --disallowedTools \"Bash\" --permission-mode acceptEdits --model {model}".to_string()
}

impl Default for AppSettings {
    fn default() -> Self {
        Self {
            max_commits: default_max_commits(),
            default_apply_mode: default_apply_mode(),
            show_eol_markers: false,
            auto_fetch_on_open: false,
            theme: default_theme(),
            external_diff_enabled: false,
            external_diff_path: String::new(),
            external_diff_args: String::new(),
            external_merge_enabled: false,
            external_merge_path: String::new(),
            external_merge_args: String::new(),
            check_for_updates_on_startup: true,
            auto_stash: false,
            ai_enabled: false,
            ai_provider: default_ai_provider(),
            ai_command: String::new(),
            ai_args: default_ai_args(),
            ai_model: String::new(),
            ai_prompt_via: default_prompt_via(),
            ai_output_format: default_output_format(),
            ai_timeout_secs: default_ai_timeout(),
            forge_connections: HashMap::new(),
        }
    }
}

fn settings_file(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let mut p = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("app_data_dir failed: {e}"))?;
    p.push("settings.json");
    Ok(p)
}

#[tauri::command]
fn settings_load(app: tauri::AppHandle) -> Result<AppSettings, String> {
    let path = settings_file(&app)?;
    if !path.exists() {
        return Ok(AppSettings::default());
    }
    let data = fs::read_to_string(&path).map_err(|e| format!("read settings failed: {e}"))?;
    serde_json::from_str(&data).map_err(|_| {
        // Corrupted file — return defaults rather than hard-erroring.
        "".to_string()
    }).or_else(|_| Ok(AppSettings::default()))
}

#[tauri::command]
fn settings_save(app: tauri::AppHandle, settings: AppSettings) -> Result<(), String> {
    let path = settings_file(&app)?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| format!("create dir failed: {e}"))?;
    }
    let data =
        serde_json::to_string_pretty(&settings).map_err(|e| format!("serialize failed: {e}"))?;
    fs::write(&path, data).map_err(|e| format!("write settings failed: {e}"))?;
    Ok(())
}

// ── external tool launch ──────────────────────────────────────────────────────

#[tauri::command]
async fn launch_detached(program: String, args: Vec<String>) -> Result<(), String> {
    tokio::process::Command::new(&program)
        .args(&args)
        .spawn()
        .map_err(|e| format!("launch failed: {e}"))?;
    Ok(())
}

#[tauri::command]
async fn launch_and_wait(program: String, args: Vec<String>) -> Result<(), String> {
    tokio::process::Command::new(&program)
        .args(&args)
        .spawn()
        .map_err(|e| format!("launch failed: {e}"))?
        .wait()
        .await
        .map_err(|e| format!("wait failed: {e}"))?;
    Ok(())
}

#[tauri::command]
fn detect_external_tools() -> Vec<serde_json::Value> {
    let candidates: &[(&str, &str)] = &[
        ("TortoiseGit", r"C:\Program Files\TortoiseGit\bin\TortoiseGitProc.exe"),
        ("Beyond Compare 3", r"C:\Program Files (x86)\Beyond Compare 3\BCompare.exe"),
        ("Beyond Compare 4", r"C:\Program Files\Beyond Compare 4\BCompare.exe"),
        ("WinMerge", r"C:\Program Files\WinMerge\WinMergeU.exe"),
    ];
    // VSCode path varies per user; expand %LOCALAPPDATA% manually
    let localappdata = std::env::var("LOCALAPPDATA").unwrap_or_default();
    let vscode_path = format!(r"{localappdata}\Programs\Microsoft VS Code\Code.exe");

    let mut found: Vec<serde_json::Value> = candidates
        .iter()
        .filter(|(_, path)| std::path::Path::new(path).exists())
        .map(|(name, path)| serde_json::json!({ "name": name, "path": path }))
        .collect();

    if std::path::Path::new(&vscode_path).exists() {
        found.push(serde_json::json!({ "name": "VSCode", "path": vscode_path }));
    }
    found
}

// ── M16/M16b: AI conflict resolution via a headless AI CLI agent ─────────────

#[derive(Debug, Serialize)]
struct DetectedAi {
    found: bool,
    path: String,
    version: String,
}

/// Locate an AI CLI executable. `command` may be a bare name ("claude",
/// "gemini", "codex", "aider") or an explicit path. For a bare name we check the
/// npm-global `%APPDATA%\npm\<name>.cmd` shim first, then PATH (`where`). Returns
/// the resolved path + its `--version` output.
#[tauri::command]
fn detect_ai_tool(command: String) -> DetectedAi {
    let command = command.trim().to_string();
    let mut candidates: Vec<String> = Vec::new();

    let looks_like_path = command.contains('\\') || command.contains('/');
    if looks_like_path {
        candidates.push(command.clone());
    } else {
        let name = if command.is_empty() { "claude".to_string() } else { command.clone() };
        if let Ok(appdata) = std::env::var("APPDATA") {
            candidates.push(format!(r"{appdata}\npm\{name}.cmd"));
        }
        if let Ok(out) = std::process::Command::new("where").arg(&name).output() {
            if out.status.success() {
                for line in String::from_utf8_lossy(&out.stdout).lines() {
                    let p = line.trim();
                    if !p.is_empty()
                        && (p.ends_with(".cmd") || p.ends_with(".exe") || p.ends_with(".bat"))
                    {
                        candidates.push(p.to_string());
                    }
                }
            }
        }
    }

    for path in candidates {
        if !std::path::Path::new(&path).exists() {
            continue;
        }
        let lower = path.to_lowercase();
        let version = if lower.ends_with(".cmd") || lower.ends_with(".bat") {
            std::process::Command::new("cmd").args(["/C", &path, "--version"]).output()
        } else {
            std::process::Command::new(&path).arg("--version").output()
        }
        .ok()
        .filter(|o| o.status.success())
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default();
        return DetectedAi { found: true, path, version };
    }
    DetectedAi { found: false, path: String::new(), version: String::new() }
}

#[derive(Debug, Serialize)]
struct AiResult {
    success: bool,
    #[serde(rename = "isError")]
    is_error: bool,
    #[serde(rename = "resultText")]
    result_text: String,
    error: String,
    #[serde(rename = "costUsd")]
    cost_usd: f64,
    #[serde(rename = "durationMs")]
    duration_ms: u64,
}

/// Run a headless AI CLI agent to resolve conflicts in-place. The agent is
/// expected to edit the conflict files on disk; the caller reads them back
/// afterwards — we never parse merged content out of stdout. `args` is fully
/// rendered by the caller. When `prompt_via == "stdin"` the prompt is fed via
/// STDIN (avoids cmd.exe quoting); otherwise the caller already embedded it in
/// `args`. `output_format == "claude-json"` parses Claude Code's JSON envelope
/// for success/cost; any other value falls back to the process exit code.
///
/// A `.cmd`/`.bat` command is run through `cmd /C` (npm shims); anything else is
/// spawned directly (cleaner argv quoting — no cmd.exe layer).
///
/// Every invocation is recorded into `git.log` / the `git-log` event stream
/// (same as `[GIT_CMD]`/`[GIT_INFO]`) via `log_ai_result`, regardless of outcome.
#[tauri::command]
async fn run_ai_resolve(
    app: tauri::AppHandle,
    repo_path: String,
    command: String,
    args: Vec<String>,
    prompt: String,
    prompt_via: String,
    output_format: String,
    timeout_secs: u64,
) -> Result<AiResult, String> {
    let log_command = ai_command_name(&command);
    let log_args = sanitize_ai_args(&args, &prompt, &prompt_via);
    let log_prompt_chars = prompt.chars().count();
    let wall_start = std::time::Instant::now();

    let outcome = run_ai_resolve_inner(
        repo_path,
        command,
        args,
        prompt,
        prompt_via.clone(),
        output_format,
        timeout_secs,
    )
    .await;

    let (success, cost_usd, duration_ms, error) = match &outcome {
        Ok(r) => (r.success, r.cost_usd, r.duration_ms, r.error.clone()),
        Err(e) => (false, 0.0, wall_start.elapsed().as_millis() as u64, e.clone()),
    };
    log_ai_result(
        &app,
        &log_command,
        &log_args,
        &prompt_via,
        log_prompt_chars,
        success,
        cost_usd,
        duration_ms,
        &error,
    );

    outcome
}

async fn run_ai_resolve_inner(
    repo_path: String,
    command: String,
    args: Vec<String>,
    prompt: String,
    prompt_via: String,
    output_format: String,
    timeout_secs: u64,
) -> Result<AiResult, String> {
    use tokio::io::AsyncWriteExt;

    if command.trim().is_empty() {
        return Err("no AI command configured (set the executable path in Settings)".to_string());
    }

    let lower = command.to_lowercase();
    let use_cmd = lower.ends_with(".cmd") || lower.ends_with(".bat");

    let mut cmd = if use_cmd {
        let mut c = tokio::process::Command::new("cmd");
        c.arg("/C").arg(&command).args(&args);
        c
    } else {
        let mut c = tokio::process::Command::new(&command);
        c.args(&args);
        c
    };

    let mut child = cmd
        .current_dir(&repo_path)
        .stdin(std::process::Stdio::piped())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .map_err(|e| format!("failed to launch AI tool '{command}': {e}"))?;

    // Feed the prompt via stdin when requested, then close the pipe (EOF) so the
    // tool starts. When the prompt is in argv, still close stdin so the tool
    // doesn't block waiting for input it will never get.
    if let Some(mut stdin) = child.stdin.take() {
        if prompt_via == "stdin" {
            stdin
                .write_all(prompt.as_bytes())
                .await
                .map_err(|e| format!("failed to write prompt to stdin: {e}"))?;
        }
        // dropping `stdin` here closes the pipe
    }

    let start = std::time::Instant::now();
    let output = match tokio::time::timeout(
        std::time::Duration::from_secs(timeout_secs),
        child.wait_with_output(),
    )
    .await
    {
        Ok(Ok(o)) => o,
        Ok(Err(e)) => return Err(format!("AI tool execution failed: {e}")),
        Err(_) => return Err(format!("AI tool timed out after {timeout_secs}s")),
    };
    let duration_ms = start.elapsed().as_millis() as u64;

    let stdout = String::from_utf8_lossy(&output.stdout).to_string();
    let stderr = String::from_utf8_lossy(&output.stderr).to_string();

    if output_format == "claude-json" {
        if stdout.trim().is_empty() {
            let detail = if stderr.trim().is_empty() {
                "no output (is the AI CLI logged in? run it once to authenticate)".to_string()
            } else {
                stderr.trim().to_string()
            };
            return Err(format!("AI tool produced no result: {detail}"));
        }
        let parsed: Value = serde_json::from_str(stdout.trim()).map_err(|e| {
            let preview: String = stdout.chars().take(500).collect();
            format!("cannot parse AI JSON output: {e}; raw: {preview}")
        })?;
        let is_error = parsed.get("is_error").and_then(|v| v.as_bool()).unwrap_or(false);
        let result_text = parsed.get("result").and_then(|v| v.as_str()).unwrap_or("").to_string();
        let cost_usd = parsed.get("total_cost_usd").and_then(|v| v.as_f64()).unwrap_or(0.0);
        return Ok(AiResult {
            success: !is_error,
            is_error,
            error: if is_error { result_text.clone() } else { String::new() },
            result_text,
            cost_usd,
            duration_ms,
        });
    }

    // Generic mode: success is the process exit code. The merged content is read
    // from disk by the caller; stdout is only kept for diagnostics.
    let ok = output.status.success();
    let result_text: String = stdout.chars().take(2000).collect();
    let err_text = if ok {
        String::new()
    } else {
        let detail = if !stderr.trim().is_empty() { stderr.trim().to_string() } else { result_text.clone() };
        format!("AI tool exited with status {}: {}", output.status, detail)
    };
    Ok(AiResult {
        success: ok,
        is_error: !ok,
        error: err_text,
        result_text,
        cost_usd: 0.0,
        duration_ms,
    })
}

// ── recent repos ─────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
struct RecentRepo {
    path: String,
    #[serde(rename = "lastOpened")]
    last_opened: u64,
}

fn recents_file(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let mut p = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("app_data_dir failed: {e}"))?;
    p.push("recents.json");
    Ok(p)
}

#[tauri::command]
fn recents_load(app: tauri::AppHandle) -> Result<Vec<RecentRepo>, String> {
    let path = recents_file(&app)?;
    if !path.exists() {
        return Ok(vec![]);
    }
    let data = fs::read_to_string(&path).map_err(|e| format!("read recents failed: {e}"))?;
    serde_json::from_str(&data).map_err(|e| format!("parse recents failed: {e}"))
}

#[tauri::command]
fn recents_save(app: tauri::AppHandle, recents: Vec<RecentRepo>) -> Result<(), String> {
    let path = recents_file(&app)?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| format!("create dir failed: {e}"))?;
    }
    let data =
        serde_json::to_string_pretty(&recents).map_err(|e| format!("serialize failed: {e}"))?;
    fs::write(&path, data).map_err(|e| format!("write recents failed: {e}"))?;
    Ok(())
}

// ── M14 forge commands ────────────────────────────────────────────────────────
//
// Tokens flow Frontend → Rust (input) → keychain (storage) → Rust (load on use).
// They MUST NOT be returned to the frontend after save, and MUST NOT be passed
// through to the sidecar — Rust is the only layer that touches plaintext tokens.

fn parse_forge_kind(kind: &str) -> Result<forge::ForgeKind, String> {
    match kind {
        "github" => Ok(forge::ForgeKind::Github),
        "gitlab" => Ok(forge::ForgeKind::Gitlab),
        "bitbucket" => Ok(forge::ForgeKind::Bitbucket),
        other => Err(format!("unsupported forge kind: {other}")),
    }
}

#[tauri::command]
// `username` param is required for Bitbucket (Basic Auth needs username+token);
// ignored by GitHub/GitLab providers.
async fn forge_test_connection(
    kind: String,
    base_url: String,
    token: String,
    username: Option<String>,
) -> Result<forge::ConnectionTestResult, String> {
    let k = parse_forge_kind(&kind)?;
    let provider = forge::provider_for(&k, &base_url, username.as_deref().unwrap_or(""));
    provider.test_connection(&token).await
}

#[tauri::command]
// M14f — test an ALREADY-SAVED connection: loads the token from the keychain
// (the frontend never sees it) and re-runs the provider's auth check. Useful to
// verify a stored token still works after sitting unused for a while.
async fn forge_test_saved_connection(
    app: tauri::AppHandle,
    repo_path: String,
) -> Result<forge::ConnectionTestResult, String> {
    let settings = settings_load(app)?;
    let conn = settings
        .forge_connections
        .get(&repo_path)
        .ok_or_else(|| "no forge connection configured for this repo".to_string())?;
    let kind = parse_forge_kind(&conn.kind)?;
    let token = forge::token_load(&kind, &conn.host, &conn.username).map_err(|e| {
        format!(
            "token not found in keychain for key '{}:{}:{}' ({e}) — please reconnect",
            conn.kind, conn.host, conn.username
        )
    })?;
    let provider = forge::provider_for(&kind, &conn.base_url, &conn.username);
    provider.test_connection(&token).await
}

#[tauri::command]
fn forge_save_connection(
    app: tauri::AppHandle,
    repo_path: String,
    kind: String,
    base_url: String,
    host: String,
    username: String,
    token: String,
) -> Result<(), String> {
    let k = parse_forge_kind(&kind)?;
    // Save token first; if it fails, don't pollute settings.json
    forge::token_save(&k, &host, &username, &token)?;
    // Verify the token is actually retrievable — catches platform-specific
    // keychain bugs where set succeeds but get fails (rare, but seen).
    match forge::token_load(&k, &host, &username) {
        Ok(retrieved) if retrieved == token => {}
        Ok(_) => {
            return Err(format!(
                "keychain saved a different value than what we wrote (key '{}:{}:{}'); refuse to proceed",
                kind, host, username
            ));
        }
        Err(e) => {
            return Err(format!(
                "keychain accepted set but get failed for key '{}:{}:{}' ({e}); aborting save",
                kind, host, username
            ));
        }
    }
    // Then update settings.json with metadata
    let mut settings = settings_load(app.clone())?;
    settings.forge_connections.insert(
        repo_path,
        ForgeConnection { kind, base_url, host, username },
    );
    settings_save(app, settings)
}

#[tauri::command]
fn forge_delete_connection(
    app: tauri::AppHandle,
    repo_path: String,
) -> Result<(), String> {
    let mut settings = settings_load(app.clone())?;
    let Some(conn) = settings.forge_connections.remove(&repo_path) else {
        return Ok(()); // Already absent — idempotent.
    };
    // Only delete the keychain entry if NO other repo still references the
    // same (kind, host, username) triple. Multiple repos can share one token
    // (vd. all your personal GitHub repos under one PAT) — deleting the
    // keychain entry would silently break the others.
    let token_still_referenced = settings.forge_connections.values().any(|other| {
        other.kind == conn.kind && other.host == conn.host && other.username == conn.username
    });
    if !token_still_referenced {
        let k = parse_forge_kind(&conn.kind)?;
        // Idempotent — ignore "not found"
        let _ = forge::token_delete(&k, &conn.host, &conn.username);
    }
    settings_save(app, settings)
}

#[tauri::command]
async fn forge_list_prs(
    app: tauri::AppHandle,
    repo_path: String,
    project_path: String,
    head: String,
) -> Result<Vec<forge::PrSummary>, String> {
    let settings = settings_load(app)?;
    let conn = settings
        .forge_connections
        .get(&repo_path)
        .ok_or_else(|| "no forge connection configured for this repo".to_string())?;
    let kind = parse_forge_kind(&conn.kind)?;
    let token = forge::token_load(&kind, &conn.host, &conn.username)
        .map_err(|e| format!(
            "token not found in keychain for key '{}:{}:{}' ({e}) — please reconnect",
            conn.kind, conn.host, conn.username
        ))?;
    let provider = forge::provider_for(&kind, &conn.base_url, &conn.username);
    provider.list_prs_for_branch(&token, &project_path, &head).await
}

#[tauri::command]
async fn forge_create_pr(
    app: tauri::AppHandle,
    repo_path: String,
    project_path: String,
    base: String,
    head: String,
    title: String,
    body: String,
    draft: bool,
) -> Result<forge::CreatePrResult, String> {
    let settings = settings_load(app)?;
    let conn = settings
        .forge_connections
        .get(&repo_path)
        .ok_or_else(|| "no forge connection configured for this repo".to_string())?;
    let kind = parse_forge_kind(&conn.kind)?;
    let token = forge::token_load(&kind, &conn.host, &conn.username)
        .map_err(|e| format!(
            "token not found in keychain for key '{}:{}:{}' ({e}) — please reconnect",
            conn.kind, conn.host, conn.username
        ))?;
    let provider = forge::provider_for(&kind, &conn.base_url, &conn.username);
    let args = forge::CreatePrArgs {
        project_path,
        base,
        head,
        title,
        body,
        draft,
    };
    provider.create_pr(&token, &args).await
}

// ── entry point ───────────────────────────────────────────────────────────────

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(ActiveSidecar(Mutex::new(HashMap::new())))
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .invoke_handler(tauri::generate_handler![
            sidecar_call,
            sidecar_cancel,
            recents_load,
            recents_save,
            settings_load,
            settings_save,
            git_log_read,
            git_log_clear,
            launch_detached,
            launch_and_wait,
            detect_external_tools,
            detect_ai_tool,
            run_ai_resolve,
            forge_test_connection,
            forge_test_saved_connection,
            forge_save_connection,
            forge_delete_connection,
            forge_create_pr,
            forge_list_prs,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
