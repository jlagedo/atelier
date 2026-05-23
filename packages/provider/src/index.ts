// Provider seam (design.md §13): the one place provider specifics live. Swap the
// base URL + auth HERE only; nothing in the agent loop changes.

export interface ProviderConfig {
  /** Model id passed to the SDK, e.g. "claude-sonnet-4-6". */
  model: string;
  /** Env the SDK subprocess needs: ANTHROPIC_API_KEY (+ optional ANTHROPIC_BASE_URL). */
  env: Record<string, string>;
}

export interface ResolveOptions {
  model?: string;
}

const DEFAULT_MODEL = "claude-sonnet-4-6";

/**
 * Resolve the provider: assert credentials are present, pick the model, and
 * return the env the SDK needs. Throws a clear error if the key is missing
 * rather than letting the SDK fail deep inside a subprocess.
 */
export function resolveProvider(opts: ResolveOptions = {}): ProviderConfig {
  const apiKey = process.env.ANTHROPIC_API_KEY;
  if (!apiKey) {
    throw new Error(
      "ANTHROPIC_API_KEY is not set. Set it in the environment before running the agent " +
        '(PowerShell: $env:ANTHROPIC_API_KEY = "sk-ant-...").',
    );
  }

  const model = opts.model ?? process.env.ATELIER_MODEL ?? DEFAULT_MODEL;

  const env: Record<string, string> = { ANTHROPIC_API_KEY: apiKey };
  // Optional base URL override: reroute sampling without touching the loop.
  const baseUrl = process.env.ANTHROPIC_BASE_URL;
  if (baseUrl) env.ANTHROPIC_BASE_URL = baseUrl;

  return { model, env };
}
