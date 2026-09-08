<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';

const props = defineProps<{ images: { src: string; alt: string }[]; initialIndex?: number }>();
const emit = defineEmits<{ (e: 'close'): void }>();
const dialog = ref<HTMLDialogElement | null>(null);
const stage = ref<HTMLElement | null>(null);
const index = ref(props.initialIndex || 0);
const current = computed(() => props.images[index.value]);
const rotation = ref(0);
const zoom = ref(1);
const x = ref(0);
const y = ref(0);
const natural = ref({ width: 0, height: 0 });
const area = ref({ width: 1, height: 1 });
const failed = ref(false);
const loaded = computed(() => natural.value.width > 0 && !failed.value);
const fit = computed(() => {
  const { width, height } = natural.value;
  if (!width || !height) return 1;
  const sideways = Math.abs(rotation.value % 180) === 90;
  return Math.min(Math.max(1, area.value.width - 24) / (sideways ? height : width), Math.max(1, area.value.height - 24) / (sideways ? width : height), 1);
});
const imageStyle = computed(() => ({
  width: `${natural.value.width * fit.value}px`,
  height: `${natural.value.height * fit.value}px`,
  transform: `translate(-50%, -50%) translate(${x.value}px, ${y.value}px) rotate(${rotation.value}deg) scale(${zoom.value})`,
}));
let observer: ResizeObserver | undefined;
let previousOverflow = '';
let previousFocus: HTMLElement | null = null;
const pointers = new Map<number, { x: number; y: number }>();
let backgroundTap = false;

function reset() { rotation.value = 0; zoom.value = 1; x.value = 0; y.value = 0; }
function changeImage(step: number) {
  index.value = (index.value + step + props.images.length) % props.images.length;
}
watch(index, () => { reset(); natural.value = { width: 0, height: 0 }; failed.value = false; pointers.clear(); backgroundTap = false; });
function setZoom(value: number) { zoom.value = Math.min(32, Math.max(0.25, value)); }
function rotate(step: number) { rotation.value += step; x.value = 0; y.value = 0; }
function onLoad(event: Event) {
  const img = event.target as HTMLImageElement;
  natural.value = { width: img.naturalWidth, height: img.naturalHeight };
}
function wheel(event: WheelEvent) { if (loaded.value) setZoom(zoom.value * Math.exp(-event.deltaY * 0.002)); }
function pointerDown(event: PointerEvent) {
  if (event.pointerType === 'mouse' && event.button !== 0) return;
  backgroundTap = pointers.size === 0 && event.target === stage.value;
  pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
  stage.value?.setPointerCapture(event.pointerId);
}
function pointerMove(event: PointerEvent) {
  const previous = pointers.get(event.pointerId);
  if (!previous) return;
  const next = { x: event.clientX, y: event.clientY };
  const dx = next.x - previous.x, dy = next.y - previous.y;
  if (Math.abs(dx) + Math.abs(dy) > 2) backgroundTap = false;
  if (pointers.size === 2) {
    backgroundTap = false;
    const other = [...pointers.entries()].find(([id]) => id !== event.pointerId)![1];
    const before = Math.hypot(previous.x - other.x, previous.y - other.y);
    const after = Math.hypot(next.x - other.x, next.y - other.y);
    if (before > 0) setZoom(zoom.value * after / before);
    x.value += dx / 2; y.value += dy / 2;
  } else if (pointers.size === 1) { x.value += dx; y.value += dy; }
  pointers.set(event.pointerId, next);
}
function pointerUp(event: PointerEvent) {
  const close = backgroundTap && pointers.size === 1 && event.type === 'pointerup';
  pointers.delete(event.pointerId);
  backgroundTap = false;
  if (close) emit('close');
}
function keydown(event: KeyboardEvent) {
  if (event.key === 'ArrowLeft' && props.images.length > 1) changeImage(-1);
  else if (event.key === 'ArrowRight' && props.images.length > 1) changeImage(1);
  else if (event.key === '+' || event.key === '=') setZoom(zoom.value * 1.25);
  else if (event.key === '-') setZoom(zoom.value / 1.25);
  else return;
  event.preventDefault();
}
onMounted(async () => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  previousOverflow = document.body.style.overflow;
  document.body.style.overflow = 'hidden';
  dialog.value?.showModal();
  await nextTick();
  if (!stage.value) return;
  observer = new ResizeObserver(([entry]) => { area.value = { width: entry.contentRect.width, height: entry.contentRect.height }; });
  observer.observe(stage.value);
});
onBeforeUnmount(() => {
  observer?.disconnect();
  document.body.style.overflow = previousOverflow;
  dialog.value?.close();
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
});
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="image-viewer" aria-label="商品图片预览" @cancel.prevent="emit('close')" @keydown="keydown">
      <header class="iv-header">
        <span class="iv-title">{{ current?.alt || '商品图片' }}</span>
        <span aria-live="polite">{{ index + 1 }} / {{ images.length }}</span>
        <button type="button" class="iv-close" aria-label="关闭图片预览" autofocus @click="emit('close')">关闭 ✕</button>
      </header>
      <div ref="stage" class="iv-stage" @wheel.prevent="wheel" @pointerdown="pointerDown" @pointermove="pointerMove" @pointerup="pointerUp" @pointercancel="pointerUp" @lostpointercapture="pointerUp" @dblclick="setZoom(zoom === 1 ? 2 : 1)">
        <span v-if="failed" class="iv-status" role="status">图片加载失败，请关闭后重试</span>
        <span v-else-if="!loaded" class="iv-status" role="status">正在加载图片…</span>
        <img v-if="current" :key="index" :src="current.src" :alt="current.alt" :style="imageStyle" :class="{ 'iv-hidden': !loaded }" draggable="false" @load="onLoad" @error="failed = true" />
      </div>
      <footer class="iv-footer">
        <div class="iv-toolbar" role="group" aria-label="图片操作">
          <button v-if="images.length > 1" type="button" aria-label="上一张图片" @click="changeImage(-1)">上一张</button>
          <button type="button" aria-label="缩小图片" :disabled="!loaded || zoom <= 0.25" @click="setZoom(zoom / 1.25)">−</button>
          <span class="iv-zoom">{{ Math.round(zoom * 100) }}%</span>
          <button type="button" aria-label="放大图片" :disabled="!loaded || zoom >= 32" @click="setZoom(zoom * 1.25)">＋</button>
          <button type="button" aria-label="向左旋转图片" :disabled="!loaded" @click="rotate(-90)">↶ 左转</button>
          <button type="button" aria-label="向右旋转图片" :disabled="!loaded" @click="rotate(90)">↷ 右转</button>
          <button type="button" :disabled="!loaded" @click="reset">适应窗口</button>
          <button type="button" :disabled="!loaded" @click="setZoom(1 / fit); x = 0; y = 0">原始大小</button>
          <button v-if="images.length > 1" type="button" aria-label="下一张图片" @click="changeImage(1)">下一张</button>
        </div>
        <p class="iv-hint">拖动查看 · 滚轮或双指缩放 · 双击放大</p>
      </footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.image-viewer { position: fixed; inset: 0; width: 100%; max-width: none; height: 100%; height: 100dvh; max-height: none; margin: 0; padding: 0; border: 0; background: rgba(12, 16, 23, .97); color: #fff; overscroll-behavior: contain; }
.image-viewer[open] { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; }
.image-viewer::backdrop { background: #0c1017; }
.iv-header { display: flex; gap: 12px; align-items: center; padding: calc(8px + env(safe-area-inset-top)) 16px 8px; }
.iv-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.image-viewer button { min-width: 44px; min-height: 44px; padding: 8px 12px; border: 1px solid #64748b; border-radius: 8px; background: #263244; color: #fff; font: inherit; font-size: 14px; cursor: pointer; }
.image-viewer button:hover { background: #3b4a60; }
.image-viewer button:focus-visible { outline: 2px solid #fff; outline-offset: 3px; }
.image-viewer button:disabled { opacity: .4; cursor: default; }
.iv-stage { min-height: 0; overflow: hidden; position: relative; touch-action: none; cursor: grab; }
.iv-stage:active { cursor: grabbing; }
.iv-stage img { position: absolute; left: 50%; top: 50%; max-width: none; max-height: none; user-select: none; transform-origin: center; }
.iv-hidden { visibility: hidden; }
.iv-status { position: absolute; inset: 0; display: grid; place-items: center; pointer-events: none; }
.iv-footer { padding: 8px 12px calc(8px + env(safe-area-inset-bottom)); background: #131c29; }
.iv-toolbar { display: flex; flex-wrap: wrap; justify-content: center; align-items: center; gap: 8px; }
.iv-zoom { min-width: 48px; font-size: 13px; text-align: center; }
.iv-hint { margin: 8px 0 0; text-align: center; font-size: 12px; color: #cbd5e1; }
@media (max-width: 600px) { .iv-header { padding-right: 12px; padding-left: 12px; } .iv-toolbar { gap: 6px; } .image-viewer button { padding: 8px; } }
</style>
