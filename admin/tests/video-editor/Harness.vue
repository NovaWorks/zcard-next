<script setup lang="ts">
import { ref } from "vue";
import Editor from "../../src/components/common/md-html-editor/index.vue";
import MediaHost from "../../src/components/common/media-picker/media-picker-host.vue";
const content = ref("<p>视频教程</p>");
</script>
<template>
  <div style="padding: 24px; max-width: 1000px; margin: auto">
    <h1>视频编辑器回归</h1>
    <Editor v-model="content" /><textarea
      id="result"
      :value="content"
      aria-label="保存内容"
      style="width: 100%; height: 100px"
    /><MediaHost />
  </div>
</template>
