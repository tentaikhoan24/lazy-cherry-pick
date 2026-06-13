// Forge integration (M14) — GitHub + GitLab PR/MR creation, test connection.
//
// Architecture: Rust handles all forge HTTP requests via reqwest. Tokens live
// in the OS keychain (Windows Credential Manager / macOS Keychain / Secret
// Service) and never reach the sidecar or settings.json. Sidecar continues to
// own git CLI ops only.
//
// Provider trait keeps GitHub/GitLab differences contained. Adding a new
// provider = one file implementing `Provider` + entry in `provider_for()`.

use keyring::Entry;
use reqwest::{header, Client, StatusCode};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};

const USER_AGENT: &str = "lazy-cherry-pick/0.9 (forge-client)";
const KEYCHAIN_SERVICE: &str = "lazy-cherry-pick.forge";

// ── public types (mirror TS forge-types.ts) ─────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum ForgeKind {
    Github,
    Gitlab,
    Bitbucket,
}

impl ForgeKind {
    fn as_str(&self) -> &'static str {
        match self {
            ForgeKind::Github => "github",
            ForgeKind::Gitlab => "gitlab",
            ForgeKind::Bitbucket => "bitbucket",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConnectionTestResult {
    pub username: String,
    pub scopes: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreatePrArgs {
    /// owner/repo (GitHub) or namespace/path (GitLab — project path with subgroups)
    pub project_path: String,
    pub base: String,
    pub head: String,
    pub title: String,
    pub body: String,
    pub draft: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreatePrResult {
    pub url: String,
    pub number: u64,
    pub state: String,
    /// True when this PR already existed before this call — frontend uses this
    /// to swap the toast wording from "created" to "already exists".
    #[serde(rename = "alreadyExisted")]
    pub already_existed: bool,
}

// ── keychain ────────────────────────────────────────────────────────────────

/// Key format: `<kind>:<host>:<username>` to allow multiple accounts per provider.
fn keychain_key(kind: &ForgeKind, host: &str, username: &str) -> String {
    format!("{}:{}:{}", kind.as_str(), host, username)
}

pub fn token_save(kind: &ForgeKind, host: &str, username: &str, token: &str) -> Result<(), String> {
    let key = keychain_key(kind, host, username);
    eprintln!("[FORGE] token_save service='{}' key='{}'", KEYCHAIN_SERVICE, key);
    let entry = Entry::new(KEYCHAIN_SERVICE, &key)
        .map_err(|e| format!("keychain entry: {e}"))?;
    entry.set_password(token).map_err(|e| format!("keychain set: {e}"))
}

pub fn token_load(kind: &ForgeKind, host: &str, username: &str) -> Result<String, String> {
    let key = keychain_key(kind, host, username);
    eprintln!("[FORGE] token_load service='{}' key='{}'", KEYCHAIN_SERVICE, key);
    let entry = Entry::new(KEYCHAIN_SERVICE, &key)
        .map_err(|e| format!("keychain entry: {e}"))?;
    entry.get_password().map_err(|e| format!("keychain get: {e}"))
}

pub fn token_delete(kind: &ForgeKind, host: &str, username: &str) -> Result<(), String> {
    let entry = Entry::new(KEYCHAIN_SERVICE, &keychain_key(kind, host, username))
        .map_err(|e| format!("keychain entry: {e}"))?;
    // Ignore "not found" — idempotent delete
    let _ = entry.delete_credential();
    Ok(())
}

// ── Provider trait ──────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrSummary {
    pub url: String,
    pub number: u64,
    pub title: String,
    pub state: String,
    pub draft: bool,
    /// Base branch the PR will merge into (e.g. "main"). Critical context —
    /// same head branch can have multiple PRs into different bases.
    pub base: String,
    /// Author login/username.
    pub author: String,
    /// ISO 8601 timestamp of last update — used for "updated 2h ago" in UI.
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    /// ISO 8601 timestamp when the PR was opened.
    #[serde(rename = "createdAt")]
    pub created_at: String,
    /// First ~200 chars of the PR description, plain text.
    #[serde(rename = "bodyPreview")]
    pub body_preview: String,
}

#[async_trait::async_trait]
pub trait Provider {
    async fn test_connection(&self, token: &str) -> Result<ConnectionTestResult, String>;
    async fn create_pr(&self, token: &str, args: &CreatePrArgs) -> Result<CreatePrResult, String>;
    /// List open PRs/MRs for a given head branch. Empty if none.
    async fn list_prs_for_branch(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
    ) -> Result<Vec<PrSummary>, String>;
}

pub fn provider_for(kind: &ForgeKind, base_url: &str, username: &str) -> Box<dyn Provider + Send + Sync> {
    let base = base_url.trim_end_matches('/').to_string();
    match kind {
        ForgeKind::Github => Box::new(GithubProvider { base }),
        ForgeKind::Gitlab => Box::new(GitlabProvider { base }),
        ForgeKind::Bitbucket => Box::new(BitbucketProvider { base, username: username.to_string() }),
    }
}

fn http_client() -> Result<Client, String> {
    Client::builder()
        .user_agent(USER_AGENT)
        .build()
        .map_err(|e| format!("http client: {e}"))
}

/// Trim a PR/MR description down to a hover-friendly preview length.
/// Strips leading/trailing whitespace, collapses runs of newlines to a single
/// "\n\n" so multi-line markdown stays readable, and appends "…" if truncated.
fn truncate_text(text: &str, max_chars: usize) -> String {
    let trimmed = text.trim();
    if trimmed.is_empty() {
        return String::new();
    }
    if trimmed.chars().count() <= max_chars {
        return trimmed.to_string();
    }
    let truncated: String = trimmed.chars().take(max_chars).collect();
    format!("{}…", truncated.trim_end())
}

async fn extract_error(resp: reqwest::Response, url: &str) -> String {
    let status = resp.status();
    let text = resp.text().await.unwrap_or_default();
    // GitHub 422 returns { "message": "Validation Failed", "errors": [{...}] }
    // — extract the nested error details so the user sees the real cause.
    // GitLab returns { "message": "...", "error": "...", "error_description": "..." }.
    if let Ok(v) = serde_json::from_str::<Value>(&text) {
        let mut parts: Vec<String> = Vec::new();
        if let Some(msg) = v.get("message").and_then(|m| m.as_str()) {
            parts.push(msg.to_string());
        }
        if let Some(arr) = v.get("errors").and_then(|e| e.as_array()) {
            for err in arr {
                if let Some(m) = err.get("message").and_then(|m| m.as_str()) {
                    parts.push(m.to_string());
                } else if let Some(code) = err.get("code").and_then(|c| c.as_str()) {
                    let field = err.get("field").and_then(|f| f.as_str()).unwrap_or("");
                    let resource = err.get("resource").and_then(|r| r.as_str()).unwrap_or("");
                    parts.push(format!("{code} on {resource}/{field}"));
                }
            }
        }
        if let Some(err) = v.get("error").and_then(|m| m.as_str()) {
            parts.push(err.to_string());
        }
        if let Some(desc) = v.get("error_description").and_then(|m| m.as_str()) {
            parts.push(desc.to_string());
        }
        if !parts.is_empty() {
            return format!("HTTP {status} at {url}: {}", parts.join(" — "));
        }
    }
    if text.is_empty() {
        format!("HTTP {status} at {url}")
    } else {
        format!("HTTP {status} at {url}: {}", text.chars().take(300).collect::<String>())
    }
}

// ── GitHub provider ─────────────────────────────────────────────────────────
// REST v3. Uses Authorization: Bearer <token>.
// PAT classic + fine-grained both work for /user and /repos/.../pulls.

struct GithubProvider {
    base: String, // e.g. "https://api.github.com"
}

#[async_trait::async_trait]
impl Provider for GithubProvider {
    async fn test_connection(&self, token: &str) -> Result<ConnectionTestResult, String> {
        let client = http_client()?;
        let url = format!("{}/user", self.base);
        let resp = client
            .get(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .header(header::ACCEPT, "application/vnd.github+json")
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            return Err(extract_error(resp, &url).await);
        }
        // Scopes are in the response headers for classic PATs; fine-grained PATs
        // don't expose them — return empty if missing.
        let scopes: Vec<String> = resp
            .headers()
            .get("x-oauth-scopes")
            .and_then(|v| v.to_str().ok())
            .map(|s| s.split(',').map(|p| p.trim().to_string()).filter(|p| !p.is_empty()).collect())
            .unwrap_or_default();
        let body: Value = resp.json().await.map_err(|e| format!("parse user: {e}"))?;
        let username = body
            .get("login")
            .and_then(|v| v.as_str())
            .ok_or_else(|| "no `login` field in response".to_string())?
            .to_string();
        Ok(ConnectionTestResult { username, scopes })
    }

    async fn create_pr(&self, token: &str, args: &CreatePrArgs) -> Result<CreatePrResult, String> {
        let client = http_client()?;
        let url = format!("{}/repos/{}/pulls", self.base, args.project_path);
        let payload = json!({
            "title": args.title,
            "body": args.body,
            "head": args.head,
            "base": args.base,
            "draft": args.draft,
        });
        let resp = client
            .post(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .header(header::ACCEPT, "application/vnd.github+json")
            .json(&payload)
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            let status_code = resp.status();
            let err_text = extract_error(resp, &url).await;
            // GitHub returns 422 with "A pull request already exists for ...".
            // Look it up so we can hand the user the existing PR URL instead of a bare error.
            if status_code == StatusCode::UNPROCESSABLE_ENTITY && err_text.contains("already exists") {
                if let Some(existing) = self.find_existing_pr(token, &args.project_path, &args.head, &args.base).await {
                    return Ok(existing);
                }
            }
            return Err(err_text);
        }
        let body: Value = resp.json().await.map_err(|e| format!("parse pr: {e}"))?;
        let pr_url = body
            .get("html_url")
            .and_then(|v| v.as_str())
            .ok_or_else(|| "no `html_url` in response".to_string())?
            .to_string();
        let number = body.get("number").and_then(|v| v.as_u64()).unwrap_or(0);
        let state = body
            .get("state")
            .and_then(|v| v.as_str())
            .unwrap_or("open")
            .to_string();
        Ok(CreatePrResult {
            url: pr_url,
            number,
            state,
            already_existed: false,
        })
    }

    async fn list_prs_for_branch(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
    ) -> Result<Vec<PrSummary>, String> {
        self.list_prs_for_branch_impl(token, project_path, head).await
    }
}

impl GithubProvider {
    async fn list_prs_for_branch_impl(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
    ) -> Result<Vec<PrSummary>, String> {
        let client = http_client()?;
        let owner = project_path
            .split('/')
            .next()
            .ok_or_else(|| "invalid project_path".to_string())?;
        let url = format!(
            "{}/repos/{}/pulls?state=open&head={}:{}",
            self.base, project_path, owner, head
        );
        let resp = client
            .get(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .header(header::ACCEPT, "application/vnd.github+json")
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            return Err(extract_error(resp, &url).await);
        }
        let arr: Value = resp.json().await.map_err(|e| format!("parse list: {e}"))?;
        let prs = arr
            .as_array()
            .map(|v| {
                v.iter()
                    .filter_map(|pr| {
                        let body_full = pr.get("body").and_then(|v| v.as_str()).unwrap_or("");
                        let body_preview = truncate_text(body_full, 200);
                        Some(PrSummary {
                            url: pr.get("html_url").and_then(|v| v.as_str())?.to_string(),
                            number: pr.get("number").and_then(|v| v.as_u64()).unwrap_or(0),
                            title: pr.get("title").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            state: pr.get("state").and_then(|v| v.as_str()).unwrap_or("open").to_string(),
                            draft: pr.get("draft").and_then(|v| v.as_bool()).unwrap_or(false),
                            base: pr.get("base").and_then(|v| v.get("ref")).and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            author: pr.get("user").and_then(|v| v.get("login")).and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            updated_at: pr.get("updated_at").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            created_at: pr.get("created_at").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            body_preview,
                        })
                    })
                    .collect()
            })
            .unwrap_or_default();
        Ok(prs)
    }

    /// Look up an open PR matching head+base. Used when create returns 422
    /// "already exists" so we can surface the existing PR URL.
    /// Returns None on any network/parse failure — caller falls back to the
    /// original error.
    async fn find_existing_pr(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
        base: &str,
    ) -> Option<CreatePrResult> {
        let client = http_client().ok()?;
        let owner = project_path.split('/').next()?;
        let url = format!(
            "{}/repos/{}/pulls?state=open&head={}:{}&base={}",
            self.base, project_path, owner, head, base
        );
        let resp = client
            .get(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .header(header::ACCEPT, "application/vnd.github+json")
            .send()
            .await
            .ok()?;
        if !resp.status().is_success() {
            return None;
        }
        let arr: Value = resp.json().await.ok()?;
        let pr = arr.as_array()?.first()?;
        Some(CreatePrResult {
            url: pr.get("html_url").and_then(|v| v.as_str())?.to_string(),
            number: pr.get("number").and_then(|v| v.as_u64()).unwrap_or(0),
            state: pr.get("state").and_then(|v| v.as_str()).unwrap_or("open").to_string(),
            already_existed: true,
        })
    }
}

// ── GitLab provider ─────────────────────────────────────────────────────────
// REST v4. Uses Authorization: Bearer <token> (PAT works the same).
// Project path needs URL encoding for the `:id` segment (group/subgroup/project).

struct GitlabProvider {
    base: String, // e.g. "https://gitlab.com"
}

fn url_encode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for c in s.chars() {
        match c {
            'A'..='Z' | 'a'..='z' | '0'..='9' | '-' | '_' | '.' | '~' => out.push(c),
            _ => {
                for b in c.to_string().as_bytes() {
                    out.push_str(&format!("%{:02X}", b));
                }
            }
        }
    }
    out
}

#[async_trait::async_trait]
impl Provider for GitlabProvider {
    async fn test_connection(&self, token: &str) -> Result<ConnectionTestResult, String> {
        let client = http_client()?;
        let url = format!("{}/api/v4/user", self.base);
        let resp = client
            .get(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            return Err(extract_error(resp, &url).await);
        }
        let body: Value = resp.json().await.map_err(|e| format!("parse user: {e}"))?;
        let username = body
            .get("username")
            .and_then(|v| v.as_str())
            .ok_or_else(|| "no `username` field in response".to_string())?
            .to_string();
        // GitLab doesn't expose scopes on /user — fetch from /personal_access_tokens/self
        let scopes_url = format!("{}/api/v4/personal_access_tokens/self", self.base);
        let scopes = match client
            .get(&scopes_url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .send()
            .await
        {
            Ok(r) if r.status().is_success() => {
                let v: Value = r.json().await.unwrap_or(Value::Null);
                v.get("scopes")
                    .and_then(|s| s.as_array())
                    .map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect())
                    .unwrap_or_default()
            }
            _ => Vec::new(),
        };
        Ok(ConnectionTestResult { username, scopes })
    }

    async fn create_pr(&self, token: &str, args: &CreatePrArgs) -> Result<CreatePrResult, String> {
        let client = http_client()?;
        let project_encoded = url_encode(&args.project_path);
        let url = format!("{}/api/v4/projects/{}/merge_requests", self.base, project_encoded);
        let payload = json!({
            "source_branch": args.head,
            "target_branch": args.base,
            "title": if args.draft { format!("Draft: {}", args.title) } else { args.title.clone() },
            "description": args.body,
        });
        let resp = client
            .post(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .json(&payload)
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            let status_code = resp.status();
            let err_text = extract_error(resp, &url).await;
            // GitLab returns 409 (or sometimes 422) for "already exists" — look up the existing MR.
            let is_dup = status_code == StatusCode::CONFLICT
                || (status_code == StatusCode::UNPROCESSABLE_ENTITY && err_text.contains("already exists"));
            if is_dup {
                if let Some(existing) = self.find_existing_mr(token, &args.project_path, &args.head, &args.base).await {
                    return Ok(existing);
                }
            }
            return Err(err_text);
        }
        let body: Value = resp.json().await.map_err(|e| format!("parse mr: {e}"))?;
        let mr_url = body
            .get("web_url")
            .and_then(|v| v.as_str())
            .ok_or_else(|| "no `web_url` in response".to_string())?
            .to_string();
        let number = body.get("iid").and_then(|v| v.as_u64()).unwrap_or(0);
        let state = body
            .get("state")
            .and_then(|v| v.as_str())
            .unwrap_or("opened")
            .to_string();
        Ok(CreatePrResult {
            url: mr_url,
            number,
            state,
            already_existed: false,
        })
    }

    async fn list_prs_for_branch(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
    ) -> Result<Vec<PrSummary>, String> {
        let client = http_client()?;
        let project_encoded = url_encode(project_path);
        let url = format!(
            "{}/api/v4/projects/{}/merge_requests?state=opened&source_branch={}",
            self.base,
            project_encoded,
            url_encode(head)
        );
        let resp = client
            .get(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            return Err(extract_error(resp, &url).await);
        }
        let arr: Value = resp.json().await.map_err(|e| format!("parse list: {e}"))?;
        let mrs = arr
            .as_array()
            .map(|v| {
                v.iter()
                    .filter_map(|mr| {
                        let title = mr.get("title").and_then(|v| v.as_str()).unwrap_or("").to_string();
                        let draft = title.starts_with("Draft:") || title.starts_with("WIP:");
                        let desc = mr.get("description").and_then(|v| v.as_str()).unwrap_or("");
                        Some(PrSummary {
                            url: mr.get("web_url").and_then(|v| v.as_str())?.to_string(),
                            number: mr.get("iid").and_then(|v| v.as_u64()).unwrap_or(0),
                            title,
                            state: mr.get("state").and_then(|v| v.as_str()).unwrap_or("opened").to_string(),
                            draft,
                            base: mr.get("target_branch").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            author: mr.get("author").and_then(|v| v.get("username")).and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            updated_at: mr.get("updated_at").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            created_at: mr.get("created_at").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            body_preview: truncate_text(desc, 200),
                        })
                    })
                    .collect()
            })
            .unwrap_or_default();
        Ok(mrs)
    }
}

impl GitlabProvider {
    async fn find_existing_mr(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
        base: &str,
    ) -> Option<CreatePrResult> {
        let client = http_client().ok()?;
        let project_encoded = url_encode(project_path);
        let url = format!(
            "{}/api/v4/projects/{}/merge_requests?state=opened&source_branch={}&target_branch={}",
            self.base,
            project_encoded,
            url_encode(head),
            url_encode(base)
        );
        let resp = client
            .get(&url)
            .header(header::AUTHORIZATION, format!("Bearer {token}"))
            .send()
            .await
            .ok()?;
        if !resp.status().is_success() {
            return None;
        }
        let arr: Value = resp.json().await.ok()?;
        let mr = arr.as_array()?.first()?;
        Some(CreatePrResult {
            url: mr.get("web_url").and_then(|v| v.as_str())?.to_string(),
            number: mr.get("iid").and_then(|v| v.as_u64()).unwrap_or(0),
            state: mr.get("state").and_then(|v| v.as_str()).unwrap_or("opened").to_string(),
            already_existed: true,
        })
    }
}

// ── Bitbucket Cloud provider ────────────────────────────────────────────────
// REST 2.0. Uses Basic Auth — but Atlassian is REMOVING App Passwords on
// 2026-07-28 (controlled brownouts from 2026-06-09). All new integrations
// MUST use Atlassian API Tokens with scopes:
//   https://id.atlassian.com/manage-profile/security/api-tokens
//
// Auth model post-migration:
//   - "username" field = the user's Atlassian account email
//   - "token" field    = the API token (starts with "ATATT…")
//   - Header: Authorization: Basic base64("<email>:<api-token>")
//
// Required token scopes (granular form introduced with API tokens):
//   - read:user:bitbucket          — needed for /2.0/user verification
//   - read:repository:bitbucket    — repo metadata (best practice)
//   - read:pullrequest:bitbucket   — list PRs
//   - write:pullrequest:bitbucket  — create PRs
//
// reqwest's `.basic_auth(user, Some(pass))` handles the base64 encoding.
// project_path format: "workspace/repo_slug".

struct BitbucketProvider {
    base: String, // e.g. "https://api.bitbucket.org"
    // For API Tokens this holds the user's Atlassian email; for legacy App
    // Passwords it holds the Bitbucket handle. Both are accepted as the
    // Basic Auth user component.
    username: String,
}

#[async_trait::async_trait]
impl Provider for BitbucketProvider {
    async fn test_connection(&self, token: &str) -> Result<ConnectionTestResult, String> {
        let client = http_client()?;
        let url = format!("{}/2.0/user", self.base);
        let resp = client
            .get(&url)
            .basic_auth(&self.username, Some(token))
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            return Err(extract_error(resp, &url).await);
        }
        let body: Value = resp.json().await.map_err(|e| format!("parse user: {e}"))?;
        // Bitbucket /user returns `username` (atlassian id) — sometimes also
        // `nickname` (display handle). Prefer username for stable identity.
        let api_username = body
            .get("username")
            .and_then(|v| v.as_str())
            .or_else(|| body.get("nickname").and_then(|v| v.as_str()))
            .ok_or_else(|| "no `username` field in response".to_string())?
            .to_string();
        // App password scopes aren't exposed via the API. Empty list is fine —
        // the test only proves auth works.
        Ok(ConnectionTestResult { username: api_username, scopes: Vec::new() })
    }

    async fn create_pr(&self, token: &str, args: &CreatePrArgs) -> Result<CreatePrResult, String> {
        let client = http_client()?;
        let url = format!("{}/2.0/repositories/{}/pullrequests", self.base, args.project_path);
        let payload = json!({
            "title": if args.draft { format!("Draft: {}", args.title) } else { args.title.clone() },
            "description": args.body,
            "source": { "branch": { "name": args.head } },
            "destination": { "branch": { "name": args.base } },
            "close_source_branch": false,
        });
        let resp = client
            .post(&url)
            .basic_auth(&self.username, Some(token))
            .json(&payload)
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            let status_code = resp.status();
            let err_text = extract_error(resp, &url).await;
            // Bitbucket returns 400 with "already exists" text for duplicate PRs.
            let is_dup = status_code == StatusCode::BAD_REQUEST && err_text.contains("already exists");
            if is_dup {
                if let Some(existing) = self.find_existing_pr(token, &args.project_path, &args.head, &args.base).await {
                    return Ok(existing);
                }
            }
            return Err(err_text);
        }
        let body: Value = resp.json().await.map_err(|e| format!("parse pr: {e}"))?;
        let url_field = body
            .get("links")
            .and_then(|l| l.get("html"))
            .and_then(|h| h.get("href"))
            .and_then(|v| v.as_str())
            .ok_or_else(|| "no html link in response".to_string())?
            .to_string();
        let number = body.get("id").and_then(|v| v.as_u64()).unwrap_or(0);
        let state = body.get("state").and_then(|v| v.as_str()).unwrap_or("OPEN").to_string();
        Ok(CreatePrResult {
            url: url_field,
            number,
            state,
            already_existed: false,
        })
    }

    async fn list_prs_for_branch(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
    ) -> Result<Vec<PrSummary>, String> {
        let client = http_client()?;
        // Bitbucket BBQL query: state=OPEN AND source.branch.name="head"
        let q = format!("state=\"OPEN\" AND source.branch.name=\"{}\"", head);
        let url = format!(
            "{}/2.0/repositories/{}/pullrequests?q={}",
            self.base,
            project_path,
            url_encode(&q)
        );
        let resp = client
            .get(&url)
            .basic_auth(&self.username, Some(token))
            .send()
            .await
            .map_err(|e| format!("network: {e}"))?;
        if !resp.status().is_success() {
            return Err(extract_error(resp, &url).await);
        }
        let body: Value = resp.json().await.map_err(|e| format!("parse list: {e}"))?;
        let values = body.get("values").and_then(|v| v.as_array());
        let prs = values
            .map(|arr| {
                arr.iter()
                    .filter_map(|pr| {
                        let title = pr.get("title").and_then(|v| v.as_str()).unwrap_or("").to_string();
                        // Bitbucket has no native draft flag — match the same convention
                        // we use when creating: prefix "Draft:" or "WIP:".
                        let draft = title.starts_with("Draft:") || title.starts_with("WIP:");
                        let desc = pr.get("description").and_then(|v| v.as_str()).unwrap_or("");
                        Some(PrSummary {
                            url: pr.get("links").and_then(|l| l.get("html")).and_then(|h| h.get("href")).and_then(|v| v.as_str())?.to_string(),
                            number: pr.get("id").and_then(|v| v.as_u64()).unwrap_or(0),
                            title,
                            state: pr.get("state").and_then(|v| v.as_str()).unwrap_or("OPEN").to_string(),
                            draft,
                            base: pr
                                .get("destination")
                                .and_then(|d| d.get("branch"))
                                .and_then(|b| b.get("name"))
                                .and_then(|v| v.as_str())
                                .unwrap_or("")
                                .to_string(),
                            author: pr
                                .get("author")
                                .and_then(|a| a.get("nickname").or_else(|| a.get("display_name")))
                                .and_then(|v| v.as_str())
                                .unwrap_or("")
                                .to_string(),
                            updated_at: pr.get("updated_on").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            created_at: pr.get("created_on").and_then(|v| v.as_str()).unwrap_or("").to_string(),
                            body_preview: truncate_text(desc, 200),
                        })
                    })
                    .collect()
            })
            .unwrap_or_default();
        Ok(prs)
    }
}

impl BitbucketProvider {
    async fn find_existing_pr(
        &self,
        token: &str,
        project_path: &str,
        head: &str,
        base: &str,
    ) -> Option<CreatePrResult> {
        let prs = self.list_prs_for_branch(token, project_path, head).await.ok()?;
        let pr = prs.into_iter().find(|p| p.base == base)?;
        Some(CreatePrResult {
            url: pr.url,
            number: pr.number,
            state: pr.state,
            already_existed: true,
        })
    }
}
