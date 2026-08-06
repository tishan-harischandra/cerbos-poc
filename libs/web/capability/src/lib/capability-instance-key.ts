import { HttpContextToken } from '@angular/common/http';

/**
 * CAPABILITY_INSTANCE_KEY names the capability snapshot instance a business
 * HTTP request depends on, so a 403 response can invalidate exactly that
 * snapshot before retrying (§12.6). Business code sets it explicitly:
 *
 * ```ts
 * this.http.get(url, {
 *   context: new HttpContext().set(CAPABILITY_INSTANCE_KEY, 'patient:patient-456'),
 * });
 * ```
 *
 * A request that never sets it is treated as module- or collection-scoped;
 * a 403 against it invalidates every cached instance snapshot instead of
 * one in particular.
 */
export const CAPABILITY_INSTANCE_KEY = new HttpContextToken<string | undefined>(
  () => undefined,
);
