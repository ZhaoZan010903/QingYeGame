import { responseCrypto } from "./response-crypto";

export const CHAT_IMAGE_LIMIT = 9;
export const CHAT_IMAGE_BYTES = 10 * 1024 * 1024;
type Request = (
  path: string,
  method?: string,
  body?: unknown,
  signal?: AbortSignal,
) => Promise<{ data?: any }>;

export function validateChatImage(
  file: Pick<File, "name" | "type" | "size">,
  avatar = false,
): string {
  const extensions: Record<string, RegExp> = {
    "image/jpeg": /\.jpe?g$/i,
    "image/png": /\.png$/i,
    "image/webp": /\.webp$/i,
  };
  if (!extensions[file.type]?.test(file.name))
    return "请选择 JPG、PNG 或 WebP 图片。";
  const max = avatar ? 2 * 1024 * 1024 : CHAT_IMAGE_BYTES;
  if (file.size <= 0 || file.size > max)
    return avatar
      ? "头像大小应在 2 MB 以内。"
      : "单张图片大小应在 10 MB 以内。";
  return "";
}

/** Binary upload stays on authenticated HTTPS; only media IDs enter the realtime message. */
export async function uploadChatImage(
  request: Request,
  file: File,
  scope: { purpose: "chat" | "avatar"; conversation_id?: string },
  signal: AbortSignal,
  progress: (value: number) => void,
): Promise<string> {
  const invalid = validateChatImage(file, scope.purpose === "avatar");
  if (invalid) throw new Error(invalid);
  const { data: ticket } = await request(
    "/uploads",
    "POST",
    {
      filename: file.name,
      mime_type: file.type,
      byte_size: file.size,
      ...scope,
    },
    signal,
  );
  const url = new URL(ticket.upload_url, window.location.origin);
  if (
    url.origin !== window.location.origin ||
    !url.pathname.startsWith("/api/v1/uploads/")
  )
    throw new Error("上传地址无效，请重试。");
  const context = await responseCrypto.prepare(url.href, "PUT", {
    "Content-Type": file.type,
  });
  await new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const clean = () => signal.removeEventListener("abort", abort);
    xhr.open("PUT", url.href);
    xhr.responseType = "arraybuffer";
    xhr.withCredentials = true;
    xhr.timeout = 120000;
    context.headers.forEach((value, key) => xhr.setRequestHeader(key, value));
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable)
        progress(Math.min(95, Math.round((e.loaded / e.total) * 95)));
    };
    xhr.onload = async () => {
      clean();
      try {
        const response = await responseCrypto.decodeXHR(xhr, context);
        if (!response.ok) throw new Error("图片上传失败，请重试。");
        resolve();
      } catch (error) {
        reject(error);
      }
    };
    xhr.onerror = xhr.ontimeout = () => {
      clean();
      reject(new Error("图片上传中断，请重试。"));
    };
    xhr.onabort = () => {
      clean();
      reject(new DOMException("Aborted", "AbortError"));
    };
    signal.addEventListener("abort", abort, { once: true });
    if (signal.aborted) {
      clean();
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    xhr.send(file);
  });
  await request(
    `/uploads/${encodeURIComponent(ticket.media_id)}/complete`,
    "POST",
    undefined,
    signal,
  );
  progress(100);
  return ticket.media_id;
}
