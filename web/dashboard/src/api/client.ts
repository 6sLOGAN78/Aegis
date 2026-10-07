import type { ErrorResponse } from './types';

export class ApiError extends Error {
  public status: number;
  public code: string;
  public requestId?: string;
  public details?: Record<string, unknown>;

  constructor(status: number, message: string, code = 'ERROR', requestId?: string, details?: Record<string, unknown>) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.requestId = requestId;
    this.details = details;
  }
}

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
  ifMatch?: string;
  idempotencyKey?: string;
}

export class ApiClient {
  private csrfToken: string | null = null;
  private onUnauthorizedCallback?: () => void;

  setCSRFToken(token: string | null) {
    this.csrfToken = token;
  }

  getCSRFToken(): string | null {
    return this.csrfToken;
  }

  onUnauthorized(cb: () => void) {
    this.onUnauthorizedCallback = cb;
  }

  async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const headers = new Headers(options.headers || {});
    const method = (options.method || 'GET').toUpperCase();

    // Attach CSRF token on mutating requests if available
    if (['POST', 'PUT', 'DELETE', 'PATCH'].includes(method) && this.csrfToken) {
      headers.set('X-CSRF-Token', this.csrfToken);
    }

    // Attach optimistic concurrency If-Match ETag if provided
    if (options.ifMatch) {
      headers.set('If-Match', options.ifMatch);
    }

    // Attach Idempotency-Key UUID if provided
    if (options.idempotencyKey) {
      headers.set('Idempotency-Key', options.idempotencyKey);
    }

    headers.set('Accept', 'application/json');

    let bodyPayload: BodyInit | undefined;
    if (options.body !== undefined) {
      if (typeof options.body === 'string') {
        bodyPayload = options.body;
        if (!headers.has('Content-Type')) {
          headers.set('Content-Type', 'application/json');
        }
      } else {
        bodyPayload = JSON.stringify(options.body);
        headers.set('Content-Type', 'application/json');
      }
    }

    const response = await fetch(path, {
      ...options,
      method,
      headers,
      body: bodyPayload,
      credentials: 'include', // Ensure HttpOnly cookie delivery
    });

    if (!response.ok) {
      let errPayload: ErrorResponse = {
        code: `HTTP_${response.status}`,
        message: response.statusText || `Request failed with HTTP ${response.status}`,
      };

      try {
        const parsed = await response.json();
        if (parsed && typeof parsed === 'object') {
          errPayload = {
            code: parsed.code || errPayload.code,
            message: parsed.message || errPayload.message,
            request_id: parsed.request_id,
            details: parsed.details,
            timestamp: parsed.timestamp,
          };
        }
      } catch {
        // Response was not JSON
      }

      if (response.status === 401) {
        if (this.onUnauthorizedCallback) {
          this.onUnauthorizedCallback();
        }
      }

      throw new ApiError(
        response.status,
        errPayload.message,
        errPayload.code,
        errPayload.request_id,
        errPayload.details,
      );
    }

    // Handle 204 No Content
    if (response.status === 204) {
      return {} as T;
    }

    return response.json() as Promise<T>;
  }

  get<T>(path: string, options?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...options, method: 'GET' });
  }

  post<T>(path: string, body?: unknown, options?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...options, method: 'POST', body });
  }

  put<T>(path: string, body?: unknown, options?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...options, method: 'PUT', body });
  }

  delete<T>(path: string, options?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...options, method: 'DELETE' });
  }
}

export const apiClient = new ApiClient();
