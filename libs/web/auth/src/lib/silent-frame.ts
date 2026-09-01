import { InjectionToken } from '@angular/core';

/**
 * Runs an OIDC authorization request in a hidden iframe against an
 * existing SSO session (issue #84's silent re-authentication) and
 * resolves with the same-origin URL the iframe eventually navigates back
 * to - the redirect URI, carrying either a code or an error in its query
 * string.
 *
 * Rejects if the iframe never reaches that redirect URI within
 * timeoutMs: a session that actually needs an interactive screen (Keycloak
 * cannot satisfy `prompt=none` silently) navigates the iframe to a login
 * or error page on Keycloak's own origin instead, which this can only ever
 * observe as "never arrived" - reading a cross-origin frame's location is
 * exactly what the browser's same-origin policy forbids, so this seam
 * cannot distinguish "denied" from "still loading" beyond that.
 */
export type SilentFrame = (authorizeUrl: string, redirectUri: string, timeoutMs?: number) => Promise<string>;

const DEFAULT_TIMEOUT_MS = 10_000;

function runSilentFrame(authorizeUrl: string, redirectUri: string, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<string> {
  return new Promise((resolve, reject) => {
    const iframe = document.createElement('iframe');
    iframe.style.display = 'none';

    let settled = false;
    const finish = (action: () => void) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      iframe.removeEventListener('load', onLoad);
      action();
      iframe.remove();
    };

    const timer = setTimeout(() => {
      finish(() => reject(new Error('silent switch timed out waiting for a response')));
    }, timeoutMs);

    const onLoad = () => {
      let href: string;
      try {
        // Same-origin only: a cross-origin frame (Keycloak's own login or
        // error page) throws reading .href, which is the interactive-
        // challenge case the type above documents.
        href = iframe.contentWindow?.location.href ?? '';
      } catch {
        return;
      }
      if (!href.startsWith(redirectUri)) {
        return;
      }
      finish(() => resolve(href));
    };

    iframe.addEventListener('load', onLoad);
    document.body.appendChild(iframe);
    iframe.src = authorizeUrl;
  });
}

export const SILENT_FRAME = new InjectionToken<SilentFrame>('SILENT_FRAME', {
  providedIn: 'root',
  factory: () => runSilentFrame,
});
