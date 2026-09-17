<script setup lang="ts">
import { ref, watch } from "vue";
import { NModal, NInput, NButton, NProgress, NAlert } from "naive-ui";
import { fetchMediaList, uploadVideo } from "@/service/api/media";
import { pickMedia } from "../media-picker";
import { videoPicker, closeVideo, validVideoURL, videoHTML } from "./index";
const src = ref("");
const poster = ref("");
const error = ref("");
const uploading = ref(false);
const progress = ref(0);
const limit = ref(100 * 1024 * 1024);
const previewOK = ref(false);
const input = ref<HTMLInputElement>();
let controller: AbortController | null = null;
watch([src, poster], () => {
  previewOK.value = false;
  error.value = "";
});
watch(
  () => videoPicker.show,
  async (show) => {
    if (!show) {
      controller?.abort();
      return;
    }
    src.value = "";
    poster.value = "";
    error.value = "";
    progress.value = 0;
    const { data } = await fetchMediaList({ kind: "video", page_size: 1 });
    if (data?.max_video_bytes) limit.value = Number(data.max_video_bytes);
  },
);
function cancelUpload() {
  controller?.abort();
}
async function chooseVideo() {
  const urls = await pickMedia({ kind: "video" });
  if (urls?.[0]) src.value = urls[0];
}
async function choosePoster() {
  const urls = await pickMedia();
  if (urls?.[0]) poster.value = urls[0];
}
async function onFile(event: Event) {
  const el = event.target as HTMLInputElement;
  const file = el.files?.[0];
  el.value = "";
  if (!file) return;
  if (!/\.mp4$/i.test(file.name) || file.size > limit.value) {
    error.value = `请选择不超过 ${Math.round(limit.value / 1024 / 1024)} MB 的 MP4 视频`;
    return;
  }
  controller = new AbortController();
  const active = controller;
  uploading.value = true;
  progress.value = 0;
  error.value = "";
  try {
    const { data, error: failed } = await uploadVideo(
      file,
      active.signal,
      (n) => (progress.value = n),
    );
    if (active.signal.aborted) return;
    if (data) src.value = data.url;
    else error.value = failed ? "上传失败，请检查文件格式、大小或网络后重试" : "上传失败";
  } finally {
    if (controller === active) {
      uploading.value = false;
      controller = null;
    }
  }
}
function insert() {
  if (!validVideoURL(src.value.trim()) || (poster.value && !validVideoURL(poster.value.trim()))) {
    error.value = "请输入 HTTPS 视频文件直链，或从素材库选择";
    return;
  }
  if (!previewOK.value) {
    error.value = "请先确认视频预览可播放；平台页面链接不属于视频直链";
    return;
  }
  closeVideo(videoHTML(src.value.trim(), poster.value.trim()));
}
</script>
<template>
  <NModal
    :show="videoPicker.show"
    preset="card"
    title="插入视频"
    style="width: min(640px, 94vw)"
    :mask-closable="false"
    @update:show="(v) => !v && closeVideo()"
  >
    <div class="flex flex-col gap-12px">
      <label for="video-url">视频地址</label>
      <NInput
        id="video-url"
        v-model:value="src"
        :disabled="uploading"
        placeholder="HTTPS 视频文件直链（不支持平台播放页面）"
      />
      <div class="flex flex-wrap gap-8px">
        <NButton :disabled="uploading" @click="chooseVideo">从视频素材库选择</NButton>
        <NButton v-auth="'media:upload'" :disabled="uploading" @click="input?.click()"
          >上传本地视频</NButton
        >
        <input ref="input" type="file" accept="video/mp4,.mp4" hidden @change="onFile" />
      </div>
      <p class="text-12px text-gray-500">
        MP4（H.264 / AAC），最大
        {{ Math.round(limit / 1024 / 1024) }} MB。上传的视频为公开素材，支持重复使用。
      </p>
      <div v-if="uploading" aria-live="polite">
        <NProgress type="line" :percentage="progress" /><NButton size="small" @click="cancelUpload"
          >取消上传</NButton
        >
      </div>
      <label for="video-poster">视频封面（可选）</label>
      <div class="flex gap-8px">
        <NInput
          id="video-poster"
          v-model:value="poster"
          placeholder="选择图片或填写 HTTPS 图片地址"
        /><NButton @click="choosePoster">选择封面</NButton>
      </div>
      <video
        v-if="validVideoURL(src)"
        :key="src + poster"
        :src="src"
        :poster="validVideoURL(poster) ? poster : undefined"
        controls
        playsinline
        preload="metadata"
        style="width: 100%; max-height: 300px; background: #111; border-radius: 8px"
        @loadeddata="previewOK = true"
        @error="
          error = '视频无法播放，请检查地址、有效期、防盗链或编码格式';
          previewOK = false;
        "
      />
      <NAlert v-if="error" type="error" :show-icon="false">{{ error }}</NAlert>
      <div class="flex justify-end gap-8px">
        <NButton @click="closeVideo()">取消</NButton
        ><NButton type="primary" :disabled="uploading || !previewOK" @click="insert"
          >插入正文</NButton
        >
      </div>
    </div>
  </NModal>
</template>
