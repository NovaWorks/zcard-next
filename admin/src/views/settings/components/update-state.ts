import type { UpdateStatus } from "@/service/api/update";

export function isUpdatePending(st: UpdateStatus | null): boolean {
  if (!st) return false;
  if (st.busy) return true;
  if (st.phase === "failed" || st.phase === "rolled_back") return false;
  return ["checking", "backing_up", "downloading", "applying", "restarting", "verifying"].includes(st.phase)
    || Boolean(st.target_version && st.target_version !== st.current_version);
}

export function isUpdateComplete(st: UpdateStatus): boolean {
  return !st.busy && st.phase === "idle"
    && Boolean(st.target_version && st.current_version === st.target_version);
}
