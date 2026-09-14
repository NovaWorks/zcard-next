import { ref } from "vue";
// Memory only: never persist recovery credentials in browser storage.
export const recoveryCodes = ref<string[]>([]);
export const recoveryTicket = ref("");
