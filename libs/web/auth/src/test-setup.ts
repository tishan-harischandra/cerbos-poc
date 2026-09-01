import '@angular/compiler';
import '@analogjs/vitest-angular/setup-snapshots';
import { setupTestBed } from '@analogjs/vitest-angular/setup-testbed';

// jsdom implements crypto.getRandomValues but not SubtleCrypto, which the
// PKCE code challenge (RFC 7636 §4.2) and the hospital switcher's silent
// re-authentication (issue #84) both need. Node's own Web Crypto has it.
if (!globalThis.crypto?.subtle) {
  const { webcrypto } = await import('node:crypto');
  Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });
}

setupTestBed();
