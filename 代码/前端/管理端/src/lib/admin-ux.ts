import { useBlocker } from "react-router-dom";
import { useEffect, useLayoutEffect } from "react";
import { createRequestId } from "./api";
import { confirmAdmin } from "./admin-confirm";

// A product editor can be mounted inside a second guarded page. Router
// navigation and logout must consider both, but show a single confirmation.
const editors = new Set<{ busy: boolean }>();

export function useUnsavedChanges(dirty: boolean, busy = false) {
  useLayoutEffect(() => {
    if (!dirty && !busy) return;
    const editor = { busy };
    editors.add(editor);
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => {
      editors.delete(editor);
      window.removeEventListener("beforeunload", warn);
    };
  }, [dirty, busy]);
}

export async function canLeaveEditor(
  forLogout: boolean,
  notify: (message: string) => void,
  signal?: AbortSignal,
) {
  const blocked = () => {
    for (const editor of editors) {
      if (editor.busy) {
        notify("正在保存或上传，请等操作完成后再试。");
        return true;
      }
    }
    return false;
  };
  if (blocked()) return false;
  if (!editors.size) return true;
  const accepted = await confirmAdmin("离开后，当前未保存的修改会丢失。", {
    confirmLabel: forLogout ? "放弃修改并退出" : "放弃修改并离开",
    signal,
  });
  return accepted && !signal?.aborted && !blocked();
}

export function useAdminNavigationGuard(
  signedIn: boolean,
  notify: (message: string) => void,
) {
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      signedIn &&
      editors.size > 0 &&
      (currentLocation.pathname !== nextLocation.pathname ||
        currentLocation.search !== nextLocation.search),
  );
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    const controller = new AbortController();
    void canLeaveEditor(false, notify, controller.signal).then((allowed) => {
      if (controller.signal.aborted) return;
      if (allowed) blocker.proceed();
      else blocker.reset();
    });
    return () => controller.abort();
  }, [blocker, notify]);
}

export const unitLabels: Record<string, string> = {
  account: "元 / 个账号",
  hour: "元 / 小时",
  day: "元 / 天",
  week: "元 / 周",
  month: "元 / 月",
  period: "元 / 时段",
  unknown: "待核对（历史数据）",
};
export interface EditableOption {
  value: string;
  label: string;
  enabled?: boolean;
  [key: string]: unknown;
}
export interface EditableGroup {
  key: string;
  label: string;
  multiple?: boolean;
  enabled?: boolean;
  required?: boolean;
  placeholder?: string;
  type?: string;
  options: EditableOption[];
  [key: string]: unknown;
}
export function parseFilters(raw: string): EditableGroup[] {
  const decoded: unknown = JSON.parse(raw);
  const value = Array.isArray(decoded)
    ? decoded.map((group) =>
        group && group.type === "text" && group.options == null
          ? { ...group, options: [] }
          : group,
      )
    : decoded;
  if (
    !Array.isArray(value) ||
    value.some(
      (g) =>
        !g ||
        typeof g.key !== "string" ||
        typeof g.label !== "string" ||
        !Array.isArray(g.options) ||
        g.options.some(
          (o: EditableOption) =>
            !o || typeof o.value !== "string" || typeof o.label !== "string",
        ),
    )
  )
    throw new Error("筛选资料格式异常，请使用高级配置检查原始内容。");
  return value;
}
export function updateAttribute(raw: string, key: string, value: unknown) {
  const attributes = JSON.parse(raw);
  if (
    !attributes ||
    typeof attributes !== "object" ||
    Array.isArray(attributes)
  )
    throw new Error("账号属性格式异常，请先检查高级资料。");
  return JSON.stringify({ ...attributes, [key]: value }, null, 2);
}
export function makeCode(prefix: string) {
  return `${prefix}-${createRequestId().replaceAll("-", "").slice(0, 20)}`;
}
export const publicSite = String(
  import.meta.env.VITE_PUBLIC_SITE_URL ?? "",
).replace(/\/$/, "");

export const locations = {
  contacts: {
    title: "客服与二维码",
    where: "用户站 → 联系客服弹窗",
    hint: "选择客服，修改联系方式或二维码，再保存。勾选的咨询入口会显示这位客服。",
  },
  pages: {
    title: "说明与协议",
    where: "用户站 → 对应说明页面",
    hint: "选择页面，修改标题和正文。勾选“公开展示”后保存，用户才能打开该页面。",
  },
  categories: {
    title: "交易分类与报价单位",
    where: "用户站 → 账号集市 / 发布账号",
    hint: "维护交易分类与允许的报价单位。区服等固定选项统一在共享字典维护，停用分类会隐藏其中的公开商品。",
  },
  settings: {
    title: "首页装修",
    where: "用户站 → 首页",
    hint: "在左侧选择要修改的区域，右侧填写内容。示意图会同步变化，保存后用户站刷新生效。",
  },
};
