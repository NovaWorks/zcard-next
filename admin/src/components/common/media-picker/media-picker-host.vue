<script setup lang="ts">
/**
 * 素材选择器全局挂载点（App.vue 引一次；弹窗常驻、按 mediaPickerState.show 显隐）。
 */
import MediaPicker from "./media-picker.vue";
import VideoPicker from "../video-picker/video-picker.vue";
</script>

<template>
  <VideoPicker />
  <MediaPicker />
</template>
