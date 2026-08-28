import { HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';

import { AuthService } from './auth.service';

/**
 * Attaches this application's own access token to every call it makes to
 * the server. Without it the ADS sees an anonymous request and answers
 * 401, which is exactly what the capability snapshot used to receive.
 *
 * Scoped to /api/ deliberately: the token exchange in AuthService goes to
 * Keycloak, a different origin and a different trust boundary, and must
 * never carry this token.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  if (!req.url.startsWith('/api/')) {
    return next(req);
  }
  const token = inject(AuthService).accessToken();
  if (!token) {
    return next(req);
  }
  return next(req.clone({ setHeaders: { Authorization: `Bearer ${token}` } }));
};
