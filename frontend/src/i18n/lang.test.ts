import { describe, expect, it } from 'vitest';
import { dictionaries } from './lang';

// Parity gate for the SPA dictionary (Phase 4): every English key must
// exist with a non-empty French string, and vice versa — a key present in
// only one locale fails loudly so it gets adopted or removed, never
// silently drifted. t() falls back to English at runtime; this test keeps
// the fallback a safety net, not a habit.
describe('i18n dictionary parity', () => {
  it('keeps en/fr key sets identical with translated values', () => {
    const en = dictionaries.en;
    const fr = dictionaries.fr;
    const enKeys = new Set(Object.keys(en));
    const frKeys = new Set(Object.keys(fr));
    const missingInFr = [...enKeys].filter((k) => !frKeys.has(k));
    const missingInEn = [...frKeys].filter((k) => !enKeys.has(k));
    expect(missingInFr).toEqual([]);
    expect(missingInEn).toEqual([]);
    const untranslated = (Object.keys(en) as (keyof typeof en)[]).filter(
      (k) => fr[k] === en[k] && !cognates.has(k as string),
    );
    expect(untranslated).toEqual([]);
  });
});

// Cognates and loanwords legitimately identical in French (mirrors the
// backend catalogue test's allowlist): any NEW identical pair fails until
// a reviewer adopts it here with justification.
const cognates = new Set([
  'commerce',
  'finance',
  'services',
  'documents',
  'agenda',
  'financeNav',
  'admin',
  'actions',
  'code',
  'description',
  'total',
  'date',
  'type',
  'message',
  'session',
  'journal',
  'charges',
  'stock',
  'slug',
  'question',
  'option',
  'votes',
  'port',
  'stMaintenance',
]);
