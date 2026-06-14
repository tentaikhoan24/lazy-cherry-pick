// Shared conflict-marker parsing + render-line building.
// Single source of truth used by both the interactive merge editor
// (`routes/conflict/+page.svelte`) and the read-only AI review pane
// (`ConflictThreePane.svelte`). Keep UI-free — pure functions only.

export interface ContextPart  { kind: "context";  lines: string[] }
export interface ConflictPart { kind: "conflict"; ours: string[]; theirs: string[] }
export type Part = ContextPart | ConflictPart;

export interface RenderLine {
  text: string | null;
  kind: "context" | "ours" | "theirs" | "filler" | "conflict-header";
  conflictIdx: number;
  lineNum: number | null;
}
export interface Rendered { left: RenderLine[]; right: RenderLine[]; conflictStarts: number[] }

/** Normalize CRLF/CR → LF so parsing and line counting are consistent. */
export function normalizeConflictText(raw: string): string {
  return raw.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

/** Split raw conflict-marker text into ordered context / conflict parts. */
export function parseConflict(raw: string): Part[] {
  const lines = normalizeConflictText(raw).split("\n");
  const result: Part[] = [];
  let st: "ctx" | "ours" | "theirs" = "ctx";
  let ctx: string[] = [], ours: string[] = [], theirs: string[] = [];
  for (const ln of lines) {
    if (st === "ctx" && ln.startsWith("<<<<<<<")) {
      if (ctx.length) result.push({ kind: "context", lines: ctx });
      ctx = []; ours = []; st = "ours";
    } else if (st === "ours" && ln.startsWith("=======")) {
      theirs = []; st = "theirs";
    } else if (st === "theirs" && ln.startsWith(">>>>>>>")) {
      result.push({ kind: "conflict", ours, theirs });
      st = "ctx"; ctx = [];
    } else if (st === "ours") { ours.push(ln); }
    else if (st === "theirs") { theirs.push(ln); }
    else { ctx.push(ln); }
  }
  if (ctx.length) result.push({ kind: "context", lines: ctx });
  return result;
}

/**
 * Build aligned left (Theirs) / right (Ours) render lines with per-pane
 * line numbers. Conflict blocks emit a `conflict-header` marker row plus
 * `filler` rows so both panes stay vertically aligned.
 */
export function buildRenderLines(parts: Part[]): Rendered {
  const left: RenderLine[] = [];
  const right: RenderLine[] = [];
  const conflictStarts: number[] = [];
  let ci = 0, lNum = 1, rNum = 1;
  for (const part of parts) {
    if (part.kind === "context") {
      for (const ln of part.lines) {
        left.push({ text: ln, kind: "context", conflictIdx: -1, lineNum: lNum++ });
        right.push({ text: ln, kind: "context", conflictIdx: -1, lineNum: rNum++ });
      }
    } else {
      conflictStarts.push(left.length);
      left.push({ text: null, kind: "conflict-header", conflictIdx: ci, lineNum: null });
      right.push({ text: null, kind: "conflict-header", conflictIdx: ci, lineNum: null });
      const max = Math.max(part.theirs.length, part.ours.length);
      for (let i = 0; i < max; i++) {
        left.push(i < part.theirs.length
          ? { text: part.theirs[i], kind: "theirs", conflictIdx: ci, lineNum: lNum++ }
          : { text: null, kind: "filler", conflictIdx: ci, lineNum: null });
        right.push(i < part.ours.length
          ? { text: part.ours[i], kind: "ours", conflictIdx: ci, lineNum: rNum++ }
          : { text: null, kind: "filler", conflictIdx: ci, lineNum: null });
      }
      ci++;
    }
  }
  return { left, right, conflictStarts };
}
