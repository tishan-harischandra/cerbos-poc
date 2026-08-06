import { provideHttpClient, withInterceptors } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { HttpClient } from '@angular/common/http';
import { TestBed } from '@angular/core/testing';

import { AuthService } from './auth.service';
import { authInterceptor } from './auth.interceptor';

describe('authInterceptor', () => {
  it('attaches a bearer token to an /api/ call when one is available', () => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([authInterceptor])),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { accessToken: () => 'a-token' } },
      ],
    });

    TestBed.inject(HttpClient).get('/api/admin/authz/resources').subscribe();

    const request = TestBed.inject(HttpTestingController).expectOne(
      '/api/admin/authz/resources',
    );
    expect(request.request.headers.get('Authorization')).toEqual('Bearer a-token');
  });

  it('leaves a request with no token unmodified', () => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([authInterceptor])),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { accessToken: () => null } },
      ],
    });

    TestBed.inject(HttpClient).get('/api/admin/authz/resources').subscribe();

    const request = TestBed.inject(HttpTestingController).expectOne(
      '/api/admin/authz/resources',
    );
    expect(request.request.headers.has('Authorization')).toBe(false);
  });

  it('never attaches a token to a request outside /api/', () => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([authInterceptor])),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { accessToken: () => 'a-token' } },
      ],
    });

    TestBed.inject(HttpClient)
      .post('http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/token', {})
      .subscribe();

    const request = TestBed.inject(HttpTestingController).expectOne(
      'http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/token',
    );
    expect(request.request.headers.has('Authorization')).toBe(false);
  });
});
