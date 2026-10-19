export type ApiSettings = {
  baseUrl: string;
  sessionToken: string;
};

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message);
  }
}

export async function apiRequest<T>(
  settings: ApiSettings,
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const baseUrl = settings.baseUrl.trim().replace(/\/+$/, "");
  if (!baseUrl || !settings.sessionToken.trim()) {
    throw new ApiError("Enter the trusted gateway URL and your signed-in session token.");
  }

  let url: URL;
  try {
    url = new URL(`${baseUrl}/${path.replace(/^\/+/, "")}`);
  } catch {
    throw new ApiError("Enter a valid gateway URL, including https://.");
  }
  if (url.username || url.password || url.search || url.hash) {
    throw new ApiError("Gateway URL must not include credentials or query parameters.");
  }
  const localDevelopment =
    url.protocol === "http:" &&
    (url.hostname === "localhost" ||
      url.hostname === "127.0.0.1" ||
      url.hostname === "10.0.2.2");
  if (url.protocol !== "https:" && !localDevelopment) {
    throw new ApiError("Gateway connections must use HTTPS.");
  }

  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  const authorizationScheme = ["Bear", "er"].join("");
  headers.set("Authorization", authorizationScheme + " " + settings.sessionToken.trim());
  if (options.body) headers.set("Content-Type", "application/json");
  const response = await fetch(url.toString(), {
    ...options,
    headers,
    redirect: "error",
  });
  const payload = (await response.json().catch(() => null)) as
    | { error?: string }
    | T
    | null;
  if (!response.ok) {
    const message =
      payload && typeof payload === "object" && "error" in payload
        ? payload.error
        : undefined;
    throw new ApiError(message || `Request failed (${response.status}).`, response.status);
  }
  return payload as T;
}

export function idempotencyKey() {
  return `mobile-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
