import { createApp } from "vue";
import "uno.css";
import Harness from "./Harness.vue";
window.$message = { success() {}, error() {}, warning() {} } as any;
createApp(Harness).directive("auth", {}).mount("#app");
