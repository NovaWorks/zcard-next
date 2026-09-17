import { watch, onBeforeUnmount, type Ref } from "vue";
export function useContentVideos(container: Ref<HTMLElement | null>) {
  let cleanup = () => {};
  watch(
    container,
    () => {
      cleanup();
      const videos = Array.from(container.value?.querySelectorAll("video") || []);
      const errors: HTMLElement[] = [];
      const fail = (event: Event) => {
        const video = event.currentTarget as HTMLVideoElement;
        if (video.nextElementSibling?.classList.contains("video-playback-error")) return;
        const message = document.createElement("p");
        message.className = "video-playback-error";
        message.textContent = "视频暂时无法播放，请检查网络或联系店主。";
        message.setAttribute("role", "status");
        video.after(message);
        errors.push(message);
      };
      for (const video of videos) {
        video.autoplay = false;
        video.controls = true;
        video.playsInline = true;
        video.preload = "none";
        video.addEventListener("error", fail);
      }
      cleanup = () => {
        for (const video of videos) {
          video.pause();
          video.removeEventListener("error", fail);
        }
        for (const e of errors) e.remove();
      };
    },
    { flush: "post" },
  );
  onBeforeUnmount(() => cleanup());
}
