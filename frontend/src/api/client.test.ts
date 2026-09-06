import { describe, expect, it } from 'vitest';
import { apiUrl, authHeaders } from './client';

describe('api client', () => {
  it('builds paths with leading slash', () => {
    expect(apiUrl('/api/v1/auth/me')).toBe('/api/v1/auth/me');
    expect(apiUrl('api/v1/auth/me')).toBe('/api/v1/auth/me');
  });

  it('attaches bearer tokens', () => {
    expect(authHeaders('tok123')).toEqual({
      'Content-Type': 'application/json',
      Authorization: 'Bearer tok123',
    });
    expect(authHeaders(null)).toEqual({ 'Content-Type': 'application/json' });
  });
});
