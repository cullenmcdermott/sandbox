// Structured logging for the runner ([T2]).
//
// Before this, the runner's diagnostics were ~30 ad-hoc `console.*` calls with
// no level, no timestamp, no session id, and no correlation id — and the HTTP
// surface logged exactly one line ever, at listen. Nothing could ingest any of
// it, and `kubectl logs` was the only reader.
//
// The constraint that shapes the design: `kubectl logs` MUST stay readable by
// eye. So the default output is human text, one line per record, with the
// structure carried as trailing key=value pairs:
//
//   2026-07-27T10:11:12.000Z INFO  opencode: serve listening port=4096
//   2026-07-27T10:11:13.000Z ERROR events: persist failed type=turn.started err="disk full"
//
// Set SANDBOX_LOG_FORMAT=json to emit newline-delimited JSON objects instead
// (same fields, machine-ingestible) — the shape [T10] wants to feed an OTLP
// collector. SANDBOX_LOG_LEVEL (debug|info|warn|error, default info) gates both.
//
// Every log record carries the session id once it is known, so a multi-session
// log aggregator can partition by it without parsing message text.

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

const LEVEL_ORDER: Record<LogLevel, number> = { debug: 10, info: 20, warn: 30, error: 40 };

/** Structured fields attached to one record. Values are JSON-encoded in json
 * mode and rendered as `key=value` in text mode; an Error is unwrapped to its
 * message (plus stack at debug level) rather than serializing as `{}`. */
export type LogFields = Record<string, unknown>;

export interface Logger {
  debug(msg: string, fields?: LogFields): void;
  info(msg: string, fields?: LogFields): void;
  warn(msg: string, fields?: LogFields): void;
  error(msg: string, fields?: LogFields): void;
  /** Derive a logger with additional pinned fields (e.g. a request's traceId). */
  child(fields: LogFields): Logger;
}

/** Sink + policy seam. The default writes to stdout/stderr; tests inject a
 * collector, and every knob is explicit so a test never depends on ambient env. */
export interface LogDeps {
  level?: LogLevel;
  format?: 'text' | 'json';
  now?: () => Date;
  write?: (line: string, level: LogLevel) => void;
}

function envLevel(): LogLevel {
  const v = (process.env.SANDBOX_LOG_LEVEL ?? '').toLowerCase();
  return v === 'debug' || v === 'info' || v === 'warn' || v === 'error' ? v : 'info';
}

function envFormat(): 'text' | 'json' {
  return (process.env.SANDBOX_LOG_FORMAT ?? '').toLowerCase() === 'json' ? 'json' : 'text';
}

// The session id is process-wide (one session per pod) and is not known until
// config loads, so it is set once at boot rather than threaded through every
// createLogger call site.
let sessionId = '';

/** Record the pod's session id on every subsequent log line. Called once at boot. */
export function setLogSessionId(id: string): void {
  sessionId = id;
}

/** Unwrap an Error into loggable primitives. Without this an `err` field
 * JSON-serializes to `{}` — the single most common way a structured logger
 * loses the actual failure. */
function normalizeFields(fields: LogFields, level: LogLevel): LogFields {
  const out: LogFields = {};
  for (const [k, v] of Object.entries(fields)) {
    if (v instanceof Error) {
      out[k] = v.message;
      if (level === 'debug' && v.stack) out[`${k}Stack`] = v.stack;
    } else {
      out[k] = v;
    }
  }
  return out;
}

/** Render a value for text mode: quote anything containing whitespace so
 * `key=value` pairs stay parseable by eye and by awk. */
function textValue(v: unknown): string {
  if (v === null || v === undefined) return '';
  const s = typeof v === 'string' ? v : JSON.stringify(v);
  return s !== undefined && /[\s"]/.test(s) ? JSON.stringify(s) : String(s);
}

function defaultWrite(line: string, level: LogLevel): void {
  // warn/error to stderr so a log scraper can separate them; kubectl logs
  // interleaves both, which is what we want for eyeball debugging.
  if (level === 'warn' || level === 'error') process.stderr.write(line + '\n');
  else process.stdout.write(line + '\n');
}

/**
 * Create a logger for one component (the module or subsystem name that appears
 * in every line: `opencode`, `events`, `claude-pane`, `http`, …).
 */
export function createLogger(component: string, deps: LogDeps = {}, pinned: LogFields = {}): Logger {
  const minLevel = LEVEL_ORDER[deps.level ?? envLevel()];
  const format = deps.format ?? envFormat();
  const now = deps.now ?? ((): Date => new Date());
  const write = deps.write ?? defaultWrite;

  function emit(level: LogLevel, msg: string, fields?: LogFields): void {
    if (LEVEL_ORDER[level] < minLevel) return;
    const merged = normalizeFields({ ...pinned, ...(fields ?? {}) }, level);
    const ts = now().toISOString();
    if (format === 'json') {
      write(
        JSON.stringify({
          ts,
          level,
          component,
          ...(sessionId !== '' ? { sessionId } : {}),
          msg,
          ...merged,
        }),
        level,
      );
      return;
    }
    const pairs = Object.entries(merged).map(([k, v]) => `${k}=${textValue(v)}`);
    // sessionId is deliberately NOT in the text line: one session per pod makes
    // it constant noise there, while json mode (which a multi-pod aggregator
    // reads) always carries it.
    write(
      `${ts} ${level.toUpperCase().padEnd(5)} ${component}: ${msg}${pairs.length > 0 ? ' ' + pairs.join(' ') : ''}`,
      level,
    );
  }

  return {
    debug: (m, f) => emit('debug', m, f),
    info: (m, f) => emit('info', m, f),
    warn: (m, f) => emit('warn', m, f),
    error: (m, f) => emit('error', m, f),
    child: (fields) => createLogger(component, deps, { ...pinned, ...fields }),
  };
}

/**
 * Write a pre-formatted line straight to stdout, bypassing the record envelope.
 *
 * This exists for exactly one caller: the SANDBOX_TRACE spans in `trace.ts`,
 * whose `trace: <id> <name> <ms>ms` shape is a documented, greppable contract
 * (docs/architecture.md, `sandbox trace`) that predates this logger. Wrapping
 * those lines in a log envelope would break every grep recipe written against
 * them. Nothing else should use this — use a component logger.
 */
export function logRaw(line: string): void {
  process.stdout.write(line + '\n');
}
