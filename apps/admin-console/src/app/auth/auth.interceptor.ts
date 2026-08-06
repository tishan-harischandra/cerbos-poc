import { HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';

import { AuthService } from './auth.service';

/**
 * Attaches the console's own access token to every call the browser makes
 * to the server (§16.1: the browser holds only its own token, never an IdP
 * admin credential). Requests to the identity provider itself - the token
 * exchange and the authorize/logout redirects - never go through
 * HttpClient with this interceptor attached to them in the same way the
 * redirects are full navigations, and the token exchange in AuthService
 * intentionally carries no Authorization header of its own.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  if (!req.url.startsWith('/api/')) {
    return next(req);
  }
  const auth = inject(AuthService);
  const token = auth.accessToken();
  if (!token) {
    return next(req);
  }
  return next(req.clone({ setHeaders: { Authorization: `Bearer ${token}` } }));
};
