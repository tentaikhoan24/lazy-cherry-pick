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
            forge_test_connection,
            forge_save_connection,
            forge_delete_connection,
            forge_create_pr,
            forge_list_prs,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
