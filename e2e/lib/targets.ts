// Targets: the reference Redmine 6.1.2 (with test fixtures) and the buropher candidate
// (the same fixtures exported and imported). Both are reset to their pristine databases
// before each scenario so that both sides start from identical state.
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';

export const REPO_ROOT = path.resolve(__dirname, '..', '..');

function findReference(): string {
  let d = REPO_ROOT;
  while (d !== path.dirname(d)) {
    if (fs.existsSync(path.join(d, '_reference', 'redmine-migrated'))) return path.join(d, '_reference');
    d = path.dirname(d);
  }
  return path.join(REPO_ROOT, '_reference');
}

export const REFERENCE_DIR = findReference();

export interface Target {
  name: 'ref' | 'cand';
  baseURL: string;
  reset(): void;
}

const refPort = process.env.COMPAT_REF_PORT ?? '4035';
const candPort = process.env.BUROPHER_CAND_PORT ?? '4135';
const refDir = process.env.COMPAT_REF_DIR ?? path.join(REFERENCE_DIR, 'ref-e2e');
const candDir = process.env.BUROPHER_CAND_DIR ?? path.join(REPO_ROOT, 'data', 'cand');
// E2E_NO_RESET=1 skips resets (useful while writing a spec against already running servers).
const noReset = process.env.E2E_NO_RESET === '1';

function run(script: string, env: Record<string, string>) {
  if (noReset) return;
  execFileSync(path.join(REPO_ROOT, 'tools', 'compat', script), ['reset'], {
    env: { ...process.env, ...env },
    stdio: ['ignore', 'ignore', 'pipe'],
    timeout: 180_000,
  });
}

export const ref: Target = {
  name: 'ref',
  baseURL: process.env.E2E_REF_URL ?? `http://127.0.0.1:${refPort}`,
  reset: () => run('redmine-ref.sh', { COMPAT_REF_DIR: refDir, COMPAT_REF_PORT: refPort }),
};

export const cand: Target = {
  name: 'cand',
  baseURL: process.env.E2E_CAND_URL ?? `http://127.0.0.1:${candPort}`,
  reset: () => run('buropher-cand.sh', { BUROPHER_CAND_DIR: candDir, BUROPHER_CAND_PORT: candPort }),
};

export const targets: Target[] = [ref, cand];
