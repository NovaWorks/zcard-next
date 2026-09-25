import { onMounted, onUnmounted } from 'vue';

// Reconnect/return refreshes only visible pages, coalescing focus + visibility events.
export function usePageRecovery(refresh: () => Promise<unknown>, active: () => boolean) {
  let lastAttempt = Date.now();
  let running = false;
  const resume = async (event: Event) => {
    if (document.visibilityState !== 'visible' || !active() || running) return;
    const age = Date.now() - lastAttempt;
    if (age < (event.type === 'online' ? 5000 : 60_000)) return;
    running = true;
    lastAttempt = Date.now();
    try { await refresh(); } catch { /* Visible loaders own their failure state. */ }
    finally { running = false; }
  };
  onMounted(() => {
    document.addEventListener('visibilitychange', resume);
    window.addEventListener('focus', resume);
    window.addEventListener('online', resume);
  });
  onUnmounted(() => {
    document.removeEventListener('visibilitychange', resume);
    window.removeEventListener('focus', resume);
    window.removeEventListener('online', resume);
  });
}
