// Bounded retries are opt-in for public, idempotent reads only.
export async function readJSON<T>(url: string, init?: RequestInit, timeoutMs = 8000): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), timeoutMs);
    let retryable = true;
    try {
      const response = await fetch(url, { ...init, signal: controller.signal });
      if (!response.ok) {
        retryable = response.status === 408 || response.status >= 500;
        throw new Error(`HTTP ${response.status}`);
      }
      return await response.json() as T;
    } catch (error) {
      if (!retryable || attempt >= 1) throw error;
    } finally {
      clearTimeout(timeout);
    }
    await new Promise(resolve => setTimeout(resolve, 350));
  }
}
