import { useBlocker } from "react-router-dom";
import { useEffect } from "react";
import { createRequestId } from "./api";

/** Every editor uses the same leave guard, including browser Back/Forward. */
export function useUnsavedChanges(dirty: boolean, busy = false) {
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      (dirty || busy) &&
      (currentLocation.pathname !== nextLocation.pathname ||
        currentLocation.search !== nextLocation.search),
  );
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (busy) {
      window.alert("正在保存或上传，请处理完成后再离开。");
      blocker.reset();
    } else if (
      window.confirm("还有未保存的内容。离开后，这些修改不会保留。确定离开？")
    )
      blocker.proceed();
    else blocker.reset();
  }, [blocker, busy]);
  useEffect(() => {
    if (!dirty && !busy) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty, busy]);
  useEffect(() => {
    const leave = (event: Event) => {
      if (busy) {
        window.alert("正在保存或上传，请处理完成后再退出。");
        event.preventDefault();
      } else if (
        dirty &&
        !window.confirm("当前有未保存的修改，退出后不会保留。确认退出？")
      )
        event.preventDefault();
    };
    window.addEventListener("qingye-before-logout", leave);
    return () => window.removeEventListener("qingye-before-logout", leave);
  }, [dirty, busy]);
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
