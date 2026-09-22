// The error types the API layer throws, so callers can tell a rejected request
// from an unreachable server without inspecting message text. Kept in their own
// module because both the client and the outbox need them.

// ApiError is a response the server rejected: `status` is its HTTP status and
// the message is the normalized payload (a field error or an {error} string).
export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

// NetworkError is a transport failure — no response at all, or a timeout. It is
// what the offline layer reacts to: a request that never reached the server is
// safe to replay, which a rejected one is not.
export class NetworkError extends Error {
  constructor(message = "Network error: could not reach the API server") {
    super(message);
    this.name = "NetworkError";
  }
}

export function isNetworkError(err: unknown): err is NetworkError {
  return err instanceof NetworkError;
}
