import { Avatar } from "@chatui/core/es/components/Avatar";
import { Button } from "@chatui/core/es/components/Button";
import { ProtectedImage } from "./MediaSecurity";
import { useEffect, useRef, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  ImagePlus,
  RotateCcw,
  X,
} from "lucide-react";
import {
  CHAT_IMAGE_LIMIT,
  uploadChatImage,
  validateChatImage,
} from "./chat-upload";
import type { ChatImage } from "./chat-types";

export function ChatAvatar({
  src,
  name,
  className = "",
}: {
  src?: string;
  name: string;
  className?: string;
}) {
  const [failed, setFailed] = useState("");
  return (
    <Avatar className={`chat-avatar ${className}`} shape="square">
      {src && failed !== src ? (
        <ProtectedImage src={src} alt="" onError={() => setFailed(src)} />
      ) : (
        <span>{Array.from(name || "清叶")[0]}</span>
      )}
    </Avatar>
  );
}

type DraftImage = {
  key: string;
  file: File;
  preview: string;
  mediaID?: string;
  progress: number;
  error?: string;
  controller: AbortController;
};
export interface ChatMediaServices {
  request: Parameters<typeof uploadChatImage>[0];
  createRequestId: () => string;
}
export function useChatImages(
  identity: string,
  selected: string,
  report: (error: string) => void,
  { request, createRequestId }: ChatMediaServices,
) {
  const drafts = useRef(new Map<string, DraftImage[]>());
  const [, render] = useState(0);
  const generation = useRef(0);
  const release = (item: DraftImage) => {
    item.controller.abort();
    URL.revokeObjectURL(item.preview);
  };
  useEffect(() => {
    generation.current++;
    drafts.current.forEach((items) => items.forEach(release));
    drafts.current.clear();
    render((x) => x + 1);
    return () => {
      generation.current++;
      drafts.current.forEach((items) => items.forEach(release));
      drafts.current.clear();
    };
  }, [identity]);
  function update(
    id: string,
    item: DraftImage,
    changes: Partial<DraftImage>,
    current: number,
  ) {
    if (
      generation.current !== current ||
      !drafts.current.get(id)?.includes(item)
    )
      return;
    Object.assign(item, changes);
    render((x) => x + 1);
  }
  async function upload(id: string, item: DraftImage) {
    const current = generation.current;
    update(id, item, { error: undefined, progress: 0 }, current);
    try {
      const mediaID = await uploadChatImage(
        request,
        item.file,
        { purpose: "chat", conversation_id: id },
        item.controller.signal,
        (progress) => update(id, item, { progress }, current),
      );
      update(id, item, { mediaID, progress: 100 }, current);
    } catch (e) {
      if (!item.controller.signal.aborted)
        update(
          id,
          item,
          { error: e instanceof Error ? e.message : "上传失败" },
          current,
        );
    }
  }
  function add(files: File[]) {
    if (!selected) return;
    const items = drafts.current.get(selected) ?? [];
    if (items.length + files.length > CHAT_IMAGE_LIMIT) {
      report("每条消息最多添加 9 张图片，可分次发送。");
      return;
    }
    const invalid = files.map((file) => validateChatImage(file)).find(Boolean);
    if (invalid) {
      report(invalid);
      return;
    }
    const created = files.map((file) => ({
      key: createRequestId(),
      file,
      preview: URL.createObjectURL(file),
      progress: 0,
      controller: new AbortController(),
    }));
    drafts.current.set(selected, [...items, ...created]);
    render((x) => x + 1);
    // Two upload workers keep large selections from saturating the connection.
    let index = 0;
    const work = async () => {
      while (index < created.length) {
        const item = created[index++];
        if (!item.controller.signal.aborted) await upload(selected, item);
      }
    };
    void work();
    void work();
  }
  function remove(key: string) {
    const items = drafts.current.get(selected) ?? [],
      item = items.find((x) => x.key === key);
    if (item) release(item);
    drafts.current.set(
      selected,
      items.filter((x) => x.key !== key),
    );
    render((x) => x + 1);
  }
  function clear(id: string, keys: string[]) {
    const items = drafts.current.get(id) ?? [];
    items.filter((x) => keys.includes(x.key)).forEach(release);
    drafts.current.set(
      id,
      items.filter((x) => !keys.includes(x.key)),
    );
    render((x) => x + 1);
  }
  const items = drafts.current.get(selected) ?? [];
  return {
    items,
    add,
    remove,
    clear,
    ready: items.every((x) => !!x.mediaID),
    retry: (key: string) => {
      const item = items.find((x) => x.key === key);
      if (item?.error) void upload(selected, item);
    },
  };
}

export function ImagePicker({
  images,
  disabled,
}: {
  images: ReturnType<typeof useChatImages>;
  disabled: boolean;
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <Button
        type="button"
        onClick={() => input.current?.click()}
        disabled={disabled || images.items.length >= CHAT_IMAGE_LIMIT}
        aria-label="添加聊天图片"
      >
        <ImagePlus size={19} />
        <span>图片</span>
      </Button>
      <input
        ref={input}
        hidden
        type="file"
        multiple
        accept="image/jpeg,image/png,image/webp"
        onChange={(e) => {
          images.add(Array.from(e.target.files ?? []));
          e.target.value = "";
        }}
      />
    </>
  );
}

export function ImageDrafts({
  images,
  disabled,
}: {
  images: ReturnType<typeof useChatImages>;
  disabled: boolean;
}) {
  if (!images.items.length) return null;
  return (
    <div className="chat-draft-images" aria-label="待发送图片">
      {images.items.map((item) => (
        <div className="chat-draft-image" key={item.key}>
          <ProtectedImage src={item.preview} alt={item.file.name} />
          <Button
            type="button"
            disabled={disabled}
            aria-label={`移除图片 ${item.file.name}`}
            onClick={() => images.remove(item.key)}
          >
            <X size={12} />
          </Button>
          {item.error ? (
            <Button
              className="chat-upload-state failed"
              title={item.error}
              type="button"
              disabled={disabled}
              onClick={() => images.retry(item.key)}
            >
              <RotateCcw size={12} />
              重试
            </Button>
          ) : !item.mediaID ? (
            <span className="chat-upload-state" role="status">
              {item.progress >= 95 ? "处理中" : `${item.progress}%`}
            </span>
          ) : (
            <span className="chat-upload-state">待发送</span>
          )}
        </div>
      ))}
      <small>可配文字一起发送 · 单张 10 MB 以内</small>
    </div>
  );
}

export function ChatImageGrid({
  images,
  open,
  loaded,
}: {
  images: ChatImage[];
  open: (images: ChatImage[], index: number) => void;
  loaded: () => void;
}) {
  return (
    <div className={`chat-image-grid ${images.length === 1 ? "single" : ""}`}>
      {images.map((image, index) => (
        <Button
          type="button"
          key={image.id}
          aria-label={`查看第 ${index + 1} 张聊天图片`}
          onClick={() => open(images, index)}
        >
          <ProtectedImage
            src={image.thumbnail_url}
            alt={`聊天图片 ${index + 1}`}
            loading="lazy"
            onLoad={loaded}
          />
        </Button>
      ))}
    </div>
  );
}

export function ChatLightbox({
  gallery,
  close,
}: {
  gallery: { images: ChatImage[]; index: number };
  close: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [index, setIndex] = useState(gallery.index),
    [failed, setFailed] = useState(false),
    [retry, setRetry] = useState(0);
  const image = gallery.images[index];
  useEffect(() => {
    const node = dialog.current;
    node?.showModal();
    return () => node?.close();
  }, []);
  useEffect(() => setFailed(false), [index]);
  const move = (delta: number) =>
    setIndex(
      (i) => (i + delta + gallery.images.length) % gallery.images.length,
    );
  return (
    <dialog
      ref={dialog}
      className="chat-lightbox"
      aria-label="聊天图片预览"
      onCancel={close}
      onClick={(e) => {
        if (e.target === e.currentTarget) close();
      }}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft") move(-1);
        if (e.key === "ArrowRight") move(1);
      }}
    >
      <header>
        <span>
          图片预览{" "}
          <b>
            {index + 1} / {gallery.images.length}
          </b>
        </span>
        <Button type="button" aria-label="关闭图片预览" onClick={close}>
          <X />
        </Button>
      </header>
      <div className="chat-lightbox-stage">
        {gallery.images.length > 1 && (
          <Button
            type="button"
            aria-label="上一张图片"
            onClick={() => move(-1)}
          >
            <ChevronLeft />
          </Button>
        )}
        {failed ? (
          <Button
            type="button"
            onClick={() => {
              setFailed(false);
              setRetry((x) => x + 1);
            }}
          >
            图片读取失败，点击重试
          </Button>
        ) : (
          <ProtectedImage
            key={`${image.id}:${retry}`}
            src={image.url + (retry ? `?retry=${retry}` : "")}
            alt={`聊天图片 ${index + 1}`}
            onError={() => setFailed(true)}
          />
        )}
        {gallery.images.length > 1 && (
          <Button type="button" aria-label="下一张图片" onClick={() => move(1)}>
            <ChevronRight />
          </Button>
        )}
      </div>
      <p>左右方向键切换 · Esc 关闭</p>
    </dialog>
  );
}
